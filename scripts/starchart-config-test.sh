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
# here escapes with continue-on-error or an if: always() (in any spelling)
# that would run the write path past a failure, no secrets are forwarded into
# a workflow that needs none, the trigger set is exactly what this file uses
# today, and the branch handed to `with: branch:` clears the same main/master
# rejection the reusable workflow enforces at runtime, matches this repo's own
# dev/vX convention, and agrees with renovate.json's own idea of which branch
# is "the" dev branch.
#
# Every assertion below reads a specific key line at its exact YAML
# indentation (anchored with a fixed run of literal spaces), not "this text
# appears somewhere in the job". A block scalar (`name: |`, `accent: |`, ...)
# can contain any text at all, including a line that looks byte-for-byte like
# a real key -- but a genuine block-scalar body is indented deeper than the
# key that introduces it, so an exact-indent anchor does not match it. Where a
# decoy could still land at the exact indent of a real key (nothing stops
# that from being technically well-formed, if oddly-shaped, YAML), the checks
# below also count matches file-wide so a duplicate -- real key plus decoy --
# fails on "found more than one" rather than silently picking whichever line
# grep happened to see first.

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

# --- triggers ----------------------------------------------------------------
#
# The header comment explains why this fires on the v* tag push instead of
# release: or a dispatch, and workflow_dispatch exists for a manual re-run.
# Any OTHER trigger key -- schedule: in particular -- would let the write path
# run unattended on a cadence the "main is the released version" invariant
# does not allow for a committed artifact. Parse by indentation rather than
# grep for individual key names, so a decoy key hidden inside a block scalar
# under on: can't pass by accident: keys immediately under `on:` sit at
# 2-space indent, keys immediately under `push:` sit at 4-space indent.

on_block="$(
	awk '
        /^on:/ { in_on = 1; next }
        in_on && /^[^[:space:]]/ { in_on = 0 }
        in_on { print }
    ' "${workflow}"
)"
[ -n "${on_block}" ] || fail "expected a top-level on: trigger block"

on_keys="$(grep -E '^  [^[:space:]]' <<<"${on_block}" | sed -E 's/^  ([^:[:space:]]+):.*/\1/' | sort -u)"
expected_on_keys="$(printf '%s\n' push workflow_dispatch | sort -u)"
if [ "${on_keys}" != "${expected_on_keys}" ]; then
	fail "on: must declare exactly push and workflow_dispatch and nothing else: got '$(tr '\n' ' ' <<<"${on_keys}")'"
fi

push_block="$(
	awk '
        $0 == "  push:" { in_push = 1; next }
        in_push && /^  [^[:space:]]/ { in_push = 0 }
        in_push { print }
    ' <<<"${on_block}"
)"
push_keys="$(grep -E '^    [^[:space:]]' <<<"${push_block}" | sed -E 's/^    ([^:[:space:]]+):.*/\1/' | sort -u)"
if [ "${push_keys}" != "tags" ]; then
	fail "on.push: must declare only tags: and nothing else (no branches:, for instance): got '$(tr '\n' ' ' <<<"${push_keys}")'"
fi

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

# --- contents: write is granted to exactly this job, and only this job -----
#
# Two independent checks. First, a file-wide count: exactly one line anywhere
# in the file may read "      contents: write" at that exact indentation, so
# neither a deleted grant nor a second job quietly picking one up goes
# unnoticed. Second, a structural check scoped to the starchart job itself:
# its own `    permissions:` key must be followed immediately by that one
# line and then a dedent, so a grant that exists somewhere in the file but
# NOT on this job (moved to an unrelated job, or replaced with an inline
# `permissions: {}` while a decoy line sits elsewhere) still fails, with a
# message that says so rather than reporting a false "file-wide count is
# fine".

write_grant_count="$(grep -cE '^      contents: write$' "${workflow}" || true)"
if [ "${write_grant_count}" -ne 1 ]; then
	fail "exactly one line in the file may read '      contents: write' (found ${write_grant_count})"
fi

permissions_state="$(awk '
	BEGIN { found = 0; state = 0; ok = 1 }
	/^    permissions:$/ {
		found = 1
		state = 1
		next
	}
	state == 1 {
		if ($0 == "      contents: write") {
			state = 2
		} else {
			ok = 0
			state = 0
		}
		next
	}
	state == 2 {
		if ($0 ~ /^      /) {
			ok = 0
		}
		state = 0
		next
	}
	END {
		if (found == 0) {
			print "missing"
		} else if (ok == 0) {
			print "malformed"
		} else {
			print "ok"
		}
	}
' <<<"${starchart_job}")"

if [ "${permissions_state}" != "ok" ]; then
	fail "the starchart job's own '    permissions:' must be followed immediately by exactly '      contents: write' and nothing else; a grant recorded elsewhere in the file (a different job, or text inside a block scalar) does not satisfy this contract"
fi

# --- no unconditional escape hatch on the write path ------------------------
#
# continue-on-error would let a failed refresh (or a failed push) report
# green. An if: always() -- in either the bare or the ${{ }} expression
# spelling -- would run the reusable workflow's write path regardless of
# anything upstream in this job; there's nothing upstream in a single-job
# file today, but the job block is the one place that guard could reappear
# and silently defeat the change-detection this delegates to.

if grep -Eq 'continue-on-error' <<<"${starchart_job}"; then
	fail "the starchart job must not carry continue-on-error; a failed refresh or push must report failure"
