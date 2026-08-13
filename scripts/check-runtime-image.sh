#!/usr/bin/env bash
set -euo pipefail

image="${1:-work-management-api:local}"
for tool in docker jq tar file diff cmp; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

inspect="$(docker image inspect "$image")"
jq -e '
  length == 1 and
  .[0].Config.User == "65532:65532" and
  .[0].Config.Entrypoint == ["/app/api"] and
  .[0].Config.WorkingDir == "/app" and
  (.[0].Config.Env // []) as $environment |
  ($environment | length) == 2 and
  ([$environment[] | select(. == "SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt")] | length) == 1 and
  ([$environment[] | select(startswith("PATH=") and ((split("=")[1]) | test("^/[^:]+(:/[^:]+)*$")))] | length) == 1
' <<<"$inspect" >/dev/null

temporary="$(mktemp -d "${TMPDIR:-/tmp}/runtime-image-check.XXXXXX")"
container=""
trap 'if [[ -n "$container" ]]; then docker rm -f "$container" >/dev/null 2>&1 || true; fi; rm -rf "$temporary"' EXIT
docker image save "$image" -o "$temporary/image.tar"
tar -xf "$temporary/image.tar" -C "$temporary"
jq -r '.[0].Layers[]' "$temporary/manifest.json" >"$temporary/layers"
: >"$temporary/actual"
while IFS= read -r layer; do
  tar -tf "$temporary/$layer" | sed 's#^\./##; s#/$##' | sed '/^$/d' >>"$temporary/actual"
done <"$temporary/layers"
LC_ALL=C sort -u -o "$temporary/actual" "$temporary/actual"

cat >"$temporary/expected" <<'PATHS'
app
app/api
app/healthcheck
app/migrate
app/migrations
app/replay-notification
app/seed
app/worker
etc
etc/ssl
etc/ssl/certs
etc/ssl/certs/ca-certificates.crt
PATHS
migration_count=0
while IFS= read -r -d '' migration; do
  case "$migration" in
    *$'\n'*) echo "tracked migration path contains a newline: $migration" >&2; exit 1 ;;
  esac
  printf 'app/%s\n' "$migration" >>"$temporary/expected"
  migration_count=$((migration_count + 1))
done < <(git ls-files -z -- 'migrations/*.sql')
((migration_count > 0)) || { echo 'no tracked SQL migrations found' >&2; exit 1; }
LC_ALL=C sort -u -o "$temporary/expected" "$temporary/expected"
if ! diff -u "$temporary/expected" "$temporary/actual"; then
  echo 'runtime image layers differ from the exact binaries, tracked migrations, and CA allowlist' >&2
  exit 1
fi
for forbidden in bin/sh usr/bin/curl var/lib/dpkg lib/apk/db usr/bin/apt-get; do
  if grep -Eq "^${forbidden}(/|$)" "$temporary/actual"; then
    echo "forbidden runtime path: $forbidden" >&2
    exit 1
  fi
done

container="$(docker create --entrypoint /app/healthcheck "$image" 127.0.0.1:1)"
for command in api healthcheck migrate replay-notification seed worker; do
  docker cp "$container:/app/$command" "$temporary/$command"
  LC_ALL=C file "$temporary/$command" | LC_ALL=C grep -q 'statically linked' || { echo "/app/$command is not static" >&2; file "$temporary/$command" >&2; exit 1; }
done
while IFS= read -r -d '' migration; do
  mkdir -p "$temporary/copied/$(dirname "$migration")"
  docker cp "$container:/app/$migration" "$temporary/copied/$migration"
  cmp -- "$migration" "$temporary/copied/$migration" || { echo "runtime migration differs from source: $migration" >&2; exit 1; }
done < <(git ls-files -z -- 'migrations/*.sql')
docker cp "$container:/etc/ssl/certs/ca-certificates.crt" "$temporary/ca-certificates.crt"
[[ -s "$temporary/ca-certificates.crt" ]] || { echo 'CA bundle is empty' >&2; exit 1; }

set +e
health_output="$(docker run --rm --entrypoint /app/healthcheck "$image" 127.0.0.1:1 2>&1)"
health_status=$?
set -e
[[ "$health_status" == 1 && "$health_output" == readiness\ probe\ failed:* ]] || { echo "healthcheck execution status=$health_status output=$health_output" >&2; exit 1; }

echo 'Scratch runtime image inspection passed.'
