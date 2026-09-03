#!/usr/bin/env bash
set -euo pipefail

# Self-test for scripts/ci/mutation-gate.sh.
#
# The fixtures below are crafted Gremlins JSON reports whose test_efficacy
# lands on an exact binary tie: 29 killed / 3 lived is 90.625, which Go's
# printf (and Gremlins' own report, and this gate's awk) round to even as
# "90.62", not up as "90.63". A floor copied verbatim from that print has to
# compare equal to the run it came from; a floor one hundredth above it has
# to still fail. Each case pins mutator coverage well clear of its own floor
# so the assertion isolates the test_efficacy comparison.

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
gate="${repository_root}/scripts/ci/mutation-gate.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

expect_status() {
	[ "$1" = "$2" ] || fail "$3: expected exit $1, got $2"
}

expect_contains() {
	grep -Fq "$2" <<<"$1" || fail "$3: output is missing \"$2\""
}

write_report() {
	local path="$1" efficacy="$2" mcover="$3" killed="$4" lived="$5"
	jq -n \
		--argjson efficacy "${efficacy}" \
		--argjson mcover "${mcover}" \
		--argjson killed "${killed}" \
		--argjson lived "${lived}" \
		'{test_efficacy: $efficacy, mutations_coverage: $mcover, mutants_killed: $killed, mutants_lived: $lived}' \
		>"${path}"
}

run_gate() {
	local report="$1" efficacy_floor="$2" mcover_floor="$3"
	set +e
	stdout="$(bash "${gate}" "${report}" "${efficacy_floor}" "${mcover_floor}" 2>"${fixture}/stderr.txt")"
	status=$?
	stderr="$(cat "${fixture}/stderr.txt")"
	set -e
}

# 90.625 against a floor of 90.62 (the value Gremlins actually prints for
# this measurement): at-floor once rounded the same way, so it passes.
report="${fixture}/tie-at-floor.json"
write_report "${report}" 90.625 100 29 3
run_gate "${report}" 90.62 100
expect_status 0 "${status}" "tie rounds to its own print and passes"
expect_contains "${stdout}" "test efficacy 90.62% (floor 90.62%)" "tie rounds to its own print and passes"

# The same 90.625 measurement against a floor one hundredth above its own
# print. Round-half-up used to read 90.625 as 90.63 and pass this by
# mistake; round-to-even reads it as 90.62 and correctly fails it.
report="${fixture}/tie-below-floor.json"
write_report "${report}" 90.625 100 29 3
run_gate "${report}" 90.63 100
expect_status 1 "${status}" "tie one hundredth under a raised floor fails"
expect_contains "${stderr}" "test efficacy 90.62% is below its floor of 90.63%" "tie one hundredth under a raised floor fails"

# 97.959183673469383 (internal/auth's actual JSON value) against the 97.96
# floor copied from Gremlins' own two-decimal print of that same run.
report="${fixture}/repeating-decimal-at-floor.json"
write_report "${report}" 97.959183673469383 100 48 1
run_gate "${report}" 97.96 100
expect_status 0 "${status}" "repeating decimal matches the floor copied from its own print"
expect_contains "${stdout}" "test efficacy 97.96% (floor 97.96%)" "repeating decimal matches the floor copied from its own print"

# An ordinary regression, well clear of any rounding boundary, still fails.
report="${fixture}/plain-regression.json"
write_report "${report}" 50.00 100 5 5
run_gate "${report}" 60.00 100
expect_status 1 "${status}" "plain below-floor regression still fails"
expect_contains "${stderr}" "test efficacy 50.00% is below its floor of 60.00%" "plain below-floor regression still fails"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} mutation gate check(s) failed" >&2
	exit 1
fi

echo "mutation gate self-tests passed."
