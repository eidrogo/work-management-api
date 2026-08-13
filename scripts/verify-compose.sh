#!/usr/bin/env bash
set -Eeuo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$repository_root"

for tool in docker curl jq go git ps; do
	command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done
docker compose version
docker info >/dev/null

run_id="$(git rev-parse --short=12 HEAD)-$(date -u +%Y%m%dT%H%M%SZ)-$$-${RANDOM}"
raw_project="${VERIFY_COMPOSE_PROJECT:-wma-verify-$run_id}"
PROJECT_NAME="$(printf '%s' "$raw_project" | tr '[:upper:]_.' '[:lower:]---' | tr -cd 'a-z0-9-')"
PROJECT_NAME="${PROJECT_NAME:0:63}"
[[ "$PROJECT_NAME" =~ ^[a-z0-9][a-z0-9-]*$ ]] || { echo "VERIFY_COMPOSE_PROJECT does not produce a valid Compose project name" >&2; exit 1; }

compose=(docker compose --project-name "$PROJECT_NAME" --env-file .env.example -f compose.yaml -f compose.verify.yaml)
label="com.docker.compose.project=$PROJECT_NAME"
project_resources() {
	local containers networks volumes
	containers="$(docker ps -aq --filter "label=$label")" || return
	networks="$(docker network ls -q --filter "label=$label")" || return
	volumes="$(docker volume ls -q --filter "label=$label")" || return
	printf '%s\n%s\n%s\n' "$containers" "$networks" "$volumes" | sed '/^[[:space:]]*$/d'
}
if ! existing="$(project_resources)"; then
	echo "Could not query Compose project resources for $PROJECT_NAME" >&2
	exit 1
fi
if [[ -n "$existing" ]]; then
	echo "Refusing nonempty Compose project $PROJECT_NAME" >&2
	printf '%s\n' "$existing" >&2
	exit 1
fi

if [[ -n "${VERIFY_LOG_DIR:-}" ]]; then
	log_dir="$VERIFY_LOG_DIR"
	mkdir -p "$log_dir"
	keep_logs=1
else
	log_dir="$(mktemp -d)"
	keep_logs=0
fi
mutated=0
active_child_pid=
active_child_pgid=

cleanup() {
	local status=$?
	trap - EXIT INT TERM
	set +e
	if ((status != 0)) && ((mutated)); then
		"${compose[@]}" ps --all >&2
		"${compose[@]}" logs --no-color --tail 300 >&2
	fi
	local down_status=0
	if ((mutated)); then
		"${compose[@]}" down --volumes --remove-orphans --timeout 10
		down_status=$?
	fi
	local leftovers resource_status=0
	leftovers="$(project_resources)" || resource_status=$?
	if ((down_status != 0)); then
		echo "Compose teardown failed with status $down_status" >&2
		((status != 0)) || status=1
	fi
	if ((resource_status != 0)); then
		echo "Could not audit Compose cleanup resources (status $resource_status)" >&2
		((status != 0)) || status=1
	elif [[ -n "$leftovers" ]]; then
		echo "Compose cleanup left labeled resources for $PROJECT_NAME:" >&2
		printf '%s\n' "$leftovers" >&2
		((status != 0)) || status=1
	else
		echo "PASS: Compose cleanup removed all project containers, networks, and volumes"
	fi
	if ((keep_logs == 0)); then
		rm -r "$log_dir"
	fi
	exit "$status"
}
on_signal() {
	local signal_name=$1 signal_status=$2
	local pid="${active_child_pid:-}" pgid="${active_child_pgid:-}" killer_pid=
	trap - INT TERM
	set +e
	if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
		if [[ "$pgid" =~ ^[0-9]+$ ]] && [[ "$pgid" == "$pid" ]]; then
			kill -"$signal_name" -- "-$pgid" 2>/dev/null
		else
			kill -"$signal_name" "$pid" 2>/dev/null
		fi
		(
			sleep 2
			if [[ "$pgid" =~ ^[0-9]+$ ]] && [[ "$pgid" == "$pid" ]]; then
				kill -KILL -- "-$pgid" 2>/dev/null
			else
				kill -KILL "$pid" 2>/dev/null
			fi
		) &
		killer_pid=$!
		wait "$pid" 2>/dev/null
		if [[ "$pgid" =~ ^[0-9]+$ ]] && [[ "$pgid" == "$pid" ]]; then
			kill -KILL -- "-$pgid" 2>/dev/null
		fi
		kill "$killer_pid" 2>/dev/null
		wait "$killer_pid" 2>/dev/null
	fi
	active_child_pid=
	active_child_pgid=
	exit "$signal_status"
}
trap cleanup EXIT
trap 'on_signal INT 130' INT
trap 'on_signal TERM 143' TERM

