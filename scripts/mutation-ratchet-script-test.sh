#!/usr/bin/env bash
set -euo pipefail

# Self-test for scripts/ci/mutation-ratchet.sh.
#
# Each case builds a throwaway records directory (one subdirectory per
# package, holding a quality-history-record.json the way the downloaded
# mutation-history-*-<run_id> artifacts do) and, where needed, a
# quality-history mutation.jsonl, then runs the real script against them.

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
ratchet="${repository_root}/scripts/ci/mutation-ratchet.sh"
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

# Write one package's quality-history-record.json fixture.
#
# write_record <dir> <name> <package> <mode> <outcome> <timed_out> \
#   <efficacy_floor> <efficacy> <mcover_floor> <mcover>
write_record() {
	local dir="$1" name="$2" package="$3" mode="$4" outcome="$5" timed_out="$6"
	local efficacy_floor="$7" efficacy="$8" mcover_floor="$9" mcover="${10}"
	mkdir -p "${dir}"
	jq -n \
		--arg name "${name}" --arg package "${package}" --arg mode "${mode}" --arg outcome "${outcome}" \
		--argjson timed_out "${timed_out}" \
		--argjson efficacy_floor "${efficacy_floor}" --argjson efficacy "${efficacy}" \
		--argjson mcover_floor "${mcover_floor}" --argjson mcover "${mcover}" \
		'{
            name: $name, package: $package, mode: $mode, outcome: $outcome, timed_out: $timed_out,
            efficacy_floor: $efficacy_floor, efficacy: $efficacy,
            mutator_coverage_floor: $mcover_floor, mutator_coverage: $mcover
        }' >"${dir}/quality-history-record.json"
}

# Write one history.jsonl row for a package. run_id defaults to omitted, so
# existing call sites (rows with no run_id) are unaffected.
write_history_row() {
	local name="$1" efficacy="$2" mcover="$3" run_id="${4:-}"
	jq -cn \
		--arg name "${name}" --argjson efficacy "${efficacy}" --argjson mcover "${mcover}" \
		--arg run_id "${run_id}" \
		'{name: $name, mode: "gated", outcome: "success", efficacy: $efficacy, mutator_coverage: $mcover}
		 + (if $run_id == "" then {} else {run_id: $run_id} end)'
}

# --- (1) a measured value below the current floor produces no proposal ------
#
# Both metrics measure below their own floor. min(pool) is then below the
# floor too, and basis - buffer is lower still, so the "proposed > floor"
# guard rejects it outright.
case1="${fixture}/case1"
mkdir -p "${case1}/records/regressed"
write_record "${case1}/records/regressed" "regressed" "./internal/regressed" \
	"gated" "success" 0 90.00 85.00 90.00 88.00
: >"${case1}/history.jsonl"

set +e
bash "${ratchet}" "${case1}/records" "${case1}/history.jsonl" "${case1}/out.json"
status=$?
set -e
expect_status 0 "${status}" "a below-floor measurement still exits 0"
count="$(jq '.proposals | length' "${case1}/out.json")"
[ "${count}" -eq 0 ] || fail "a below-floor measurement must produce no proposal, got ${count}"

# --- (2) proposed < measured always, proposed > current_floor always --------
#
# A package with real headroom and some history spread, so it actually
# proposes on both metrics.
case2="${fixture}/case2"
mkdir -p "${case2}/records/edge"
write_record "${case2}/records/edge" "edge" "./internal/edge" \
	"gated" "success" 0 74.73 85.00 91.00 95.00
{
	write_history_row "edge" 81.90 92.00
	write_history_row "edge" 83.00 93.00
} >"${case2}/history.jsonl"

bash "${ratchet}" "${case2}/records" "${case2}/history.jsonl" "${case2}/out.json"
proposal_count="$(jq '.proposals | length' "${case2}/out.json")"
[ "${proposal_count}" -gt 0 ] || fail "case2 fixture should have produced at least one proposal to test"

