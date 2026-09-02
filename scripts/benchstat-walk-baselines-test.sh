#!/usr/bin/env bash
set -euo pipefail

# Self-test for scripts/benchstat-walk-baselines.sh.
#
# scripts/benchstat-gate.sh is stubbed so this test exercises only the
# walk-back loop: which candidate got tried, in what order, and what happens
# when none, one, or several of them are comparable. The gate script's own
# parsing of benchstat output is covered separately by
# scripts/benchstat-gate-script-test.sh.

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
script="${repository_root}/scripts/benchstat-walk-baselines.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

mkdir -p "${fixture}/results"
echo "current run" >"${fixture}/current.txt"
# Names are chosen so none is a substring of another (unlike "match" and
# "mismatch" would be), so a later `grep -F` for one candidate's filename
# can't accidentally match another.
echo "other-cpu baseline" >"${fixture}/results/othercpu-results.txt"
echo "same-cpu baseline" >"${fixture}/results/samecpu-results.txt"
echo "slower baseline" >"${fixture}/results/slower-results.txt"

# Stub scripts/benchstat-gate.sh: its exit code and summary are driven by the
# basename of the --baseline file, so a fixture file's name is also its
# scripted behavior. Every invocation is appended to gate.log (one line:
# the baseline path it was called with), so the walk-back's call order is
# directly observable.
cat >"${fixture}/stub-gate.sh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
baseline=""
current=""
summary_file=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--baseline)
		baseline="$2"
		shift 2
		;;
	--current)
		current="$2"
		shift 2
		;;
	--summary-file)
		summary_file="$2"
		shift 2
		;;
	*)
		shift
		;;
	esac
done
echo "${baseline}" >>"${GATE_LOG}"
case "${baseline}" in
*othercpu*)
	echo "no comparison for ${baseline}" >"${summary_file}"
	exit 3
	;;
*slower*)
	echo "**Regression.** ${baseline} vs ${current}" >"${summary_file}"
	exit 1
	;;
*)
	echo "**No regression.** ${baseline} vs ${current}" >"${summary_file}"
	exit 0
	;;
esac
STUB
chmod +x "${fixture}/stub-gate.sh"

status=0
summary=""
gate_log=""

run_walk() {
	local candidates="$1"
	: >"${fixture}/gate.log"
	set +e
	GATE_LOG="${fixture}/gate.log" \
		BENCHSTAT_GATE="${fixture}/stub-gate.sh" \
		"${@:2}" \
		bash "${script}" "${candidates}" "${fixture}/current.txt" "${fixture}/summary.md" \
		>"${fixture}/stdout.txt" 2>"${fixture}/stderr.txt"
	status=$?
	set -e
	summary="$(cat "${fixture}/summary.md" 2>/dev/null || true)"
	gate_log="$(cat "${fixture}/gate.log" 2>/dev/null || true)"
}

expect_status() {
	[ "$1" = "$2" ] || fail "$3: expected exit $1, got $2"
}

expect_contains() {
	grep -Fq "$2" <<<"$1" || fail "$3: expected to find \"$2\""
}

expect_line_count() {
	local got
	got="$(grep -c . <<<"$1" || true)"
	[ "${got}" = "$2" ] || fail "$3: expected $2 gate invocation(s), got ${got}"
}

# (a) First candidate mismatches (different hardware), second matches: the
# loop must skip the mismatch and gate against the match, in that order.
cat >"${fixture}/candidates-othercpu-then-samecpu.txt" <<CANDIDATES
111	${fixture}/results/othercpu-results.txt	run 111 (other cpu)
222	${fixture}/results/samecpu-results.txt	run 222 (same cpu)
CANDIDATES

run_walk "${fixture}/candidates-othercpu-then-samecpu.txt"
expect_status 0 "${status}" "mismatch then match"
expect_contains "${summary}" "**No regression.**" "mismatch then match"
expect_contains "${summary}" "samecpu-results.txt" "mismatch then match"
expect_line_count "${gate_log}" 2 "mismatch then match"
[ "$(sed -n '1p' <<<"${gate_log}")" = "${fixture}/results/othercpu-results.txt" ] ||
	fail "mismatch then match: first attempt should be the mismatched candidate"
[ "$(sed -n '2p' <<<"${gate_log}")" = "${fixture}/results/samecpu-results.txt" ] ||
	fail "mismatch then match: second attempt should be the matching candidate"

# (b) Empty candidates file: the documented "no earlier successful run" text,
# no attempt to run the gate script at all.
: >"${fixture}/candidates-empty.txt"
run_walk "${fixture}/candidates-empty.txt"
expect_status 0 "${status}" "empty candidates"
expect_contains "${summary}" "**No baseline.**" "empty candidates"
expect_contains "${summary}" "recorded" "empty candidates"
expect_line_count "${gate_log}" 0 "empty candidates"