"${compose[@]}" config --quiet
echo "PASS: verification Compose config is valid"

resolve_port() {
	local service=$1 target=$2 mapping
	mapping="$("${compose[@]}" port "$service" "$target")"
	if [[ "$(printf '%s\n' "$mapping" | sed '/^$/d' | wc -l | tr -d ' ')" != "1" ]] || [[ ! "$mapping" =~ ^127[.]0[.]0[.]1:[0-9]+$ ]]; then
		echo "Unexpected $service/$target port mapping: $mapping" >&2
		return 1
	fi
	printf '%s\n' "${mapping##*:}"
}

run_owned_child() {
	local output=$1 monitor_was_set=0 observed_pgid shell_pgid status
	shift
	case $- in
		*m*) monitor_was_set=1 ;;
	esac
	set -m
	if [[ "$output" == "-" ]]; then
		"$@" &
	else
		"$@" >"$output" 2>&1 &
	fi
	active_child_pid=$! active_child_pgid=$!
	observed_pgid="$(ps -o pgid= -p "$active_child_pid" 2>/dev/null | tr -d '[:space:]')" || observed_pgid=
	if ((monitor_was_set == 0)); then
		set +m
	fi
	shell_pgid="$(ps -o pgid= -p "$$" 2>/dev/null | tr -d '[:space:]')" || shell_pgid=
	if [[ ! "$observed_pgid" =~ ^[0-9]+$ ]] || [[ "$observed_pgid" != "$active_child_pid" ]] || [[ "$observed_pgid" == "$shell_pgid" ]]; then
		if kill -0 "$active_child_pid" 2>/dev/null; then
			kill -TERM "$active_child_pid" 2>/dev/null || true
			wait "$active_child_pid" 2>/dev/null || true
			active_child_pid=
			active_child_pgid=
			echo "Could not isolate verification child in its own process group" >&2
			return 1
		fi
		active_child_pgid=
	fi
	wait "$active_child_pid" 2>/dev/null
	status=$?
	active_child_pid=
	active_child_pgid=
	return "$status"
}

mutated=1
run_owned_child - "${compose[@]}" up --detach --wait --wait-timeout 120 postgres

postgres_port="$(resolve_port postgres 5432)"
export TEST_DATABASE_URL="postgres://work:work@127.0.0.1:${postgres_port}/work_management?sslmode=disable"
echo "PASS: PostgreSQL test database is fresh and loopback-published"

if [[ "${VERIFY_COMPOSE_INJECT_FAILURE:-}" == "after-postgres" ]]; then
	echo "Injected verification failure after PostgreSQL startup" >&2
	exit 97
fi

run_test_suite() {
	local label=$1
	shift
	local log="$log_dir/$label.log" status skip_count pass_count package_count
	set +e
	run_owned_child "$log" "$@"
	status=$?
	set -e
	if ((status != 0)); then
		echo "$label failed with status $status" >&2
		tail -200 "$log" >&2
		return "$status"
	fi
	skip_count="$(jq -s '[.[] | select(.Action == "skip" and .Test != null)] | length' "$log")"
	if [[ "$skip_count" != "0" ]]; then
		echo "$label contained $skip_count forbidden skip markers" >&2
		jq -r 'select(.Action == "skip" and .Test != null) | "SKIP: \(.Package) \(.Test)"' "$log" >&2
		return 1
	fi
	if grep -Eq -- 'TEST_DATABASE_URL is not configured|^--- SKIP:' "$log"; then
		echo "$label contained a textual skip marker outside the JSON audit" >&2
		grep -E -- 'TEST_DATABASE_URL is not configured|^--- SKIP:' "$log" >&2
		return 1
	fi
	pass_count="$(jq -s '[.[] | select(.Action == "pass" and .Test != null and (.Test | contains("/") | not))] | length' "$log")"
	package_count="$(jq -s '[.[] | select(.Action == "pass" and .Test == null)] | length' "$log")"
	echo "PASS: $label completed with zero skips ($pass_count top-level tests; $package_count packages)"
}

