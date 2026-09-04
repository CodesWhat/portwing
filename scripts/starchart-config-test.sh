#!/usr/bin/env bash
set -euo pipefail

# Contract for .github/workflows/starchart.yml (PW-3.5).
#
# starchart.yml is a caller, not the workflow that writes anything: it has no
# steps of its own and delegates the whole regenerate-and-commit-back job to
# CodesWhat/.github's starchart-refresh.yml, pinned by full commit SHA. Every
# run so far (33800754348, 33757705980) took that reusable workflow's no-op
# branch, so the git add/commit/push path inside it has never actually
# executed here. That path -- change detection via `git status --porcelain`,
# the github-actions[bot] identity, staging only the two generated SVG paths,
# the `chore(docs): refresh the star-history chart` commit, and the push back
# to `${TARGET_BRANCH}` -- lives entirely inside the pinned SHA and is out of
# this repo's tree; CodesWhat/.github carries its own contract test for it
# (.github/tests/starchart_refresh_contract_test.py). Reaching into another
# repo's file from here would mean either vendoring a copy that goes stale or
# hitting the network at test time, neither of which this repo's config-test
# pattern does anywhere else.
#
# What IS this repo's contract is everything the caller controls about that
# delegation: the pin is frozen to an exact commit (so the behaviour above
# can't drift out from under this file without a deliberate SHA bump), the
# write permission is granted to exactly the one job that uses it, nothing
# here escapes with continue-on-error or an if: always() that would run the
# write path past a failure, and the branch handed to `with: branch:` clears
# the same main/master rejection the reusable workflow enforces at runtime
# and matches this repo's own dev/vX convention -- so a bad caller value
# fails here instead of only inside a run nobody dispatches to check.
#
# The trigger itself (v* tag push, not release:) is already covered by
# scripts/package-release-config-test.sh; this file does not duplicate it.

workflow="${1:-.github/workflows/starchart.yml}"

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

if [ ! -f "${workflow}" ]; then
	fail "workflow not found: ${workflow}"
	exit 1
fi

# --- top-level permissions -------------------------------------------------
#
# Nothing here needs anything by default; the one job that does declares it
# for itself below.

grep -Eq '^permissions:[[:space:]]*\{\}[[:space:]]*$' "${workflow}" ||
	fail "workflow-level permissions must be exactly permissions: {}"

# --- the starchart job -----------------------------------------------------

job_block() {
	awk -v job="  $2:" '
        $0 == job { in_job = 1; next }
        in_job && /^  [^[:space:]]/ { in_job = 0 }
        in_job { print }
    ' "$1"
}

starchart_job="$(job_block "${workflow}" "starchart")"
[ -n "${starchart_job}" ] || fail "expected a top-level 'starchart' job"

# --- contents: write is granted to exactly this job ------------------------
#
# A grep for "contents: write" anywhere in the job block is not enough: a
# second job elsewhere in the file could declare the same permission and this
# check would still pass. Count every job in the whole file that grants it,
# not just this one's.

jobs_block="$(
	awk '
        /^jobs:/ { in_jobs = 1; next }
        in_jobs { print }
    ' "${workflow}"
)"
write_grant_count="$(grep -c '^[[:space:]]*contents: write[[:space:]]*$' <<<"${jobs_block}" || true)"
if [ "${write_grant_count}" -ne 1 ]; then
	fail "exactly one job in the file must grant contents: write (found ${write_grant_count})"
fi
grep -Eq '^[[:space:]]*contents: write[[:space:]]*$' <<<"${starchart_job}" ||
	fail "the starchart job itself must grant contents: write; the reusable workflow's commit-back step needs it"

# --- no unconditional escape hatch on the write path ------------------------
#
# continue-on-error would let a failed refresh (or a failed push) report
# green. An if: always() would run the reusable workflow's write path
# regardless of anything upstream in this job -- there's nothing upstream in
# a single-job file today, but the job block is the one place that guard
# could reappear and silently defeat the change-detection this delegates to.

if grep -Eq 'continue-on-error' <<<"${starchart_job}"; then
	fail "the starchart job must not carry continue-on-error; a failed refresh or push must report failure"
fi
if grep -Eq '^[[:space:]]*if:[[:space:]]*always\(\)' <<<"${starchart_job}"; then
	fail "the starchart job must not run unconditionally via if: always(); the write path is real work, not cleanup"
fi

# --- the reusable workflow pin ----------------------------------------------
#
# A branch or tag ref is mutable, so the write path this file delegates to
# could change behaviour without a commit to this repo. The pin has to be a
# full 40-hex commit SHA with a trailing comment recording why/when, matching
# how every other reusable-workflow pin in this repo is written.

uses_line="$(grep -E '^[[:space:]]*uses:[[:space:]]*CodesWhat/\.github/' <<<"${starchart_job}" || true)"
if [ -z "${uses_line}" ]; then
	fail "expected the starchart job to call CodesWhat/.github's starchart-refresh.yml via uses:"
else
	echo "${uses_line}" | grep -Eq \
		'^[[:space:]]*uses: CodesWhat/\.github/\.github/workflows/starchart-refresh\.yml@[0-9a-f]{40}[[:space:]]+#' ||
		fail "the starchart-refresh.yml pin must be a full 40-hex commit SHA followed by a comment: ${uses_line}"
fi

# --- the target branch ------------------------------------------------------
#
# The reusable workflow already refuses main/master/HEAD/empty as a
# commit-back target at run time (starchart-refresh.yml's "Reject a
# protected branch" step). Asserting the same list here means a caller value
# that would fail there fails at review time on this side instead, and
# pinning it to this repo's own dev/vX convention catches a value that
# clears the reusable workflow's guard but still isn't the dev branch this
# repo actually promotes from.

with_block="$(
	awk '
        $0 == "    with:" { in_with = 1; next }
        in_with && /^    [^[:space:]]/ { in_with = 0 }
        in_with { print }
    ' <<<"${starchart_job}"
)"
branch_line="$(grep -E '^[[:space:]]*branch:' <<<"${with_block}" || true)"
if [ -z "${branch_line}" ]; then
	fail "expected the starchart job's with: block to set branch:"
else
	branch_value="$(sed -E 's/^[[:space:]]*branch:[[:space:]]*//' <<<"${branch_line}")"
	case "${branch_value}" in
	main | master | HEAD | "")
		fail "branch must not be main/master/HEAD/empty; the reusable workflow refuses these too, but this fails before a run is even needed: got '${branch_value}'"
		;;
	esac
	grep -Eq '^dev/v[0-9]+\.[0-9]+$' <<<"${branch_value}" ||
		fail "branch must match this repo's dev/vX.Y convention: got '${branch_value}'"
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} starchart contract check(s) failed" >&2
	exit 1
fi

echo "starchart contract checks passed."