bad="$(jq -r '
    .proposals[]
    | select(.proposed <= .current_floor)
    | .metric
' "${case2}/out.json")"
[ -z "${bad}" ] || fail "proposed must always be > current_floor, violated for: ${bad}"

# measured is not carried in the proposal, but proposed is derived from
# basis (min of the pool, itself <= the measured value) minus a strictly
# positive buffer, so proposed < basis <= measured always. Assert the
# stronger, checkable form: proposed < basis.
bad="$(jq -r '
    .proposals[]
    | select(.proposed >= .basis)
    | .metric
' "${case2}/out.json")"
[ -z "${bad}" ] || fail "proposed must always be < basis (and thus < measured), violated for: ${bad}"

# --- (3) min(pool) drives it, not the current measurement -------------------
#
# Pool is exactly [91.11, 92.00] against a floor of 90.00: basis 91.11,
# spread 0.89, buffer max(1.0, 0.89) = 1.00, proposed 90.11, gain 0.11.
# Below the 2.00 minimum gain, so no proposal for efficacy.
case3="${fixture}/case3"
mkdir -p "${case3}/records/pool-driven"
write_record "${case3}/records/pool-driven" "pool-driven" "./internal/pool-driven" \
	"gated" "success" 0 90.00 92.00 0 0
write_history_row "pool-driven" 91.11 0 >"${case3}/history.jsonl"

bash "${ratchet}" "${case3}/records" "${case3}/history.jsonl" "${case3}/out.json"
efficacy_proposals="$(jq '[.proposals[] | select(.metric == "efficacy")] | length' "${case3}/out.json")"
[ "${efficacy_proposals}" -eq 0 ] ||
	fail "pool [91.11, 92.00] against floor 90.00 must not clear the 2.00 minimum gain"

# --- (4) mode/timed_out skips, each with a reason ----------------------------
case4="${fixture}/case4"
write_record "${case4}/records/zm" "zm" "./internal/zm" "zero-mutants" "success" 0 0 0 0 0
write_record "${case4}/records/um" "um" "./internal/um" "unmeasured" "skipped" 0 0 0 0 0
write_record "${case4}/records/up" "up" "./internal/up" "unparseable" "failure" 0 0 0 0 0
write_record "${case4}/records/to" "to" "./internal/to" "gated" "success" 5 80.00 85.00 80.00 85.00
: >"${case4}/history.jsonl"

bash "${ratchet}" "${case4}/records" "${case4}/history.jsonl" "${case4}/out.json"
while read -r pkg_name expected_reason; do
	reason="$(jq -r --arg n "${pkg_name}" '.skipped[] | select(.name == $n) | .reason' "${case4}/out.json")"
	[ "${reason}" = "${expected_reason}" ] ||
		fail "${pkg_name}: expected skip reason '${expected_reason}', got '${reason}'"
done <<-EOF
	zm zero-mutants
	um unmeasured
	up unparseable
	to timed-out
EOF
proposal_count="$(jq '.proposals | length' "${case4}/out.json")"
[ "${proposal_count}" -eq 0 ] || fail "every package in case4 should have been skipped, not proposed"

# --- (5) the proposed value re-fed to mutation-gate.sh against its own basis -
case5="${fixture}/case5"
mkdir -p "${case5}/records/roundtrip"
write_record "${case5}/records/roundtrip" "roundtrip" "./internal/roundtrip" \
	"gated" "success" 0 74.73 85.00 91.00 95.00
{
	write_history_row "roundtrip" 81.90 92.00
	write_history_row "roundtrip" 83.00 93.00
} >"${case5}/history.jsonl"

bash "${ratchet}" "${case5}/records" "${case5}/history.jsonl" "${case5}/out.json"
proposed="$(jq -r '.proposals[] | select(.metric == "efficacy") | .proposed' "${case5}/out.json")"
basis="$(jq -r '.proposals[] | select(.metric == "efficacy") | .basis' "${case5}/out.json")"
[ -n "${proposed}" ] || fail "case5 fixture should have produced an efficacy proposal to round-trip"

