#!/usr/bin/env bash
set -euo pipefail
if [[ -f .env ]]; then
  echo 'Refusing to verify with .env in repository root.'
  exit 1
fi
patterns=(
  'AKIA[0-9A-Z]{16}'
  'gh[pousr]_[A-Za-z0-9_]{20,}'
  '-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----'
  'sk-[A-Za-z0-9]{20,}'
)
for pattern in "${patterns[@]}"; do
  if grep -RInE --exclude-dir=.git --exclude-dir=bin --exclude='check-secrets.sh' -- "$pattern" .; then
    echo "Potential secret matched pattern: $pattern"
    exit 1
  fi
done
echo 'No high-confidence secret patterns found.'
