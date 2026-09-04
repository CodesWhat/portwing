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
# Three rounds of review grew this file from "match a few key lines" to a
# thicket of exact-indent regexes, awk state machines, and allowlists,
# closing one bypass at a time: a decoy in a block scalar, a second job, a
# spelling a tolerant regex didn't happen to cover, a quoted key, a YAML
# anchor. A fourth round found two more: YAML's explicit-key syntax
# (`? "if"` / newline / `: always()`) is invisible to a check that only ever
# read a key's own `key:` line, and a space before a colon
# (`concurrency :`) was invisible to an allowlist extraction anchored on
# `key:` with no space. Each fix closed its own bypass and reopened the
# question of what the next one would be, because every one of those checks
# describes the SHAPE of a violation instead of the shape of compliance --
# and YAML has more ways to spell a given structure than any finite set of
# regexes is going to enumerate.
#
# So this file no longer asks "does anything look wrong". It asks "is this
# byte-for-byte the one file this repo has reviewed", via a golden template:
# the exact expected contents of starchart.yml, embedded below, compared to
# the real file line by line. Three placeholders -- @PIN@, @PIN_COMMENT@, and
# @BRANCH@ -- are the only positions allowed to vary, and each is validated
# against the same rules the old checks used (a 40-hex SHA with a non-empty
# comment; a branch that isn't main/master/HEAD/empty, matches this repo's
# dev/vX.Y convention, and agrees with renovate.json's baseBranchPatterns).
# Every other line, including blank lines, comments, and indentation, must
# match exactly. There is no longer a spelling for continue-on-error, a
# second job, an inline permissions map, an explicit-key if:, or a spaced
# concurrency: to hide behind, because there's no code path left that reads
# "does this look like key X" -- everything that isn't a placeholder is
# compared as inert text.
#
# When the workflow file legitimately changes -- a pin roll, a release cut
# rolling the branch -- the three placeholders absorb it and this contract
# needs no edit. Anything else is a deliberate edit to both files in the same
# PR: change the workflow, then change the template below to match, in the
# same diff a reviewer sees. That review IS the check; the golden template
# just means a change that was never reviewed can't slip past by construction
# instead of by yet another regex someone has to think to write.

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

# --- line endings ------------------------------------------------------------
#
# The template comparison below is a byte-for-byte line match. A trailing \r
# turns every line into a silent non-match instead of a failure with a
# message that says what's wrong, so this is checked first and exits
# immediately rather than let the template comparison report the whole file
# as one giant deviation.

crlf_count="$(tr -cd '\r' <"${workflow}" | wc -c | tr -d '[:space:]')"
if [ "${crlf_count}" -ne 0 ]; then
	fail "workflow file must not contain CRLF line endings (found ${crlf_count} carriage return byte(s)); the template comparison assumes bare LF"
	exit 1
fi

# --- the golden template ------------------------------------------------------

template_file="$(mktemp "${TMPDIR:-/tmp}/starchart-template.XXXXXX")"
trap 'rm -f "${template_file}"' EXIT

cat >"${template_file}" <<'__STARCHART_TEMPLATE_EOF__'
name: Star Chart

# Regenerates the docs/assets/star-history.svg pair from GitHub's own stargazer
# timestamps and commits it back. The chart is a committed artifact rather
# than a live third-party embed on purpose: a stale SVG is visible and a
# missing one is a visibly broken image, where a live route renders a
# plausible card at HTTP 200 forever. It also keeps visitor IPs off a third
# party, which is what the cookieless posture requires.
#
# It refreshes at a release, not on a cron. A committed artifact refreshed on a
# schedule mutates underneath a tag, which is what "main is the released
# version" forbids for exactly this file. Tying it to releases means the chart
# only ever moves when something ships.
#
# The trigger is the v* tag push, and picking it took two wrong answers first.
# They failed for DIFFERENT reasons, which is worth keeping apart: only one of
# them can be fixed by granting something.
#
#   `release: [published]`, which the shared workflow documents, is inert here.
#   release.yml publishes via GoReleaser with GITHUB_TOKEN, and GitHub creates
#   no workflow run for an event emitted with that credential. It would read as
#   correctly wired and silently never run. Nothing grants past this.
#
#   Dispatching from release.yml with RELEASE_PAT returns 403, "Resource not
#   accessible by personal access token". Creating a workflow_dispatch is an
#   Actions API write and RELEASE_PAT carries only Contents RW; contents: write
#   does not imply actions: write. Suppression is NOT the reason here, and
#   GITHUB_TOKEN would have hit the same wall: workflow_dispatch is exempt from
#   the rule above, as is repository_dispatch, so a dispatch needs the Actions
#   scope whichever credential carries it. (Do not read that exemption as "only
#   these two ever run": a GITHUB_TOKEN pull_request also creates a run, but an
#   approval-gated one. The dispatch pair is what fires unattended.)
#
# The tag push works and is already proven in this repo: release-cut.yml pushes
# the v* tag with RELEASE_PAT precisely so downstream workflows fire, and
# release.yml has always been triggered that way. Same event, same credential,
# nothing new to grant. Do not move this back to `release:` or to a dispatch
# without re-reading which credential creates the event.
#
# Firing at the tag does NOT mean the chart inside tag vN is as of vN. The
# refresh commits to the dev branch below, not to the tagged revision, so each
# chart ships with the FOLLOWING release. That is a deliberate one-release lag:
# committing to the release branch mid-cut would be worse. The bound that
# matters is that the artifact never changes underneath a tag already pointing
# at it.
#
# `accent` is this repo's logo colour, #7230d2 from the pigeon neck band, and
# the registry is ops standards/readme-shape.md. It is required upstream with
# no default so that a caller cannot silently inherit another repo's brand
# colour. The dark variant is derived from it rather than passed, so there is
# only one value to keep in sync, and the workflow emits both files: GitHub's
# theme toggle drives a <picture> in the README but not a media query inside an
# <img>-embedded SVG.
#
# `branch` has no default upstream and must be the dev branch: ruleset
# 17620625 requires PRs plus two approvals on main, so a commit-back there
# would be rejected. The reusable workflow rejects main/master before
# checkout rather than dying at the push.
#
# The reusable workflow declares its own harden-runner as its first step, and
# this caller job has no steps of its own, so this repo's egress allow-lists
# do not apply and there is nothing to add here.

