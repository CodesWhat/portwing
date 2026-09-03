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

# Assert the exact flags fuzz-run.sh is contractually supposed to pass, not
# just that some argument has the right shape — a hardcoded
# -fuzz=^WrongTarget$ or the wrong -fuzztime value would still satisfy a
# shape-only check. MOCK_EXPECT_TARGET/MOCK_EXPECT_FUZZTIME carry the exact
# target and fuzztime the test itself passed to fuzz-run.sh, so the
# expectation is derived from the same call the test made, not duplicated
# here as an independent literal that could drift — or coincidentally match
# a bug — on its own.
expected_fuzz="-fuzz=^${MOCK_EXPECT_TARGET:?MOCK_EXPECT_TARGET must be set}\$"
expected_fuzztime="-fuzztime=${MOCK_EXPECT_FUZZTIME:?MOCK_EXPECT_FUZZTIME must be set}"
saw_run=0
saw_fuzz=0
saw_fuzztime=0
saw_parallel=0
for arg in "$@"; do
	case "${arg}" in
	'-run=^$') saw_run=1 ;;
	"${expected_fuzz}") saw_fuzz=1 ;;
	"${expected_fuzztime}") saw_fuzztime=1 ;;
	-parallel=*) saw_parallel=1 ;;
	esac
done
[ "${saw_run}" -eq 1 ] || {
	echo "go test missing -run=^\$ in args: $*" >&2
	exit 92
}
[ "${saw_fuzz}" -eq 1 ] || {
	echo "go test missing exact ${expected_fuzz} in args: $*" >&2
	exit 93
}
[ "${saw_fuzztime}" -eq 1 ] || {
	echo "go test missing exact ${expected_fuzztime} in args: $*" >&2
	exit 94
}
if [ -n "${FUZZ_PARALLEL:-}" ]; then
	expected_parallel="-parallel=${FUZZ_PARALLEL}"
	if [ "${saw_parallel}" -ne 1 ]; then
		echo "go test missing -parallel= in args despite FUZZ_PARALLEL set: $*" >&2
		exit 95
	fi
	case " $* " in
	*" ${expected_parallel} "*) ;;
	*)
		echo "go test's -parallel= value doesn't match FUZZ_PARALLEL=${FUZZ_PARALLEL}: $*" >&2
		exit 96
		;;
	esac
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
		MOCK_EXPECT_TARGET="FuzzTarget" MOCK_EXPECT_FUZZTIME="1s" \
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

# FUZZ_PARALLEL is forwarded as -parallel=<value>. None of the scenarios
# above set it, which would otherwise leave the go shim's -parallel
# assertion (fuzz-run-script-test.sh's own check that fuzz-run.sh actually
# passes the value through) dead code that never runs.
status="$(run_fuzz pass FUZZ_PARALLEL=3)"
expect "a passing run with FUZZ_PARALLEL set" 0 pass "" "${status}"

# FUZZ_LOG_FILE accumulates every attempt's raw output, not just the last one
# — scripts/ci/go-fuzz.sh depends on this for its own artifacts/go-fuzz
# fuzz.log.
log="${fixture}/accumulated.log"
rm -f "${log}" "${fixture}/state"
MOCK_MODE=flake-then-pass MOCK_STATE="${fixture}/state" PATH="${bin}:${PATH}" \
	MOCK_EXPECT_TARGET="FuzzTarget" MOCK_EXPECT_FUZZTIME="1s" \
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
	MOCK_EXPECT_TARGET="FuzzTarget" MOCK_EXPECT_FUZZTIME="1s" \
	FUZZ_RETRIES=2 FUZZ_ATTEMPT_HOOK=record_attempt bash "${fuzz_run}" "./internal/fixture/" "FuzzTarget" "1s" >/dev/null 2>&1 || true
[ "$(wc -l <"${hook_log}" | tr -d ' ')" = "2" ] ||
	fail "FUZZ_ATTEMPT_HOOK must fire once per failed attempt (2 attempts here)"