run_test_suite go-test-normal env CGO_ENABLED=0 go test -json -count=1 ./...
run_test_suite go-test-race go test -json -race -count=1 ./...

if [[ "${VERIFY_COMPOSE_INJECT_FAILURE:-}" == "after-tests" ]]; then
	echo "Injected verification failure after database suites" >&2
	exit 98
fi

run_owned_child - "${compose[@]}" up --build --detach --wait --wait-timeout 180

assert_completed_service() {
	local service=$1 container state
	container="$("${compose[@]}" ps --all -q "$service")"
	[[ -n "$container" ]] || { echo "$service container is missing" >&2; return 1; }
	state="$(docker inspect --format '{{.State.Status}} {{.State.ExitCode}}' "$container")"
	[[ "$state" == "exited 0" ]] || { echo "$service state=$state" >&2; return 1; }
}
assert_completed_service migrate
assert_completed_service seed
echo "PASS: migrate and seed completed successfully"

smtp_port="$(resolve_port mailpit 1025)"
mailpit_port="$(resolve_port mailpit 8025)"
api_port="$(resolve_port api 8080)"
api_admin_port="$(resolve_port api 9090)"
worker_admin_port="$(resolve_port worker 9091)"
for port in "$postgres_port" "$smtp_port" "$mailpit_port" "$api_port" "$api_admin_port" "$worker_admin_port"; do
	[[ "$port" =~ ^[0-9]+$ ]] || { echo "Resolved nonnumeric port $port" >&2; exit 1; }
done
echo "PASS: all six published ports are ephemeral IPv4 loopback mappings"

curl_common=(--silent --show-error --connect-timeout 2 --max-time 10)
assert_admin_endpoint() {
	local base=$1 path=$2
	if [[ "$path" == "/metrics" ]]; then
		curl "${curl_common[@]}" --fail "$base$path" | grep -q '^work_management_'
	else
		curl "${curl_common[@]}" --fail "$base$path" | jq -e '.status == "ok" or .status == "ready"' >/dev/null
	fi
}
api_admin_url="http://127.0.0.1:$api_admin_port"
worker_admin_url="http://127.0.0.1:$worker_admin_port"
for path in /health/live /health/ready /metrics; do
	assert_admin_endpoint "$api_admin_url" "$path"
	assert_admin_endpoint "$worker_admin_url" "$path"
done
echo "PASS: API and worker admin live, ready, and metrics endpoints respond"

api_url="http://127.0.0.1:$api_port"
business_status="$(curl "${curl_common[@]}" --output "$log_dir/business.json" --write-out '%{http_code}' --header 'Authorization: Bearer dev-manager-token' "$api_url/v1/work-items?limit=1")"
[[ "$business_status" == "200" ]] && jq -e '.data | type == "array"' "$log_dir/business.json" >/dev/null
for path in /health/live /health/ready /metrics; do
	status="$(curl "${curl_common[@]}" --output "$log_dir/public-${path//\//-}.json" --dump-header "$log_dir/public-${path//\//-}.headers" --write-out '%{http_code}' "$api_url$path")"
	[[ "$status" == "404" ]]
	request_id="$(awk 'BEGIN{FS=":"} tolower($1)=="x-request-id" {sub(/^[^:]*:[[:space:]]*/, ""); sub(/\r$/, ""); print}' "$log_dir/public-${path//\//-}.headers")"
	jq -e --arg request_id "$request_id" '.error.code == "RESOURCE_NOT_FOUND" and .error.request_id == $request_id and ($request_id | length > 0)' "$log_dir/public-${path//\//-}.json" >/dev/null
done
echo "PASS: public API serves business routes and excludes operational routes"

run_owned_child - env API_URL="$api_url" API_ADMIN_URL="$api_admin_url" MAILPIT_URL="http://127.0.0.1:$mailpit_port" ./scripts/demo.sh
echo "PASS: live manager/worker/Mailpit/durable-state demo completed"
echo "PASS: authoritative Compose verification completed"
