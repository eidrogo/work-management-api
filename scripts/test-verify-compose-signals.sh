#!/usr/bin/env bash
set -Eeuo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
signal_name="${VERIFY_SIGNAL_TEST_SIGNAL:-TERM}"
child_exit="${VERIFY_SIGNAL_TEST_CHILD_EXIT:-}"
case "$signal_name" in
	INT) expected_status=130; expected_event=child-int ;;
	TERM) expected_status=143; expected_event=child-term ;;
	*) echo "VERIFY_SIGNAL_TEST_SIGNAL must be INT or TERM" >&2; exit 2 ;;
esac
if [[ -n "$child_exit" ]] && [[ ! "$child_exit" =~ ^[1-9][0-9]*$ ]]; then
	echo "VERIFY_SIGNAL_TEST_CHILD_EXIT must be a positive integer" >&2
	exit 2
fi
sandbox="$(mktemp -d)"
mock_bin="$sandbox/bin"
state="$sandbox/state"
mkdir -p "$mock_bin" "$state/logs"
event_log="$state/events.log"
output_log="$state/verifier.log"
fifo="$state/child.fifo"
mkfifo "$fifo"
verifier_pid=
child_pid=

finish() {
	set +e
	if [[ -n "$verifier_pid" ]] && kill -0 "$verifier_pid" 2>/dev/null; then
		kill -KILL "$verifier_pid" 2>/dev/null
		wait "$verifier_pid" 2>/dev/null
	fi
	if [[ -f "$state/child.pid" ]]; then
		child_pid="$(cat "$state/child.pid")"
	fi
	if [[ -n "$child_pid" ]] && kill -0 "$child_pid" 2>/dev/null; then
		kill -KILL "$child_pid" 2>/dev/null
	fi
	rm -rf "$sandbox"
}
trap finish EXIT INT TERM

cat >"$mock_bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -Eeuo pipefail
: "${VERIFY_SIGNAL_TEST_EVENTS:?}"
joined=" $* "
case "$joined" in
	*" compose version "*) echo "Docker Compose mock" ;;
	*" info "*) ;;
	*" ps -aq "*|*" network ls -q "*|*" volume ls -q "*) ;;
	*" config --quiet "*) ;;
	*" up --detach --wait --wait-timeout 120 postgres "*) printf 'postgres-up\n' >>"$VERIFY_SIGNAL_TEST_EVENTS" ;;
	*" port postgres 5432 "*) echo "127.0.0.1:55432" ;;
	*" ps --all "*|*" logs --no-color "*) ;;
	*" down --volumes --remove-orphans --timeout 10 "*) printf 'down\n' >>"$VERIFY_SIGNAL_TEST_EVENTS" ;;
	*) echo "unexpected mock docker invocation:$joined" >&2; exit 90 ;;
esac
MOCK

cat >"$mock_bin/go" <<'MOCK'
#!/usr/bin/env bash
set -Eeuo pipefail
: "${VERIFY_SIGNAL_TEST_EVENTS:?}"
: "${VERIFY_SIGNAL_TEST_FIFO:?}"
: "${VERIFY_SIGNAL_TEST_STATE:?}"
printf '%s\n' "$$" >"$VERIFY_SIGNAL_TEST_STATE/child.pid"
printf 'child-start\n' >>"$VERIFY_SIGNAL_TEST_EVENTS"
trap 'printf "child-int\n" >>"$VERIFY_SIGNAL_TEST_EVENTS"; exit 130' INT
trap 'printf "child-term\n" >>"$VERIFY_SIGNAL_TEST_EVENTS"; exit 143' TERM
IFS= read -r _ <"$VERIFY_SIGNAL_TEST_FIFO"
if [[ -n "${VERIFY_SIGNAL_TEST_CHILD_EXIT:-}" ]]; then
	printf 'child-exit-%s\n' "$VERIFY_SIGNAL_TEST_CHILD_EXIT" >>"$VERIFY_SIGNAL_TEST_EVENTS"
	exit "$VERIFY_SIGNAL_TEST_CHILD_EXIT"
fi
MOCK
chmod +x "$mock_bin/docker" "$mock_bin/go"