on:
  push:
    tags:
      - "v*"
  workflow_dispatch:

permissions: {}

jobs:
  starchart:
    permissions:
      contents: write
    uses: CodesWhat/.github/.github/workflows/starchart-refresh.yml@@PIN@  # @PIN_COMMENT@
    with:
      branch: @BRANCH@
      accent: "#7230d2"
__STARCHART_TEMPLATE_EOF__

# Bash 3.2 -- macOS's shipped /bin/bash -- has neither readarray/mapfile nor
# a way to build an array from a heredoc piped straight into $(...): a
# heredoc body containing an apostrophe breaks that combination's parser
# outright (reproduced locally; not present on the bash 5.x this also runs
# under in CI). A line-at-a-time read loop from a real file is portable to
# both.
template_lines=()
while IFS= read -r line || [ -n "${line}" ]; do
	template_lines+=("${line}")
done <"${template_file}"

actual_lines=()
while IFS= read -r line || [ -n "${line}" ]; do
	actual_lines+=("${line}")
done <"${workflow}"

template_count=${#template_lines[@]}
actual_count=${#actual_lines[@]}

structural_ok=1
pin=""
pin_comment=""
pin_line=""
branch_value=""

uses_prefix="    uses: CodesWhat/.github/.github/workflows/starchart-refresh.yml@"
branch_prefix="      branch: "

i=0
while [ "${i}" -lt "${template_count}" ] && [ "${i}" -lt "${actual_count}" ]; do
	t="${template_lines[$i]}"
	a="${actual_lines[$i]}"
	line_no=$((i + 1))

	case "${t}" in
	*'@PIN@'*)
		if [ "${a#"${uses_prefix}"}" != "${a}" ]; then
			rest="${a#"${uses_prefix}"}"
			if [ "${rest#*"  # "}" != "${rest}" ]; then
				pin="${rest%%"  # "*}"
				pin_comment="${rest#*"  # "}"
			else
				pin="${rest}"
				pin_comment=""
			fi
			pin_line="${a}"
		else
			fail "starchart.yml deviates from the pinned shape at line ${line_no}: expected '${t}', got '${a}'"
			structural_ok=0
		fi
		;;
	*'@BRANCH@'*)
		if [ "${a#"${branch_prefix}"}" != "${a}" ]; then
			branch_value="${a#"${branch_prefix}"}"
		else
			fail "starchart.yml deviates from the pinned shape at line ${line_no}: expected '${t}', got '${a}'"
			structural_ok=0
		fi
		;;
	*)
		if [ "${t}" != "${a}" ]; then
			fail "starchart.yml deviates from the pinned shape at line ${line_no}: expected '${t}', got '${a}'"
			structural_ok=0
		fi
		;;
	esac

	if [ "${structural_ok}" -eq 0 ]; then
		break
	fi
	i=$((i + 1))
done

if [ "${structural_ok}" -eq 1 ] && [ "${template_count}" -ne "${actual_count}" ]; then
	fail "starchart.yml deviates from the pinned shape: expected ${template_count} lines, got ${actual_count}"
	structural_ok=0
fi

# --- the three slots that are allowed to vary --------------------------------
#
# Only reached when every non-placeholder line matched exactly and the line
# counts agree, so pin, pin_comment, and branch_value were captured from the
# real file's own uses: and branch: lines, not left at their empty defaults.

if [ "${structural_ok}" -eq 1 ]; then
	if ! [[ ${pin} =~ ^[0-9a-f]{40}$ ]] || [ -z "$(printf '%s' "${pin_comment}" | tr -d '[:space:]')" ]; then
		fail "the starchart-refresh.yml pin must be a full 40-hex commit SHA followed by a non-empty comment: ${pin_line}"
	fi

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
