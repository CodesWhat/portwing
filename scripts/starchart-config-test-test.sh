#!/usr/bin/env bash
set -euo pipefail

# Self-test for scripts/starchart-config-test.sh. Each case breaks the real
# workflow in one specific way and asserts the contract rejects it with the
# message that names that specific breakage. A contract nobody has watched
# fail is a contract nobody knows works.
#
# Three rounds of review turned scripts/starchart-config-test.sh from "match
# a few key lines" into a thicket of exact-indent regexes, awk state
# machines, and allowlists, and this file grew a mutation for every bypass
# each round closed: a decoy in a block scalar, a second job, a spelling a
# tolerant regex didn't cover, a quoted key, a YAML anchor. A fourth round
# found two more the allowlists still missed (YAML's explicit-key syntax,
# and a space before a colon), which is what finally moved the contract to a
# golden template instead of one more check: the exact expected contents of
# starchart.yml, embedded in the contract script, compared to the real file
# line by line. Under that model, almost all of those old mutations now
# exercise the exact same mechanism -- "an extra or changed line fails
# structurally, whatever it says" -- and produce the literal same message,
# because the contract no longer looks at what a line SAYS, only whether it
# matches. Where several old mutations now collapse to one indistinguishable
# assertion (a decoy hiding in a block scalar, continue-on-error, an
# if: always() in any of six spellings, a second job in any of three
# permission-grant forms), this file keeps one representative case and says
# so in a comment, rather than asserting the same string ten times over.
#
# The three slots -- @PIN@, @PIN_COMMENT@, @BRANCH@ -- are the only positions
# the template lets vary, and they keep their own specific messages: a
# malformed pin is still "must be a full 40-hex commit SHA...", a bad branch
# is still "must not be main/master/HEAD/empty" or "must match this repo's
# dev/vX.Y convention" or the renovate.json cross-check, not a generic
# deviation. Deleting the branch: line entirely, though, is no longer a
# slot mutation: with no with:-block key check left to notice a missing key,
# a deleted branch: line just shifts every line after it out of position,
# which the structural comparison catches on its own terms.
#
# The contract cross-checks branch: against renovate.json's baseBranchPatterns,
# so this fixture tree carries its own copy to stay hermetic -- no reading the
# real repo's renovate.json from a temp directory that isn't it.

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-starchart.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${test_root}/scripts"
cp scripts/starchart-config-test.sh "${test_root}/scripts/"
cp renovate.json "${test_root}/renovate.json"
fixture="${test_root}/workflow.yml"

reset_fixture() {
	cp .github/workflows/starchart.yml "${fixture}"
}

run_contract() {
	(cd "${test_root}" && bash scripts/starchart-config-test.sh workflow.yml 2>&1)
}

assert_passes() {
	local failure_message="$1"
	if ! run_contract >/dev/null 2>&1; then
		echo "FAIL: ${failure_message}" >&2
		echo "--- actual output ---" >&2
		run_contract >&2 || true
		exit 1
	fi
}

# The golden template compares line by line and stops at the first mismatch
# (see the contract's own comments), so a single mutation produces exactly
# one FAIL: line -- there is no longer a defense-in-depth case where two
# independent checks legitimately fire on the same edit, the way there was
# when the contract was a pile of separate regexes. expected_count stays
# here, defaulting to 1, purely as a tripwire: if a future edit to the
# contract ever makes one mutation trip two checks again, this catches that
# regression instead of silently accepting a noisier failure.
assert_rejected() {
	local expected="$1"
	local failure_message="$2"
	local expected_count="${3:-1}"
	local output
	local status
	local actual_count

	set +e
	output="$(run_contract)"
	status=$?
	set -e

	actual_count="$(grep -c '^FAIL: ' <<<"${output}" || true)"

	if [ "${status}" -eq 0 ] || ! grep -Fq "${expected}" <<<"${output}" || [ "${actual_count}" -ne "${expected_count}" ]; then
		echo "FAIL: ${failure_message}" >&2
		echo "--- actual output (expected ${expected_count} FAIL: line(s), got ${actual_count}) ---" >&2
		echo "${output}" >&2
		exit 1
	fi
}

# Insert TEXT (may contain embedded newlines) immediately after the first
# line that equals PATTERN exactly. Plain bash, no awk -v embedded-newline
# quirks and no BSD-vs-GNU sed 'a\' syntax differences to paper over.
insert_after_line() {
	local pattern="$1"
	local text="$2"
	local tmp="${fixture}.tmp"

	: >"${tmp}"
	while IFS= read -r line || [ -n "${line}" ]; do
		printf '%s\n' "${line}" >>"${tmp}"
		if [ "${line}" = "${pattern}" ]; then
			printf '%s\n' "${text}" >>"${tmp}"
		fi
	done <"${fixture}"
	mv "${tmp}" "${fixture}"
}

