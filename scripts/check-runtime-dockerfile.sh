#!/usr/bin/env bash
set -euo pipefail

command -v python3 >/dev/null || { echo 'python3 is required' >&2; exit 1; }
dockerfile="${1:-Dockerfile}"
[[ -f "$dockerfile" ]] || { echo "$dockerfile is missing" >&2; exit 1; }

python3 - "$dockerfile" <<'PYTHON'
from pathlib import Path
import sys

path = Path(sys.argv[1])
logical = []
buffer = ""
for raw in path.read_text().splitlines():
    stripped = raw.strip()
    if not stripped or stripped.startswith("#"):
        continue
    if buffer:
        buffer += " " + stripped
    else:
        buffer = stripped
    if buffer.endswith("\\"):
        buffer = buffer[:-1].rstrip()
        continue
    logical.append(" ".join(buffer.split()))
    buffer = ""
if buffer:
    raise SystemExit(f"{path}: dangling Dockerfile continuation")

expected = [
    "FROM golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd AS toolchain",
    "FROM toolchain AS build",
    "WORKDIR /src",
    "COPY go.mod go.sum ./",
    "RUN go mod download && go mod verify",
    "COPY . .",
    'RUN set -e; CGO_ENABLED=0 go test ./... && mkdir -p /out && for command in api worker migrate seed replay-notification healthcheck; do CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "/out/${command}" "./cmd/${command}"; done',
    "FROM scratch AS runtime",
    "ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt",
    "WORKDIR /app",
    "COPY --from=toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt",
    "COPY --from=build --chown=65532:65532 /out/ /app/",
    "COPY --chown=65532:65532 migrations/ /app/migrations/",
    "USER 65532:65532",
    "EXPOSE 8080 9090 9091",
    'ENTRYPOINT ["/app/api"]',
]
if logical != expected:
    print(f"{path} must match the fail-closed package-manager-free three-stage runtime policy", file=sys.stderr)
    limit = max(len(logical), len(expected))
    for index in range(limit):
        actual = logical[index] if index < len(logical) else "<missing>"
        wanted = expected[index] if index < len(expected) else "<none>"
        if actual != wanted:
            print(f"instruction {index + 1}: expected {wanted!r}; got {actual!r}", file=sys.stderr)
    raise SystemExit(1)
PYTHON

echo 'Dockerfile scratch runtime policy passed.'