set -m
PATH="$mock_bin:$PATH" \
VERIFY_SIGNAL_TEST_EVENTS="$event_log" \
VERIFY_SIGNAL_TEST_FIFO="$fifo" \
VERIFY_SIGNAL_TEST_STATE="$state" \
VERIFY_SIGNAL_TEST_CHILD_EXIT="$child_exit" \
VERIFY_LOG_DIR="$state/logs" \
VERIFY_COMPOSE_PROJECT="wma-signal-regression-$$" \
"$repository_root/scripts/verify-compose.sh" >"$output_log" 2>&1 &
verifier_pid=$!
set +m

ready=0
for _ in $(seq 1 200); do
	if grep -q '^PASS: PostgreSQL test database is fresh and loopback-published$' "$output_log" 2>/dev/null && [[ -f "$state/child.pid" ]]; then
		ready=1
		break
	fi
	if ! kill -0 "$verifier_pid" 2>/dev/null; then
		break
	fi
	sleep 0.01
done
if ((ready == 0)); then
	cat "$output_log" >&2
	echo "verifier did not reach the owned test child" >&2
	exit 1
fi

child_pid="$(cat "$state/child.pid")"
if [[ -n "$child_exit" ]]; then
	printf 'release\n' >"$fifo"
	set +e
	wait "$verifier_pid" 2>/dev/null
	status=$?
	set -e
	verifier_pid=
	if [[ "$status" != "$child_exit" ]]; then
		cat "$output_log" >&2
		echo "verifier exit=$status want child status $child_exit" >&2
		exit 1
	fi
	if kill -0 "$child_pid" 2>/dev/null; then
		echo "owned test child $child_pid was not reaped" >&2
		exit 1
	fi
	if ! grep -q "^child-exit-${child_exit}$" "$event_log" || ! grep -q '^down$' "$event_log"; then
		cat "$event_log" >&2
		echo "child completion or Compose cleanup was not observed" >&2
		exit 1
	fi
	if ! grep -q '^PASS: Compose cleanup removed all project containers, networks, and volumes$' "$output_log"; then
		cat "$output_log" >&2
		echo "cleanup resource audit did not pass" >&2
		exit 1
	fi
	echo "PASS: owned test child status $child_exit propagated unchanged and cleanup reaped the child"
	exit 0
fi
kill -"$signal_name" "$verifier_pid"

finished=0
polls=0
for _ in $(seq 1 100); do
	polls=$((polls + 1))
	if ! kill -0 "$verifier_pid" 2>/dev/null; then
		finished=1
		break
	fi
	sleep 0.01
done
if ((finished == 0)); then
	cat "$output_log" >&2
	printf 'events:\n' >&2
	cat "$event_log" >&2
	echo "verifier did not terminate and begin cleanup within one second" >&2
	exit 1
fi

set +e
wait "$verifier_pid" 2>/dev/null
status=$?
set -e
verifier_pid=
if [[ "$status" != "$expected_status" ]]; then
	cat "$output_log" >&2
	echo "verifier exit=$status want=$expected_status" >&2
	exit 1
fi
if ! grep -q "^${expected_event}$" "$event_log"; then
	cat "$event_log" >&2
	echo "owned test child did not receive $signal_name" >&2
	exit 1
fi
if ! grep -q '^down$' "$event_log"; then
	cat "$event_log" >&2
	echo "Compose cleanup did not start promptly" >&2
	exit 1
fi
if kill -0 "$child_pid" 2>/dev/null; then
	echo "owned test child $child_pid was not reaped" >&2
	exit 1
fi
term_line="$(grep -n "^${expected_event}$" "$event_log" | cut -d: -f1)"
down_line="$(grep -n '^down$' "$event_log" | cut -d: -f1)"
if ((term_line >= down_line)); then
	cat "$event_log" >&2
	echo "cleanup began before the owned child was terminated and reaped" >&2
	exit 1
fi
if ! grep -q '^PASS: Compose cleanup removed all project containers, networks, and volumes$' "$output_log"; then
	cat "$output_log" >&2
	echo "cleanup resource audit did not pass" >&2
	exit 1
fi
echo "PASS: SIG${signal_name} reached the owned test process group, reaped it, and began Compose cleanup promptly ($polls polls)"