# The real workflow must pass its own contract. This is also this file's
# "byte-identical copy must pass" case: reset_fixture is a byte-for-byte
# copy of the real file, so this already exercises that path directly,
# without a second, redundant copy-and-run.
reset_fixture
assert_passes "the real starchart.yml must pass its own contract"

# --- line endings ------------------------------------------------------------

reset_fixture
awk '{ printf "%s\r\n", $0 }' "${fixture}" >"${fixture}.crlf" && mv "${fixture}.crlf" "${fixture}"
assert_rejected \
	"workflow file must not contain CRLF line endings" \
	"contract must reject a CRLF-terminated file, and for that specific reason"

# --- the structural comparison: exact text, exact position, exact count -----

# A single trailing space on an otherwise-untouched line is still a mismatch;
# the comparison is byte-for-byte, not whitespace-tolerant.
reset_fixture
sed -i.bak 's/^permissions: {}$/permissions: {} /' "${fixture}"
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 70: expected 'permissions: {}', got 'permissions: {} '" \
	"contract must reject a single trailing space on an otherwise-correct line"

# A changed comment line fails just as hard as a changed key: nothing in the
# template is exempt from the comparison, including prose that was never a
# security boundary before this round.
reset_fixture
sed -i.bak 's/^name: Star Chart$/name: Star Charts/' "${fixture}"
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 1: expected 'name: Star Chart', got 'name: Star Charts'" \
	"contract must reject a changed comment/title line"

# A changed trigger, a changed tag pattern, and a deleted tag entry are all
# just changed or shifted lines now -- one representative of each shape
# (content change, and content change from a deletion shifting the next line
# into view) is enough; the mechanism is identical either way.
reset_fixture
sed -i.bak 's/^      - "v\*"$/      - "release-*"/' "${fixture}"
assert_rejected \
	'starchart.yml deviates from the pinned shape at line 67' \
	"contract must reject a changed tags: entry"

reset_fixture
sed -i.bak '/^      - "v\*"$/d' "${fixture}"
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 67: expected '      - \"v*\"', got '  workflow_dispatch:'" \
	"contract must reject a deleted tags: entry, caught by the next real line shifting into its place"

# An inserted line -- an extra trigger key, a second job, a decoy hiding in a
# block scalar, continue-on-error, an if: always() in any spelling, a
# secrets: in any spelling -- is the same mechanism from here on: whatever
# gets inserted is either a mismatch against the template line it displaces,
# or (inserted at the very end) a bare line-count mismatch. One of each shape
# stands in for what used to be a dozen near-identical assertions.
reset_fixture
insert_after_line "  workflow_dispatch:" '  schedule:'
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 69: expected '', got '  schedule:'" \
	"contract must reject an extra schedule: trigger inserted into the on: block"

reset_fixture
sed -i.bak 's/^    permissions:$/    continue-on-error: true\n    permissions:/' "${fixture}"
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 74: expected '    permissions:', got '    continue-on-error: true'" \
	"contract must reject continue-on-error inserted before the job's permissions: block"

reset_fixture
sed -i.bak 's/^    permissions:$/    secrets: inherit\n    permissions:/' "${fixture}"
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 74: expected '    permissions:', got '    secrets: inherit'" \
	"contract must reject secrets: inherit inserted before the job's permissions: block"

reset_fixture
cat >>"${fixture}" <<'YAML'
  extra:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
YAML
assert_rejected \
	"starchart.yml deviates from the pinned shape: expected 79 lines, got 83" \
	"contract must reject a second job appended after the sole job -- caught purely on line count, since nothing in the template read past line 79 before"

reset_fixture
printf '\n' >>"${fixture}"
assert_rejected \
	"starchart.yml deviates from the pinned shape: expected 79 lines, got 80" \
	"contract must reject a single extra trailing blank line, on line count alone"

# --- the class round 4 found: a quoted key, an anchor, an explicit key, a --
# --- spaced colon -- all now caught the same way, because none of them can -
# --- appear without changing SOME line's text, and the template doesn't ----
# --- care what changed, only that something did ----------------------------

