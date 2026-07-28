#!/usr/bin/env bash
set -euo pipefail

readonly forbidden_package='golang.org/x/crypto/openpgp'

if [[ "$#" -eq 0 ]]; then
  if go list -deps -test ./... | grep -Fxq "${forbidden_package}"; then
    echo "error: ${forbidden_package} is present in the Portwing dependency graph" >&2
    exit 1
  fi
  echo "verified: ${forbidden_package} is absent from the Portwing dependency graph"
  exit 0
fi

for binary_path in "$@"; do
  if [[ ! -r "${binary_path}" ]]; then
    echo "error: cannot inspect unreadable binary: ${binary_path}" >&2
    exit 1
  fi
  if LC_ALL=C grep -aFq "${forbidden_package}" "${binary_path}"; then
    echo "error: ${forbidden_package} is linked into ${binary_path}" >&2
    exit 1
  fi
done

echo "verified: ${forbidden_package} is absent from ${#} shipped binaries"
