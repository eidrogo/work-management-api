#!/usr/bin/env bash
set -euo pipefail

command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 1; }
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
checker="$script_dir/check-runtime-dockerfile.sh"
fixture="$(mktemp -d "${TMPDIR:-/tmp}/runtime-dockerfile-check.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT

write_valid() {
  sed '1{/^# syntax=/d;}' "$script_dir/../Dockerfile" >"$fixture/Dockerfile"
}
expect_rejected() {
  local description=$1
  if "$checker" "$fixture/Dockerfile" >/dev/null 2>&1; then
    echo "Runtime Dockerfile gate accepted $description" >&2
    exit 1
  fi
  write_valid
}

replace_literal() {
  local old=$1 new=$2
  OLD="$old" NEW="$new" python3 - "$fixture/Dockerfile" <<'PYTHON'
import os
import pathlib
import sys
path = pathlib.Path(sys.argv[1])
content = path.read_text()
old = os.environ["OLD"]
new = os.environ["NEW"]
if old not in content:
    raise SystemExit(f"missing fixture literal: {old!r}")
path.write_text(content.replace(old, new, 1))
PYTHON
}

write_valid
"$checker" "$fixture/Dockerfile" >/dev/null

replace_literal 'FROM scratch AS runtime' 'FROM debian:bookworm AS runtime'
expect_rejected 'a non-scratch runtime'
replace_literal 'USER 65532:65532' $'RUN apt-get update && apt-get install -y curl\nUSER 65532:65532'
expect_rejected 'a runtime package install'
replace_literal 'FROM toolchain AS build' $'FROM toolchain AS build\nRUN apt-get update && apt-get install -y curl'
expect_rejected 'a build-stage package install'
replace_literal 'FROM toolchain AS build' $'FROM toolchain AS build\nRUN set -e; \\\n    apt-get update'
expect_rejected 'a multiline build-stage package install'
replace_literal 'USER 65532:65532' $'COPY unexpected /unexpected\nUSER 65532:65532'
expect_rejected 'an unexpected runtime copy'
replace_literal 'USER 65532:65532' $'ADD https://example.invalid/tool /tool\nUSER 65532:65532'
expect_rejected 'a remote runtime ADD'
replace_literal 'FROM golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd AS toolchain' 'FROM golang:1.26.5-bookworm AS toolchain'
expect_rejected 'an unpinned toolchain image'
replace_literal 'FROM toolchain AS build' 'FROM attacker:latest AS build'
expect_rejected 'a mutable external build stage'
replace_literal 'FROM toolchain AS build' $'FROM attacker:latest AS unused\nFROM toolchain AS build'
expect_rejected 'an extra unused external stage'
replace_literal 'FROM toolchain AS build' $'FROM toolchain AS toolchain\nFROM toolchain AS build'
expect_rejected 'a duplicate toolchain alias'
replace_literal 'RUN go mod download && go mod verify' $'ADD https://example.invalid/archive.tar /src/\nRUN go mod download && go mod verify'
expect_rejected 'a build-stage remote ADD'
replace_literal 'RUN go mod download && go mod verify' $'COPY --from=attacker:latest /tool /tool\nRUN go mod download && go mod verify'
expect_rejected 'an external COPY source'
replace_literal 'RUN go mod download && go mod verify' $'RUN git clone https://example.invalid/repo\nRUN go mod download && go mod verify'
expect_rejected 'a VCS network fetch'
replace_literal 'RUN go mod download && go mod verify' $'RUN go install golang.org/x/tools/cmd/stringer@latest\nRUN go mod download && go mod verify'
expect_rejected 'a mutable go install input'
replace_literal 'RUN go mod download && go mod verify' $'RUN go env -w GOPROXY=https://example.invalid\nRUN go mod download && go mod verify'
expect_rejected 'a Go proxy mutation'
replace_literal 'USER 65532:65532' $'USER 65532:65532\nfrom debian:bookworm AS final'
expect_rejected 'a stage appended after scratch runtime'
replace_literal 'USER 65532:65532' 'USER app'
expect_rejected 'a named runtime user'
replace_literal 'COPY --from=toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt' 'COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt'
expect_rejected 'a CA bundle copied from the mutable build stage'
replace_literal 'for command in api worker migrate seed replay-notification healthcheck; do' 'for command in api worker migrate seed replay-notification; do'
expect_rejected 'an incomplete command set'

"$checker" "$fixture/Dockerfile" >/dev/null
echo 'Runtime Dockerfile policy mutation tests passed.'
