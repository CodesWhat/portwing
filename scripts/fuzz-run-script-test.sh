#!/usr/bin/env bash
set -euo pipefail

# Exercises scripts/ci/fuzz-run.sh (PW-5.10) directly, against a fake `go` on
# PATH, so the retry/classify contract shared by lefthook.yml,
# scripts/ci/go-fuzz.sh, quality-fuzz-nightly.yml and quality-fuzz-monthly.yml
# is pinned once instead of only indirectly through each caller.

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
fuzz_run="${repository_root}/scripts/ci/fuzz-run.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

failures=0
fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

bin="${fixture}/bin"
mkdir -p "${bin}"

# One fake `go` covers every scenario below, selected by $MOCK_MODE, so a
# single canned-output shim exercises pass, a boundary flake that then
# passes, a committed-seed regression, a fresh crasher, a non-flake (compile)
# error, a flake exhausted on every attempt, and a SIGTERM'd run. Each
# invocation counts its own attempt in $MOCK_STATE, which the caller resets
# per scenario.
cat >"${bin}/go" <<'MOCK'
#!/usr/bin/env bash
set -u
if [ "${1:-}" != "test" ]; then
	echo "unexpected go command: $*" >&2
	exit 90
fi
count=0
if [ -f "${MOCK_STATE:-}" ]; then count="$(cat "${MOCK_STATE}")"; fi
count=$((count + 1))
printf '%s' "${count}" >"${MOCK_STATE}"
case "${MOCK_MODE:-pass}" in
pass)
	echo "ok"
	exit 0
	;;
flake-then-pass)
	if [ "${count}" -eq 1 ]; then
		echo "panic: test timed out after 5s: context deadline exceeded"
		exit 1
	fi
	echo "ok"
	exit 0
	;;
seed-regression)
	echo "--- FAIL: FuzzTarget/seed#01 (0.00s)"
	echo "failure while testing seed corpus entry"
	exit 1
	;;
crash)
	echo "Failing input written to testdata/fuzz/FuzzTarget/deadbeef"
	exit 1
	;;
compile-error)
	echo "# example.invalid/internal/fixture"
	echo "internal/fixture/fixture.go:1:1: syntax error: unexpected EOF"
	exit 2
	;;
flake-exhausted)
	echo "panic: test timed out after 5s: context deadline exceeded"
	exit 1
	;;
infra)
	exit 143
	;;
*)
	echo "unknown MOCK_MODE: ${MOCK_MODE:-}" >&2
	exit 91
	;;
esac
MOCK
chmod +x "${bin}/go"

run_fuzz() {
	local mode="$1"
	shift
	local state="${fixture}/state"
	local output="${fixture}/output"
	rm -f "${state}" "${output}"
	local status=0
	# Extra VAR=value overrides arrive as ordinary arguments (from "$@"), so
	# bash's own assignment-prefix parsing — which only recognizes literal,
	# unexpanded NAME=value tokens — does not apply to them. `env` parses its
	# argv for "=" at runtime instead, which does.
	env MOCK_MODE="${mode}" MOCK_STATE="${state}" \
		PATH="${bin}:${PATH}" \
		FUZZ_OUTPUT_FILE="${output}" \
		"$@" \
		bash "${fuzz_run}" "./internal/fixture/" "FuzzTarget" "1s" >"${fixture}/stdout" 2>"${fixture}/stderr" || status=$?
	echo "${status}"
}

output_field() {
	awk -F= -v key="$1" '$1 == key { v = substr($0, length(key) + 2) } END { print v }' "${fixture}/output" 2>/dev/null
	true
}

expect() {
	local description="$1" expected_status="$2" expected_kind="$3" expected_reason="${4:-}"
	local actual_status="${5}"
	[ "${actual_status}" = "${expected_status}" ] ||
		fail "${description}: expected exit ${expected_status}, got ${actual_status}"
	local actual_kind
	actual_kind="$(output_field kind)"
	[ "${actual_kind}" = "${expected_kind}" ] ||
		fail "${description}: expected kind=${expected_kind}, got kind=${actual_kind:-<empty>}"
	if [ -n "${expected_reason}" ]; then
		local actual_reason
		actual_reason="$(output_field reason)"
		[ "${actual_reason}" = "${expected_reason}" ] ||
			fail "${description}: expected reason=${expected_reason}, got reason=${actual_reason:-<empty>}"
	fi
}

status="$(run_fuzz pass)"
expect "a passing fuzz run" 0 pass "" "${status}"

status="$(run_fuzz flake-then-pass FUZZ_RETRIES=2)"
expect "a boundary flake that passes on retry" 0 pass "" "${status}"

status="$(run_fuzz seed-regression)"
expect "a failing committed seed corpus entry" 1 crash seed-regression "${status}"

status="$(run_fuzz crash)"
expect "a freshly discovered crasher" 1 crash first-discovery "${status}"

status="$(run_fuzz compile-error)"
expect "a non-flake (compile) error" 2 error "" "${status}"

status="$(run_fuzz flake-exhausted FUZZ_RETRIES=2)"
expect "a boundary flake on every attempt" 3 flake "" "${status}"

status="$(run_fuzz infra)"
expect "a SIGTERM'd run" 4 infra "" "${status}"

# FUZZ_RETRIES bounds the attempt count: a single retry (FUZZ_RETRIES=1) must
# not get the second attempt that turns a flake into a pass above.
status="$(run_fuzz flake-then-pass FUZZ_RETRIES=1)"
expect "a boundary flake with no retry budget left" 3 flake "" "${status}"

# FUZZ_LOG_FILE accumulates every attempt's raw output, not just the last one
# — scripts/ci/go-fuzz.sh depends on this for its own artifacts/go-fuzz
# fuzz.log.
log="${fixture}/accumulated.log"
rm -f "${log}" "${fixture}/state"
MOCK_MODE=flake-then-pass MOCK_STATE="${fixture}/state" PATH="${bin}:${PATH}" \
	FUZZ_RETRIES=2 FUZZ_LOG_FILE="${log}" bash "${fuzz_run}" "./internal/fixture/" "FuzzTarget" "1s" >/dev/null 2>&1
grep -q "context deadline exceeded" "${log}" ||
	fail "FUZZ_LOG_FILE must include the first (failed) attempt's output"
grep -q "^ok$" "${log}" ||
	fail "FUZZ_LOG_FILE must include the second (passing) attempt's output"

# FUZZ_ATTEMPT_HOOK fires once per failed attempt, before the run gives up —
# scripts/ci/go-fuzz.sh depends on this to snapshot the corpus per attempt.
hook_log="${fixture}/hook.log"
rm -f "${hook_log}" "${fixture}/state"
record_attempt() { echo "attempt=$1" >>"${hook_log}"; }
export -f record_attempt
export hook_log
MOCK_MODE=flake-exhausted MOCK_STATE="${fixture}/state" PATH="${bin}:${PATH}" \
	FUZZ_RETRIES=2 FUZZ_ATTEMPT_HOOK=record_attempt bash "${fuzz_run}" "./internal/fixture/" "FuzzTarget" "1s" >/dev/null 2>&1 || true
[ "$(wc -l <"${hook_log}" | tr -d ' ')" = "2" ] ||
	fail "FUZZ_ATTEMPT_HOOK must fire once per failed attempt (2 attempts here)"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} fuzz-run.sh check(s) failed" >&2
	exit 1
fi

echo "fuzz-run.sh checks passed."
