#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
checker="$script_dir/check-dockerfile-syntax.sh"
expected='# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e'
fixture="$(mktemp -d "${TMPDIR:-/tmp}/dockerfile-syntax-check.XXXXXX")"
lowercase_fixture=''
trap 'rm -rf "$fixture"; if [[ -n "$lowercase_fixture" ]]; then rm -rf "$lowercase_fixture"; fi' EXIT

git -C "$fixture" init -q
mkdir -p "$fixture/api" "$fixture/worker"

write_valid() {
  printf '%s\nFROM scratch\n' "$expected" >"$1"
}

write_valid "$fixture/Dockerfile"
write_valid "$fixture/api/Dockerfile.production"
write_valid "$fixture/worker/service.Dockerfile"
newline_candidate="$fixture/worker/line
break.Dockerfile"
write_valid "$newline_candidate"
printf '# syntax=docker/dockerfile:latest\n' >"$fixture/NotDockerfile"
git -C "$fixture" add .

(cd "$fixture" && "$checker") >/dev/null

expect_rejected() {
  local description="$1"
  if (cd "$fixture" && "$checker") >/dev/null 2>&1; then
    echo "Dockerfile syntax gate accepted $description." >&2
    exit 1
  fi
}

printf '# syntax=docker/dockerfile:1.7\nFROM scratch\n' >"$fixture/Dockerfile"
expect_rejected 'a mutable root frontend'
write_valid "$fixture/Dockerfile"

printf '# syntax=docker/dockerfile:1.7\nFROM scratch\n' >"$fixture/api/Dockerfile.production"
expect_rejected 'a mutable Dockerfile.* frontend'
write_valid "$fixture/api/Dockerfile.production"

printf '# syntax=docker/dockerfile:1.7\nFROM scratch\n' >"$fixture/worker/service.Dockerfile"
expect_rejected 'a mutable *.Dockerfile frontend'
write_valid "$fixture/worker/service.Dockerfile"

printf 'FROM scratch\n  # SyNtAx = docker/dockerfile:latest\n' >"$fixture/Dockerfile"
expect_rejected 'a sole misplaced mixed-case syntax directive'
write_valid "$fixture/Dockerfile"

printf '  # SyNtAx = docker/dockerfile:latest\n' >>"$fixture/Dockerfile"
expect_rejected 'a duplicate mixed-case syntax directive'
write_valid "$fixture/Dockerfile"

lowercase_fixture="$(mktemp -d "${TMPDIR:-/tmp}/dockerfile-syntax-lowercase.XXXXXX")"
git -C "$lowercase_fixture" init -q
mkdir -p "$lowercase_fixture/service"
write_valid "$lowercase_fixture/Dockerfile"
printf '# syntax=docker/dockerfile:latest\nFROM scratch\n' >"$lowercase_fixture/service/service.dockerfile"
git -C "$lowercase_fixture" add .
if (cd "$lowercase_fixture" && "$checker") >/dev/null 2>&1; then
  echo 'Dockerfile syntax gate accepted a mutable lowercase *.dockerfile frontend.' >&2
  rm -rf "$lowercase_fixture"
  exit 1
fi
rm -rf "$lowercase_fixture"

(cd "$fixture" && "$checker") >/dev/null
echo 'Dockerfile syntax gate mutation tests passed.'
