#!/usr/bin/env bash
set -euo pipefail

# Shared retry/classify engine for a single `go test -fuzz` target. Owns the
# two crash-message phrases and the -fuzztime boundary-flake retry so
# lefthook.yml, scripts/ci/go-fuzz.sh, quality-fuzz-nightly.yml and
# quality-fuzz-monthly.yml each become a call into this script instead of
# reimplementing the loop four times. See PW-5.10.
#
# Usage: fuzz-run.sh <pkg> <target> <fuzztime>
#
# Env:
#   FUZZ_RETRIES      total attempts before giving up on a boundary flake
#                      (default 2, matching every current caller)
#   FUZZ_TIMEOUT      passed as `go test -timeout`; the flag is omitted when
#                      this is unset, so `go test`'s own default applies
#   FUZZ_PARALLEL     passed as `go test -parallel`; omitted when unset
#   FUZZ_OUTPUT_FILE  appended with `kind=`, `reason=` (crash only) and
#                      `status=` lines — point this at $GITHUB_OUTPUT to wire
#                      the classification straight into a workflow step's
#                      outputs
#   FUZZ_LOG_FILE     appended with every attempt's raw `go test` output
#   FUZZ_ATTEMPT_HOOK command invoked with the attempt number as $1 after
#                      each failed attempt, before it is classified — used by
#                      go-fuzz.sh to snapshot the corpus per attempt
#
# Exit codes: 0 pass, 1 crash, 2 error, 3 flake after retries exhausted,
# 4 killed by SIGTERM (a lost runner or a job timeout, not a code failure).

pkg="${1:?usage: fuzz-run.sh <pkg> <target> <fuzztime>}"
target="${2:?usage: fuzz-run.sh <pkg> <target> <fuzztime>}"
fuzztime="${3:?usage: fuzz-run.sh <pkg> <target> <fuzztime>}"

retries="${FUZZ_RETRIES:-2}"

annotate_prefix=""
warn_prefix=""
if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
	annotate_prefix="::error::"
	warn_prefix="::warning::"
fi

emit() {
	if [ -n "${FUZZ_OUTPUT_FILE:-}" ]; then
		echo "$1" >>"${FUZZ_OUTPUT_FILE}"
	fi
}

attempt_log="$(mktemp)"
trap 'rm -f "${attempt_log}"' EXIT

go_test_args=(-run='^$' -fuzz="^${target}\$" -fuzztime="${fuzztime}")
if [ -n "${FUZZ_TIMEOUT:-}" ]; then
	go_test_args+=(-timeout="${FUZZ_TIMEOUT}")
fi
if [ -n "${FUZZ_PARALLEL:-}" ]; then
	go_test_args+=(-parallel="${FUZZ_PARALLEL}")
fi
go_test_args+=("${pkg}")

attempt=1
while [ "${attempt}" -le "${retries}" ]; do
	rc=0
	go test "${go_test_args[@]}" 2>&1 | tee "${attempt_log}" || rc="${PIPESTATUS[0]}"

	if [ -n "${FUZZ_LOG_FILE:-}" ]; then
		cat "${attempt_log}" >>"${FUZZ_LOG_FILE}"
	fi

	if [ "${rc}" -eq 0 ]; then
		emit "kind=pass"
		emit "status=0"
		exit 0
	fi

	if [ -n "${FUZZ_ATTEMPT_HOOK:-}" ]; then
		"${FUZZ_ATTEMPT_HOOK}" "${attempt}"
	fi

	if grep -q "Failing input written to testdata" "${attempt_log}"; then
		emit "kind=crash"
		emit "reason=first-discovery"
		emit "status=${rc}"
		echo "${annotate_prefix}${target} found a crashing input (matched 'Failing input written to testdata') — commit it to the seed corpus and fix the regression." >&2
		exit 1
	fi

	# The committed seed corpus already holds a crasher from a prior run, so
	# Go re-tests it before generating anything new and never reaches the
	# "Failing input written" path above. Same finding, different message.
	if grep -q "failure while testing seed corpus entry" "${attempt_log}"; then
		emit "kind=crash"
		emit "reason=seed-regression"
		emit "status=${rc}"
		echo "${annotate_prefix}${target}'s committed seed corpus is failing (matched 'failure while testing seed corpus entry') — a previously-fixed crash has regressed." >&2
		exit 1
	fi

	# Killed by a signal, not a test failure. Checked after the crash phrases
	# so a real finding can never be reclassified, and before the flake/error
	# check below so a SIGTERM'd `go test` stops being labelled a code
	# regression. 137 is deliberately not matched here: it is ambiguous
	# between a teardown escalating to SIGKILL and the kernel OOM-killing a
	# fuzz target that ate the runner's memory, and the second one is a real
	# finding.
	if [ "${rc}" -eq 143 ]; then
		emit "kind=infra"
		emit "status=${rc}"
		echo "${annotate_prefix}${target}: killed by SIGTERM (exit 143), not a code failure." >&2
		exit 4
	fi

	if ! grep -q "context deadline exceeded" "${attempt_log}"; then
		emit "kind=error"
		emit "status=${rc}"
		echo "${annotate_prefix}${target} failed for a non-flake reason (exit ${rc})." >&2
		exit 2
	fi

	if [ "${attempt}" -lt "${retries}" ]; then
		echo "${warn_prefix}${target}: known -fuzztime boundary flake on attempt ${attempt}/${retries}." >&2
	fi
	attempt=$((attempt + 1))
done

emit "kind=flake"
emit "status=${rc}"
echo "${annotate_prefix}${target} hit the boundary flake on all ${retries} attempts." >&2
exit 3