# FUZZ_RETRIES must be validated before anything runs: 0, negative, or
# non-numeric used to skip the retry loop's body entirely and either
# misreport the misconfiguration as kind=flake or abort on an unbound `rc`,
# depending on the bash build. None of these should ever reach `go test`.
for bad_retries in 0 -1 abc; do
	state="${fixture}/state"
	output="${fixture}/output"
	rm -f "${state}" "${output}"
	status=0
	env MOCK_MODE=pass MOCK_STATE="${state}" PATH="${bin}:${PATH}" \
		FUZZ_OUTPUT_FILE="${output}" FUZZ_RETRIES="${bad_retries}" \
		MOCK_EXPECT_TARGET="FuzzTarget" MOCK_EXPECT_FUZZTIME="1s" \
		bash "${fuzz_run}" "./internal/fixture/" "FuzzTarget" "1s" >"${fixture}/stdout" 2>"${fixture}/stderr" || status=$?
	expect "FUZZ_RETRIES=${bad_retries} rejected up front" 2 error "" "${status}"
	[ -f "${state}" ] &&
		fail "FUZZ_RETRIES=${bad_retries} must be rejected before invoking go test, but the fake go ran"
done

# tee losing its write to the attempt log must not be silently treated as a
# clean `go test` run. Shim `mktemp` to hand fuzz-run.sh a directory instead
# of a file, so `tee "${attempt_log}"` fails with EISDIR the same way an
# unwritable path (full disk, permissions) would.
real_mktemp="$(command -v mktemp)"
mktemp_bin="${fixture}/mktemp-bin"
mkdir -p "${mktemp_bin}"
cat >"${mktemp_bin}/mktemp" <<SHIM
#!/usr/bin/env bash
exec "${real_mktemp}" -d
SHIM
chmod +x "${mktemp_bin}/mktemp"

state="${fixture}/state"
output="${fixture}/output"
rm -f "${state}" "${output}"
status=0
env MOCK_MODE=pass MOCK_STATE="${state}" PATH="${mktemp_bin}:${bin}:${PATH}" \
	FUZZ_OUTPUT_FILE="${output}" \
	MOCK_EXPECT_TARGET="FuzzTarget" MOCK_EXPECT_FUZZTIME="1s" \
	bash "${fuzz_run}" "./internal/fixture/" "FuzzTarget" "1s" >"${fixture}/stdout" 2>"${fixture}/stderr" || status=$?
expect "tee failing to write the attempt log" 2 error "" "${status}"

# Meta-check: prove the fake go shim's flag assertions actually bite. If
# fuzz-run.sh silently dropped -fuzz= from its go test invocation, nightly
# and monthly would run zero fuzz targets per matrix entry and still look
# green on every other check in this file.
mutated="${fixture}/fuzz-run-no-fuzz-flag.sh"
sed 's/-fuzz=[^[:space:]]*//' "${fuzz_run}" >"${mutated}"
chmod +x "${mutated}"
grep -q -- '-fuzz=' "${mutated}" &&
	fail "mutation setup failed: -fuzz= is still present in ${mutated}"

state="${fixture}/state"
output="${fixture}/output"
rm -f "${state}" "${output}"
mutation_status=0
env MOCK_MODE=pass MOCK_STATE="${state}" PATH="${bin}:${PATH}" \
	FUZZ_OUTPUT_FILE="${output}" \
	MOCK_EXPECT_TARGET="FuzzTarget" MOCK_EXPECT_FUZZTIME="1s" \
	bash "${mutated}" "./internal/fixture/" "FuzzTarget" "1s" >"${fixture}/stdout" 2>"${fixture}/stderr" || mutation_status=$?
[ "${mutation_status}" -ne 0 ] ||
	fail "a copy of fuzz-run.sh missing -fuzz= from its go test invocation must be caught by the go shim, but it exited 0"
# The shim's own rejection message is `go test`'s stdout, which fuzz-run.sh
# tees straight through to its own stdout — not its stderr.
grep -q "missing exact" "${fixture}/stdout" ||
	fail "the go shim must report why it rejected a fuzz-run.sh invocation missing -fuzz="

if [ "${failures}" -ne 0 ]; then
	echo "${failures} fuzz-run.sh check(s) failed" >&2
	exit 1
fi

echo "fuzz-run.sh checks passed."
