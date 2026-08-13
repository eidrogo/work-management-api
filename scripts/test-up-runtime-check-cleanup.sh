#!/usr/bin/env bash
set -Eeuo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$repository_root"

for tool in docker make mktemp grep sed sort diff chmod rm cat; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

real_docker="$(command -v docker)"
project="wma-up-check-$RANDOM-$$"
label="com.docker.compose.project=$project"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/up-runtime-check.XXXXXX")"
export POSTGRES_PORT=0 SMTP_PORT=0 MAILPIT_UI_PORT=0 API_PORT=0 API_ADMIN_PORT=0 WORKER_PORT=0

project_resources() {
  {
    "$real_docker" ps -aq --filter "label=$label"
    "$real_docker" network ls -q --filter "label=$label"
    "$real_docker" volume ls -q --filter "label=$label"
  } | sed '/^[[:space:]]*$/d'
}

cleanup() {
  local status=$?
  trap - EXIT
  COMPOSE_PROJECT_NAME="$project" "$real_docker" compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  for service in api worker migrate seed; do
    "$real_docker" image rm "${project}-${service}" >/dev/null 2>&1 || true
  done
  rm -r "$temporary"
  exit "$status"
}
trap cleanup EXIT

[[ -z "$(project_resources)" ]] || { echo "refusing nonempty test project $project" >&2; exit 1; }

cat >"$temporary/docker" <<SCRIPT
#!/usr/bin/env bash
if [[ "\${1:-}" == image && "\${2:-}" == inspect && "\${3:-}" != --format ]]; then
  echo 'injected runtime image inspection failure' >&2
  exit 86
fi
exec "$real_docker" "\$@"
SCRIPT
chmod +x "$temporary/docker"

set +e
PATH="$temporary:$PATH" COMPOSE_PROJECT_NAME="$project" make up >"$temporary/output" 2>&1
status=$?
set -e
if ((status == 0)) || ! grep -q 'injected runtime image inspection failure' "$temporary/output"; then
  cat "$temporary/output" >&2
  echo "make up did not propagate the injected runtime inspection failure (status=$status)" >&2
  exit 1
fi
if leftovers="$(project_resources)" && [[ -n "$leftovers" ]]; then
  cat "$temporary/output" >&2
  echo "make up left project resources after the injected pre-start failure:" >&2
  printf '%s\n' "$leftovers" >&2
  exit 1
fi

COMPOSE_PROJECT_NAME="$project" "$real_docker" compose up --no-build --no-deps --detach postgres >/dev/null
old_postgres="$(COMPOSE_PROJECT_NAME="$project" "$real_docker" compose ps -q postgres)"
old_state="$($real_docker inspect --format '{{.State.Status}}' "$old_postgres")"
[[ "$old_state" == running ]] || { echo "existing control container state=$old_state" >&2; exit 1; }
existing_resources="$(project_resources | LC_ALL=C sort)"

set +e
PATH="$temporary:$PATH" COMPOSE_PROJECT_NAME="$project" make up >"$temporary/existing-output" 2>&1
existing_status=$?
set -e
if ((existing_status == 0)) || ! grep -q 'injected runtime image inspection failure' "$temporary/existing-output"; then
  cat "$temporary/existing-output" >&2
  echo "make up did not propagate the existing-project inspection failure (status=$existing_status)" >&2
  exit 1
fi
new_postgres="$(COMPOSE_PROJECT_NAME="$project" "$real_docker" compose ps -q postgres)"
new_state="$($real_docker inspect --format '{{.State.Status}}' "$new_postgres")"
new_resources="$(project_resources | LC_ALL=C sort)"
if [[ "$new_postgres" != "$old_postgres" || "$new_state" != running || "$new_resources" != "$existing_resources" ]]; then
  cat "$temporary/existing-output" >&2
  echo "make up mutated the existing project before runtime inspection: old=$old_postgres new=$new_postgres state=$new_state" >&2
  diff -u <(printf '%s\n' "$existing_resources") <(printf '%s\n' "$new_resources") >&2 || true
  exit 1
fi

echo 'make up runtime-inspection failures preserve fresh and existing Compose state.'
