#!/usr/bin/env bash
set -euo pipefail
PROJECT_NAME="${VERIFY_COMPOSE_PROJECT:-work-management-api-verification}"
export API_PORT="${VERIFY_API_PORT:-18081}"
export POSTGRES_PORT="${VERIFY_POSTGRES_PORT:-15432}"
export SMTP_PORT="${VERIFY_SMTP_PORT:-11025}"
export MAILPIT_UI_PORT="${VERIFY_MAILPIT_UI_PORT:-18025}"
export WORKER_PORT="${VERIFY_WORKER_PORT:-19091}"
compose=(docker compose --project-name "$PROJECT_NAME" --env-file .env.example)
cleanup(){ status=$?; trap - EXIT; if [[ $status -ne 0 ]]; then "${compose[@]}" ps || true; "${compose[@]}" logs --no-color || true; fi; "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true; exit $status; }
trap cleanup EXIT
"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
"${compose[@]}" up --build --detach --wait --wait-timeout 180
curl --fail --silent "http://localhost:${API_PORT}/health/ready" >/dev/null
curl --fail --silent "http://localhost:${WORKER_PORT}/health/ready" >/dev/null
API_URL="http://localhost:${API_PORT}" MAILPIT_URL="http://localhost:${MAILPIT_UI_PORT}" ./scripts/demo.sh
