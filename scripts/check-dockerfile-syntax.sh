#!/usr/bin/env bash
set -euo pipefail

expected='# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e'
dockerfiles=()
while IFS= read -r -d '' tracked_file; do
  basename="${tracked_file##*/}"
  lowercase_basename="$(printf '%s' "$basename" | LC_ALL=C tr '[:upper:]' '[:lower:]')"
  case "$lowercase_basename" in
    dockerfile|dockerfile.*|*.dockerfile)
      dockerfiles+=("$tracked_file")
      ;;
  esac
done < <(git ls-files -z)
if [[ "${#dockerfiles[@]}" -eq 0 ]]; then
  echo 'No tracked Dockerfile candidates found.' >&2
  exit 1
fi
for dockerfile in "${dockerfiles[@]}"; do
  first_line="$(sed -n '1p' "$dockerfile")"
  syntax_count="$(LC_ALL=C grep -ciE '^[[:space:]]*#[[:space:]]*syntax[[:space:]]*=' "$dockerfile" || true)"
  if [[ "$syntax_count" != 1 || "$first_line" != "$expected" ]]; then
    echo "$dockerfile must use the single approved digest-pinned syntax frontend:" >&2
    echo "$expected" >&2
    exit 1
  fi
done

echo "Dockerfile syntax frontend is digest-pinned (${#dockerfiles[@]} tracked file(s))."
