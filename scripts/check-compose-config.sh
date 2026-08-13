#!/usr/bin/env bash
set -euo pipefail

rendered="$(docker compose --env-file .env.example config --format json)"
jq -e '
  ([.services[] | .ports[]?] | length) == 6 and
  ([.services[] | .ports[]?] | all(.host_ip == "127.0.0.1")) and
  ([.services.postgres.ports[].target] | sort) == [5432] and
  ([.services.mailpit.ports[].target] | sort) == [1025, 8025] and
  ([.services.api.ports[].target] | sort) == [8080, 9090] and
  ([.services.worker.ports[].target] | sort) == [9091] and
  .services.api.environment.API_ADMIN_ADDR == ":9090" and
  .services.worker.environment.WORKER_HTTP_ADDR == ":9091" and
  .services.api.healthcheck.test[-1] == "http://127.0.0.1:9090/health/ready" and
  .services.worker.healthcheck.test[-1] == "http://127.0.0.1:9091/health/ready"
' <<<"$rendered" >/dev/null