# A candidates file that never matches: the loop exhausts every entry, warns
# on stdout (this is a GitHub Actions "::warning::" annotation, which is read
# from stdout, not stderr), and still exits 0 rather than failing the job
# over a hardware mismatch.
cat >"${fixture}/candidates-all-othercpu.txt" <<CANDIDATES
111	${fixture}/results/othercpu-results.txt	run 111 (other cpu)
CANDIDATES
run_walk "${fixture}/candidates-all-othercpu.txt"
expect_status 0 "${status}" "all mismatch"
expect_contains "$(cat "${fixture}/stdout.txt")" "::warning::" "all mismatch"
expect_line_count "${gate_log}" 1 "all mismatch"

# A regression on the first candidate stops the walk-back immediately and
# propagates the gate script's exit code.
cat >"${fixture}/candidates-slower-first.txt" <<CANDIDATES
111	${fixture}/results/slower-results.txt	run 111 (slower)
222	${fixture}/results/samecpu-results.txt	run 222 (same cpu)
CANDIDATES
run_walk "${fixture}/candidates-slower-first.txt"
expect_status 1 "${status}" "regression on first candidate"
expect_contains "${summary}" "**Regression.**" "regression on first candidate"
expect_line_count "${gate_log}" 1 "regression on first candidate: must not try the second candidate"

# ACCEPT_NEW_BASELINE short-circuits before the candidates file is even read;
# a nonexistent path proves it never gets touched.
run_walk "${fixture}/does-not-exist.txt" env ACCEPT_NEW_BASELINE=true
expect_status 0 "${status}" "accept new baseline"
expect_contains "${summary}" "**Comparison skipped by request.**" "accept new baseline"
expect_line_count "${gate_log}" 0 "accept new baseline"

# Usage error: wrong argument count.
set +e
bash "${script}" "${fixture}/candidates-empty.txt" "${fixture}/current.txt" >/dev/null 2>&1
usage_status=$?
set -e
expect_status 2 "${usage_status}" "missing summary argument"

# (c) Mutation check: the walk-back depends on
# `[ "${status}" -eq 3 ] || break` to keep trying candidates only while the
# previous one was an unmatched-hardware result (status 3). Invert that
# comparison in a /tmp copy and confirm the othercpu-then-samecpu scenario no
# longer reaches the matching candidate — proving this test would catch the
# inversion. (An inverted condition breaks on the *first*, mismatched,
# candidate instead of continuing past it, so the walk-back never gets to the
# second one.)
mutant="${fixture}/benchstat-walk-baselines.mutant.sh"
# shellcheck disable=SC2016 # Single-quoted on purpose: this is a literal sed
# pattern matching the script's own `${status}` text, not our own expansion.
sed 's/\[ "\${status}" -eq 3 \] || break/[ "${status}" -ne 3 ] || break/' \
	"${script}" >"${mutant}"
if diff -q "${script}" "${mutant}" >/dev/null; then
	fail "mutation check: the sed substitution made no change; nothing was tested"
fi
chmod +x "${mutant}"

: >"${fixture}/gate.log"
set +e
GATE_LOG="${fixture}/gate.log" \
	BENCHSTAT_GATE="${fixture}/stub-gate.sh" \
	bash "${mutant}" \
	"${fixture}/candidates-othercpu-then-samecpu.txt" "${fixture}/current.txt" \
	"${fixture}/mutant-summary.md" \
	>/dev/null 2>&1
set -e
mutant_gate_log="$(cat "${fixture}/gate.log" 2>/dev/null || true)"

# The correct (unmutated) run above made 2 gate invocations for this same
# candidates file (othercpu, then samecpu). The mutant must diverge from
# that: it should stop after the first candidate and never try the second.
if [ "$(grep -c . <<<"${mutant_gate_log}")" != "1" ]; then
	fail "mutation check: inverting -eq 3 || break did not change the walk-back's call count; gate.log has: ${mutant_gate_log}"
fi
if grep -Fq "samecpu-results.txt" <<<"${mutant_gate_log}"; then
	fail "mutation check: inverting -eq 3 || break still reached the matching candidate"
fi

# Contract check: the workflow this script was extracted from must actually
# call it, with the same three files in the same order, rather than the
# walk-back logic silently drifting back into an inline `run:` block.
workflow="${repository_root}/.github/workflows/quality-bench-monthly.yml"
if ! grep -Fq "scripts/benchstat-walk-baselines.sh" "${workflow}"; then
	fail "quality-bench-monthly.yml must call scripts/benchstat-walk-baselines.sh"
fi
if ! grep -Fq "baseline-candidates.txt benchmark-results.txt bench-comparison.md" "${workflow}"; then
	fail "quality-bench-monthly.yml must call scripts/benchstat-walk-baselines.sh with the candidates, current, and summary files in order"
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} benchstat walk-baselines check(s) failed" >&2
	exit 1
fi

echo "benchstat walk-baselines self-tests passed."
