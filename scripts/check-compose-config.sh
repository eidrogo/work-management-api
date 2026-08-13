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
  .services.api.image == (.name + "-api") and
  .services.api.pull_policy == "build" and
  .services.worker.environment.WORKER_HTTP_ADDR == ":9091" and
  .services.api.healthcheck.test == ["CMD", "/app/healthcheck", "127.0.0.1:9090"] and
  .services.worker.healthcheck.test == ["CMD", "/app/healthcheck", "127.0.0.1:9091"] and
  .services.api.healthcheck.timeout == "3s" and
  .services.worker.healthcheck.timeout == "3s"
' <<<"$rendered" >/dev/null
