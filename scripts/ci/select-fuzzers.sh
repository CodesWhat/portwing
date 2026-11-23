#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
# Print the fuzzers-json value for a pull request: only the fuzzers whose
# package, or a package it imports, holds a file the PR changed.
#
#   select-fuzzers.sh <base-ref> <fuzzer-list.json>
#
# Run from the repository root. Every uncertain path fails OPEN and prints the
# full list: an unreadable base ref, a failed diff, an empty diff, an import
# graph that can't be built, or a change to something that affects every
# fuzzer (the Go module files, the CI caller, the list itself, or anything
# under scripts/ci). Output is one compact JSON line.

base_ref="${1:?usage: select-fuzzers.sh <base-ref> <fuzzer-list.json>}"
list_path="${2:?usage: select-fuzzers.sh <base-ref> <fuzzer-list.json>}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

full_list() {
  jq -c . "${list_path}"
}

fail_open() {
  echo "::warning::select-fuzzers: $1, running every fuzzer" >&2
  full_list
  exit 0
}

# Validate the list up front; without it there is nothing to select from.
jq -e 'type == "array"' "${list_path}" >/dev/null || {
  echo "::error::select-fuzzers: ${list_path} is not a readable JSON array" >&2
  exit 1
}

changed=""
if ! changed="$(git diff --no-renames --name-only "${base_ref}...HEAD" 2>/dev/null)"; then
  fail_open "git diff against ${base_ref} failed"
fi
[ -n "${changed}" ] || fail_open "empty diff against ${base_ref}"

while IFS= read -r file; do
  case "${file}" in
    go.mod | go.sum | .github/workflows/ci-verify.yml | .github/ci-fuzzers.json | scripts/ci/*)
      echo "select-fuzzers: ${file} affects every fuzzer" >&2
      full_list
      exit 0
      ;;
  esac
done <<<"${changed}"

if ! selected="$(printf '%s\n' "${changed}" | python3 -I "${here}/select-fuzzers.py" "${list_path}" "$(pwd)")"; then
  fail_open "could not build the import graph"
fi
echo "${selected}"