fi
if grep -Eq '^    if:.*always\(\)' <<<"${starchart_job}"; then
	fail "the starchart job must not run unconditionally via if: always() in any spelling; the write path is real work, not cleanup"
fi

# --- no secrets forwarded into the reusable workflow -------------------------
#
# The reusable workflow's steps don't need any of this repo's secrets; a
# `secrets: inherit` here would hand it every secret this repo has for no
# reason.

if grep -Eq '^    secrets:' <<<"${starchart_job}"; then
	fail "the starchart job must not declare secrets: (inherit or otherwise); the reusable workflow needs none"
fi

# --- the reusable workflow pin ----------------------------------------------
#
# A branch or tag ref is mutable, so the write path this file delegates to
# could change behaviour without a commit to this repo. The pin has to be a
# full 40-hex commit SHA with a trailing non-empty comment recording why/when,
# matching how every other reusable-workflow pin in this repo is written, and
# it has to be the only `uses:` line anywhere in the file -- a second one,
# wherever it's hiding, is a decoy or a second delegation this contract has
# not reviewed.

all_uses_count="$(grep -cE '^[[:space:]]*uses:[[:space:]]' "${workflow}" || true)"
job_uses_lines="$(grep -E '^    uses: ' <<<"${starchart_job}" || true)"
job_uses_count=0
if [ -n "${job_uses_lines}" ]; then
	job_uses_count="$(wc -l <<<"${job_uses_lines}" | tr -d '[:space:]')"
fi

if [ "${job_uses_count}" -ne 1 ]; then
	fail "expected exactly one 'uses:' line at 4-space indent inside the starchart job (found ${job_uses_count})"
elif [ "${all_uses_count}" -ne 1 ]; then
	fail "no other 'uses:' lines are allowed anywhere in the file, but found ${all_uses_count} total"
else
	echo "${job_uses_lines}" | grep -Eq \
		'^    uses: CodesWhat/\.github/\.github/workflows/starchart-refresh\.yml@[0-9a-f]{40}[[:space:]]+#[[:space:]]*[^[:space:]]' ||
		fail "the starchart-refresh.yml pin must be a full 40-hex commit SHA followed by a non-empty comment: ${job_uses_lines}"
fi

# --- the target branch ------------------------------------------------------
#
# The reusable workflow already refuses main/master/HEAD/empty as a
# commit-back target at run time (starchart-refresh.yml's "Reject a
# protected branch" step). Asserting the same list here means a caller value
# that would fail there fails at review time on this side instead, and
# pinning it to this repo's own dev/vX convention catches a value that
# clears the reusable workflow's guard but still isn't the dev branch this
# repo actually promotes from. renovate.json's baseBranchPatterns names that
# same dev branch for an entirely different reason (where Renovate opens
# PRs); the two roll together at a release cut, so drift between them is
# exactly the failure this last check exists to catch.

with_block="$(
	awk '
        $0 == "    with:" { in_with = 1; next }
        in_with && /^    [^[:space:]]/ { in_with = 0 }
        in_with { print }
    ' <<<"${starchart_job}"
)"

branch_lines="$(grep -E '^      branch: ' <<<"${with_block}" || true)"
branch_count=0
if [ -n "${branch_lines}" ]; then
	branch_count="$(wc -l <<<"${branch_lines}" | tr -d '[:space:]')"
fi

# The three checks below (forbidden value, shape, renovate.json agreement)
# are deliberately chained with elif/nesting rather than run unconditionally
# one after another: a single bad branch_value has exactly one root cause,
# and it should produce exactly one failure message, not a pile of
# consequential ones (main/master/HEAD is never going to match dev/vX.Y
# either, and comparing a value that already failed the shape check against
# renovate.json adds nothing).

if [ "${branch_count}" -ne 1 ]; then
	fail "expected exactly one 'branch:' line at 6-space indent in the starchart job's with: block (found ${branch_count})"
else
	branch_value="$(sed -E 's/^      branch: //' <<<"${branch_lines}")"
	case "${branch_value}" in
	main | master | HEAD | "")
		fail "branch must not be main/master/HEAD/empty; the reusable workflow refuses these too, but this fails before a run is even needed: got '${branch_value}'"
		;;
	*)
		if ! echo "${branch_value}" | grep -Eq '^dev/v[0-9]+\.[0-9]+$'; then
			fail "branch must match this repo's dev/vX.Y convention: got '${branch_value}'"
		else
			renovate_file="${STARCHART_RENOVATE_JSON:-renovate.json}"
			if [ ! -f "${renovate_file}" ]; then
				fail "expected ${renovate_file} to exist to cross-check the branch value"
			elif ! renovate_branch="$(jq -r '.baseBranchPatterns | if length == 1 then .[0] else error("expected exactly one baseBranchPattern") end' "${renovate_file}" 2>/dev/null)"; then
				fail "${renovate_file}'s baseBranchPatterns must contain exactly one entry"
			elif [ "${branch_value}" != "${renovate_branch}" ]; then
				fail "branch: (${branch_value}) must match ${renovate_file}'s baseBranchPatterns (${renovate_branch}); both roll together at a release cut"
			fi
		fi
		;;
	esac
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} starchart contract check(s) failed" >&2
	exit 1
fi

echo "starchart contract checks passed."