reset_fixture
sed -i.bak "s/^    permissions:\$/    'secrets': inherit\n    permissions:/" "${fixture}"
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 74: expected '    permissions:', got '    'secrets': inherit'" \
	"contract must reject a single-quoted 'secrets': key, which no tolerant regex ever needed to recognize by name"

reset_fixture
insert_after_line "name: Star Chart" 'run-name: &unconditional always()'
insert_after_line "  starchart:" '    if: *unconditional'
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 2: expected '', got 'run-name: &unconditional always()'" \
	"contract must reject a YAML anchor/alias pair (if: *unconditional resolving an always() anchor on run-name:), caught at the anchor's own line before the alias is even reached"

reset_fixture
insert_after_line "  starchart:" \
	'    ? secrets
    : inherit'
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 74: expected '    permissions:', got '    ? secrets'" \
	"contract must reject YAML's explicit-key mapping syntax (? secrets / : inherit), which no key: line regex could ever see"

reset_fixture
insert_after_line "  starchart:" '    concurrency : starchart'
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 74: expected '    permissions:', got '    concurrency : starchart'" \
	"contract must reject a spaced colon (concurrency :), which no key-with-no-space-before-the-colon allowlist could ever recognize as a key"

# --- the three slots: pin and pin comment -----------------------------------

reset_fixture
sed -i.bak \
	's|starchart-refresh\.yml@[0-9a-f]\{40\}|starchart-refresh.yml@main|' \
	"${fixture}"
assert_rejected \
	"must be a full 40-hex commit SHA followed by a non-empty comment" \
	"contract must reject the reusable workflow pinned to a branch"

reset_fixture
sed -i.bak \
	's|\(starchart-refresh\.yml@[0-9a-f]\{40\}\)  # main, 2026-08-21|\1|' \
	"${fixture}"
assert_rejected \
	"must be a full 40-hex commit SHA followed by a non-empty comment" \
	"contract must reject a SHA pin with no trailing comment"

reset_fixture
sed -i.bak \
	's|\(starchart-refresh\.yml@[0-9a-f]\{40\}\)  # main, 2026-08-21|\1  #|' \
	"${fixture}"
assert_rejected \
	"must be a full 40-hex commit SHA followed by a non-empty comment" \
	"contract must reject a SHA pin with an empty trailing comment"

# uses: line deleted outright: the following with: line shifts into its
# place, so this is caught structurally, at the uses: line's own position.
reset_fixture
sed -i.bak '/^    uses:/d' "${fixture}"
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 76: expected '    uses: CodesWhat/.github/.github/workflows/starchart-refresh.yml@@PIN@  # @PIN_COMMENT@', got '    with:'" \
	"contract must reject a starchart job with no uses: line"

# --- the third slot: branch -------------------------------------------------

reset_fixture
sed -i.bak 's/^      branch: dev\/v0\.9$/      branch: main/' "${fixture}"
assert_rejected \
	"branch must not be main/master/HEAD/empty" \
	"contract must reject branch: main, which the reusable workflow also refuses at run time"

reset_fixture
sed -i.bak 's/^      branch: dev\/v0\.9$/      branch: some-feature-branch/' "${fixture}"
assert_rejected \
	"branch must match this repo's dev/vX.Y convention" \
	"contract must reject a branch that isn't this repo's dev/vX.Y"

reset_fixture
sed -i.bak 's/^      branch: dev\/v0\.9$/      branch: dev\/v9.9/' "${fixture}"
assert_rejected \
	"must match renovate.json's baseBranchPatterns" \
	"contract must reject a branch: that is shaped like dev/vX.Y but disagrees with renovate.json"

# Deleted outright: no longer a slot mutation. There is no with:-key check
# left to notice a missing branch: key on its own terms -- the accent: line
# shifts up into the branch: line's position, and the structural comparison
# catches that shift the same way it catches any other deleted line.
reset_fixture
sed -i.bak '/^      branch: dev\/v0\.9$/d' "${fixture}"
assert_rejected \
	"starchart.yml deviates from the pinned shape at line 78: expected '      branch: @BRANCH@', got '      accent: \"#7230d2\"'" \
	"contract must reject a with: block with no branch: line, caught by the accent: line shifting into its place"

# accent:'s value has no placeholder -- it's pinned outright, the same as
# every other non-slot line -- so changing it is an ordinary structural
# deviation at its own line, not a slot check.
reset_fixture
sed -i.bak 's/^      accent: "#7230d2"$/      accent: "#000000"/' "${fixture}"
assert_rejected \
	'starchart.yml deviates from the pinned shape at line 79' \
	"contract must reject a changed accent: value"

echo "starchart contract self-tests passed."