report="${case5}/basis-report.json"
jq -n --argjson efficacy "${basis}" \
	'{test_efficacy: $efficacy, mutations_coverage: 100, mutants_killed: 1, mutants_lived: 0}' >"${report}"

set +e
bash "${gate}" "${report}" "${proposed}" 0
gate_status=$?
set -e
expect_status 0 "${gate_status}" "a run measuring the pool's own basis (${basis}) must clear the proposed floor (${proposed})"

# --- (6) this run's own history row must not evict the oldest prior --------
#
# The ratchet job needs [gremlins, history], so by the time it checks out
# quality-history the history job has already appended this run's row for
# every package. Without the run_id filter, the newest slot in the 6-row
# window holds a duplicate of the current measurement instead of the oldest
# real prior (70.00), raising basis from 70.00 to 90.00 and turning a
# correctly-conservative "no proposal" into a proposal of 84.00.
case6="${fixture}/case6"
mkdir -p "${case6}/records/edge"
write_record "${case6}/records/edge" "edge" "./internal/edge" \
	"gated" "success" 0 74.73 96.00 91.00 99.00
{
	write_history_row "edge" 70.00 99.00 r1
	write_history_row "edge" 90.00 99.00 r2
	write_history_row "edge" 90.00 99.00 r3
	write_history_row "edge" 90.00 99.00 r4
	write_history_row "edge" 90.00 99.00 r5
	write_history_row "edge" 90.00 99.00 r6
	write_history_row "edge" 96.00 99.00 CURRENT
} >"${case6}/history.jsonl"

GITHUB_RUN_ID=CURRENT bash "${ratchet}" "${case6}/records" "${case6}/history.jsonl" "${case6}/out.json"
efficacy_proposals="$(jq '[.proposals[] | select(.metric == "efficacy")] | length' "${case6}/out.json")"
[ "${efficacy_proposals}" -eq 0 ] ||
	fail "this run's own history row must not evict the oldest prior (70.00) from the 6-row window"

# --- (7) samples counts distinct runs, not this run counted twice ----------
case7="${fixture}/case7"
mkdir -p "${case7}/records/dupe"
write_record "${case7}/records/dupe" "dupe" "./internal/dupe" \
	"gated" "success" 0 74.73 90.00 0 0
{
	write_history_row "dupe" 89.00 0 r1
	write_history_row "dupe" 90.00 0 CURRENT
} >"${case7}/history.jsonl"

GITHUB_RUN_ID=CURRENT bash "${ratchet}" "${case7}/records" "${case7}/history.jsonl" "${case7}/out.json"
samples="$(jq -r '.proposals[] | select(.metric == "efficacy") | .samples' "${case7}/out.json")"
[ "${samples}" = "2" ] ||
	fail "samples must count this run plus 1 real prior, not a duplicate of itself, got samples=${samples}"

# --- (8) history rows written before the run_id field existed still count --
#
# Guards against over-filtering: a row with no run_id must not be treated as
# "this run" just because GITHUB_RUN_ID is set.
case8="${fixture}/case8"
mkdir -p "${case8}/records/legacy"
write_record "${case8}/records/legacy" "legacy" "./internal/legacy" \
	"gated" "success" 0 74.73 96.00 0 0
{
	write_history_row "legacy" 95.00 0
	write_history_row "legacy" 95.00 0
} >"${case8}/history.jsonl"

GITHUB_RUN_ID=CURRENT bash "${ratchet}" "${case8}/records" "${case8}/history.jsonl" "${case8}/out.json"
samples="$(jq -r '.proposals[] | select(.metric == "efficacy") | .samples' "${case8}/out.json")"
[ "${samples}" = "3" ] ||
	fail "history rows without a run_id must survive the current-run filter, got samples=${samples}"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} mutation ratchet check(s) failed" >&2
	exit 1
fi

echo "mutation ratchet self-tests passed."
