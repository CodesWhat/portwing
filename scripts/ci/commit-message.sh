#!/usr/bin/env bash
set -euo pipefail

base_ref="${BASE_REF:-}"
if [ -z "${base_ref}" ] || ! git check-ref-format --branch "${base_ref}" >/dev/null 2>&1; then
	echo "invalid or missing BASE_REF" >&2
	exit 2
fi

bash scripts/validate-commit-range-test.sh
bash scripts/validate-commit-range.sh "origin/${base_ref}"
