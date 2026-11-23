#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Contract test for .github/workflows/quality-bench-monthly.yml.
#
# The scheduled run failed on 2026-10-01 and 2026-11-01 while hand dispatches
# passed, because a run that failed the regression gate could never become a
# baseline and the threshold sat inside runner noise. These checks pin the
# parts of the workflow that keep that from coming back.

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
workflow="${repository_root}/.github/workflows/quality-bench-monthly.yml"

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

require() {
	local pattern="$1" message="$2"
	grep -Eq -e "${pattern}" "${workflow}" || fail "${message}"
}

forbid() {
	local pattern="$1" message="$2"
	if grep -Eq -e "${pattern}" "${workflow}"; then
		fail "${message}"
	fi
}

require "^  schedule:" "workflow lost its schedule trigger"
require "gh run list" "baseline candidates are no longer listed with gh run list"
require "--status completed" "baseline listing must include completed runs, not only successes"
forbid "--status success" "baseline listing must not be limited to --status success: a gated failure would pin the baseline"
require 'conclusion == "success" or \.conclusion == "failure"' "baseline listing must keep success and failure conclusions only"
require "bench-comparison\.md -print" "a failed run must prove the gate recorded a verdict before it can be a baseline"

threshold="$(sed -n "s/^  BENCH_REGRESSION_THRESHOLD_PERCENT: '\([0-9.]*\)'.*/\1/p" "${workflow}")"
if [ -z "${threshold}" ]; then
	fail "BENCH_REGRESSION_THRESHOLD_PERCENT not found"
elif ! awk -v t="${threshold}" 'BEGIN { exit !(t + 0 >= 15) }'; then
	fail "threshold ${threshold}% is inside the runner noise seen on unchanged code (10.6%); keep it at 15 or above"
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} check(s) failed" >&2
	exit 1
fi
echo "quality-bench-monthly contract: ok"
