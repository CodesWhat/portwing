#!/usr/bin/env bash
set -euo pipefail

# Self-test for scripts/starchart-config-test.sh. Each case breaks the real
# workflow in one specific way and asserts the contract rejects it with the
# message that names that specific breakage. A contract nobody has watched
# fail is a contract nobody knows works.
#
# Several cases plant a decoy: text that LOOKS like the real key somewhere
# the contract must not be reading from (a block scalar body, an unrelated
# job, a different indentation level, a spelling GitHub Actions accepts but
# this file's regexes didn't happen to spell out). Those exist because the
# contract used to match text anywhere in the job block instead of the
# specific key line at its exact indentation -- or only recognized one
# spelling of a key -- so a decoy satisfied it. Each decoy case must still
# fail, and it must fail with the message for the check the decoy was aimed
# at, not some other check catching it by accident.
#
# A second round found the bypass class that survives all of the above: a
# FUTURE JOB. A block-scalar decoy has to fake a real key's exact
# indentation; a second job doesn't -- it's genuinely well-formed YAML that
# a "look inside the starchart job" check never had a reason to visit. Once
# a second job is banned outright (the jobs: block may declare exactly one
# job, and it has to be starchart), most of those mutations legitimately
# trip more than one check at once: the job-count check AND whatever the
# second job's content also happens to violate (an extra permissions:
# occurrence, a second uses: line, ...). That's real defense in depth, not a
# bug, so assert_rejected also asserts how many FAIL lines the run produced
# -- it defaults to 1, and every case that legitimately trips more than one
# check passes its actual count explicitly, so a check silently going quiet
# (dropping the total) or a check silently getting noisier (a check that
# used to be isolated no longer is) both show up as a self-test failure
# instead of a comment nobody re-reads.
#
# A third round added the job and with: key allowlists, the quoted-key ban,
# and the anchor/alias/merge-key ban. Those checks fire ALONGSIDE most of the
# job-scoped decoys and tolerant-spelling mutations above, not instead of
# them: a `name: |` decoy plants a real "name" key at the job's own 4-space
# indent, which the allowlist has never seen before and rejects on its own
# terms, in addition to whatever check the decoy was originally aimed at. So
# this round's work was mostly re-deriving expected_count for mutations that
# already existed, not just adding new ones -- every count below was
# re-verified against the actual contract script, not assumed to carry over.
# A few mutations below exist specifically because a double-quote-tolerant
# regex doesn't recognize a single-quoted key at all: those trip ONLY the new
# quoted-key ban, with a message that names the general shape (an unquoted
# key), not the specific key -- which is the point of an allowlist over a
# blocklist: it doesn't need to recognize a new spelling to reject it.
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

# expected_count defaults to 1: most mutations break exactly one invariant
# and should produce exactly one FAIL: line. A mutation that legitimately
# breaks more than one passes its real count explicitly (see the header
# comment above) -- fail() has a stable "FAIL: " prefix for exactly this,
# so counting is a plain line count, not a guess.
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

# The real workflow must pass its own contract.
reset_fixture
assert_passes "the real starchart.yml must pass its own contract"

# --- line endings ------------------------------------------------------------

reset_fixture
awk '{ printf "%s\r\n", $0 }' "${fixture}" >"${fixture}.crlf" && mv "${fixture}.crlf" "${fixture}"
assert_rejected \
	"workflow file must not contain CRLF line endings" \
	"contract must reject a CRLF-terminated file, and for that specific reason"

# --- top-level permissions --------------------------------------------------

reset_fixture
sed -i.bak 's/^permissions: {}$/permissions:\n  contents: write/' "${fixture}"
assert_rejected \
	"workflow-level permissions must be exactly permissions: {}" \
	"contract must reject a workflow-level permissions block that isn't {}"

# --- triggers ----------------------------------------------------------------

reset_fixture
insert_after_line "  workflow_dispatch:" \
	'  schedule:
    - cron: "0 3 * * *"'
assert_rejected \
	"on: must declare exactly push and workflow_dispatch and nothing else" \
	"contract must reject an extra schedule: trigger"

reset_fixture
insert_after_line '      - "v*"' \
	'    branches:
      - main'
assert_rejected \
	"on.push: must declare only tags: and nothing else" \
	"contract must reject an extra branches: key under push:"

# A column-0 comment planted inside the on: block used to end the awk state
# machine's scan early, so a schedule: key placed after it was silently
# outside the region the key-set check ever looked at.
reset_fixture
insert_after_line "  workflow_dispatch:" \
	'# a column-0 comment
  schedule:
    - cron: "0 3 * * *"'
assert_rejected \
	"on: must declare exactly push and workflow_dispatch and nothing else" \
	"contract must reject a schedule: trigger hidden after a column-0 comment inside on:"

# --- the tag pattern ---------------------------------------------------------

reset_fixture
sed -i.bak 's/^      - "v\*"$/      - "release-*"/' "${fixture}"
assert_rejected \
	'on.push.tags: must be exactly '"'"'      - "v*"'"'" \
	"contract must reject a changed tags: entry"

reset_fixture
sed -i.bak '/^      - "v\*"$/d' "${fixture}"
assert_rejected \
	"on.push.tags: must contain exactly the one entry this file uses today (found 0)" \
	"contract must reject an emptied tags: list"

# --- exactly one job, named starchart ---------------------------------------
#
# A second job doesn't need to fake a key's exact indentation the way a
# block-scalar decoy does -- it's genuinely well-formed YAML. Each of these
# also happens to trip whichever file-wide guard its particular grant
# spelling matches (a step-form uses:, write-all, or the inline {contents:
# write} map), which is why the expected counts below are more than 1: this
# is the job-count check and at least one other independent check both
# correctly firing on the same edit, not double-counting the same problem.
# None of these trip the round-3 job-key allowlist: that allowlist only ever
# reads the starchart job's own keys, and a second job named "extra" or
# "unrelated" sits outside it entirely.

reset_fixture
cat >>"${fixture}" <<'YAML'
  extra:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
YAML
assert_rejected \
	"jobs: must declare exactly one job (found 2)" \
	"contract must reject a second job whose step uses the '- uses:' form" \
	2

reset_fixture
cat >>"${fixture}" <<'YAML'
  extra:
    permissions: write-all
    runs-on: ubuntu-24.04
    steps:
      - run: echo hi
YAML
assert_rejected \
	"jobs: must declare exactly one job (found 2)" \
	"contract must reject a second job granting permissions: write-all" \
	3

reset_fixture
cat >>"${fixture}" <<'YAML'
  extra:
    permissions: {contents: write}
    runs-on: ubuntu-24.04
    steps:
      - run: echo hi
YAML
assert_rejected \
	"jobs: must declare exactly one job (found 2)" \
	"contract must reject a second job granting permissions via an inline {contents: write} map" \
	4

# A 2-space-indented comment planted inside the (sole) job used to end the
# awk state machine's scan early, so a secrets: key placed after it was
# silently outside the region the secrets: check ever looked at, even
# though the job itself is still just one job. That same secrets: line also
# now sits at the job's own 4-space indent, so the round-3 job-key allowlist
# rejects it too -- the awk-scoping fix and the allowlist catch it on two
# independent grounds.
reset_fixture
insert_after_line '      accent: "#7230d2"' \
	'  # a 2-space comment
    secrets: inherit'
assert_rejected \
	"must not declare secrets:" \
	"contract must reject secrets: inherit hidden after a 2-space comment inside the job" \
	2

# --- file-wide bans on alternate permission grants, isolated -----------------
#
# Each of these plants its decoy inside the (sole) job's own name: block
# scalar, with no second job and no "permissions:" prefix where that would
# also trip the permissions: occurrence count. Round 3 added a job-key
# allowlist, and "name" is not on it -- planting the decoy behind a `name: |`
# key means every one of these now also trips the allowlist ("found: name"),
# alongside whichever file-wide guard the decoy's own text was aimed at.

reset_fixture
insert_after_line "  starchart:" \
	'    name: |
      permissions: read'
assert_rejected \
	"the file may declare permissions: exactly twice" \
	"contract must reject a decoy permissions: read line even without write-all or {contents" \
	2

reset_fixture
insert_after_line "  starchart:" \
	'    name: |
      not a real key but write-all appears here'
assert_rejected \
	"the file must not contain write-all anywhere" \
	"contract must reject write-all appearing anywhere in the file, isolated from the other permissions guards" \
	2

reset_fixture
insert_after_line "  starchart:" \
	'    name: |
      freestanding {contents: write} text'
assert_rejected \
	"the file must not contain an inline {contents: ...} permissions map anywhere" \
	"contract must reject {contents appearing anywhere in the file, isolated from the other permissions guards" \
	2

# permissions: { actions: write } avoids the {contents substring (so that
# guard stays quiet) but still opens a non-empty inline map, and it still has
# a "permissions:" prefix, so the occurrence-count guard fires alongside it,
# on top of the job-key allowlist firing on the enclosing name: | decoy.
reset_fixture
insert_after_line "  starchart:" \
	'    name: |
      permissions: { actions: write }'
assert_rejected \
	"the file must not contain a non-empty inline permissions: {...} map anywhere" \
	"contract must reject a non-{contents inline permissions map" \
	3

# --- contents: write scoped to exactly one job, structurally ---------------

# Dropped entirely: the reusable workflow's commit-back step would fail on
# every run with no permission to push. This trips the file-wide count AND
# the job-structural placement check -- deleting the only grant line breaks
# both invariants at once, which is real, not a bug.
reset_fixture
sed -i.bak '/^      contents: write$/d' "${fixture}"
assert_rejected \
	"exactly one line in the file may read '      contents: write' (found 0)" \
	"contract must reject a starchart job missing contents: write, on its own message" \
	2

# Granted a second time by another job: now also banned outright by the
# job-count check, plus the permissions: occurrence count (a third
# permissions: block) and the file-wide contents: write count (now 2).
reset_fixture
cat >>"${fixture}" <<'YAML'
  extra:
    permissions:
      contents: write
    runs-on: ubuntu-24.04
    steps:
      - run: echo hi
YAML
assert_rejected \
	"jobs: must declare exactly one job (found 2)" \
	"contract must reject a second job also granting contents: write" \
	3

# Moved, not dropped: the starchart job's own grant is gone but a second job
# has it instead. The job-scoped structural check on the starchart job still
# fires (its own permissions: block is empty), alongside the job-count and
# permissions: occurrence-count checks that a second job trips on its own.
reset_fixture
sed -i.bak '/^      contents: write$/d' "${fixture}"
cat >>"${fixture}" <<'YAML'
  unrelated:
    permissions:
      contents: write
    runs-on: ubuntu-24.04
    steps:
      - run: echo hi
YAML
assert_rejected \
	"a grant recorded elsewhere in the file (a different job, or text inside a block scalar) does not satisfy this contract" \
	"contract must reject contents: write moved to an unrelated job, with the job-scoping message" \
	3

# permissions: {} on the job, plus a decoy "contents: write" line sitting in
# a block scalar at the exact indentation the real grant would use. The
# file-wide count reads 1 (the decoy), so only the job-scoped structural
# check -- which reads the line immediately after the starchart job's own
# `    permissions:` key -- can catch that the job itself grants nothing.
# The decoy sits behind a `name: |` key, so the job-key allowlist also fires.
reset_fixture
sed -i.bak '/^      contents: write$/d' "${fixture}"
sed -i.bak 's/^    permissions:$/    permissions: {}/' "${fixture}"
insert_after_line "  starchart:" \
	'    name: |
      contents: write'
assert_rejected \
	"a grant recorded elsewhere in the file (a different job, or text inside a block scalar) does not satisfy this contract" \
	"contract must reject a decoy contents: write hiding in a block scalar while the job's own grant is permissions: {}" \
	2

# --- the gap between the grant and whatever comes next ----------------------
#
# A blank line or a comment directly after "      contents: write" used to
# reset the structural check's state machine without checking what's on the
# other side of the gap -- exactly where a second, wider grant would hide.

reset_fixture
sed -i.bak '/^      contents: write$/a\
\
      actions: write' "${fixture}"
assert_rejected \
	"must not be blank or a comment" \
	"contract must reject a blank line directly after the contents: write grant, even with a wider grant hiding behind it"

reset_fixture
sed -i.bak '/^      contents: write$/a\
      # spacer' "${fixture}"
assert_rejected \
	"must not be blank or a comment" \
	"contract must reject a comment directly after the contents: write grant"

# --- no unconditional escape hatch, tolerant of alternate spellings --------
#
# Round 3 added a job-key allowlist that reads the same 4-space-indented job
# keys these mutations plant. Any variant where the key sits at an exact
# 4-space indent with no space before its colon (continue-on-error:, if:,
# secrets:) now also trips the allowlist ("found: <key>"), alongside the
# tolerant-spelling check it originally existed to test -- both correctly
# firing on the same edit. A variant with a space before the colon (if :,
# secrets :) isn't recognized as a key by the allowlist's strict pattern at
# all, so those stay isolated at count 1. A double-quoted key isn't
# recognized as a key by the allowlist either (a quote isn't a valid key
# character), but round 3's quoted-key ban recognizes it as a quoted line
# and fires instead -- so those move from isolated to a 2-count pair as well,
# just via the ban rather than the allowlist.

reset_fixture
sed -i.bak 's/^    permissions:$/    continue-on-error: true\n    permissions:/' "${fixture}"
assert_rejected \
	"must not carry continue-on-error" \
	"contract must reject a starchart job with continue-on-error" \
	2

reset_fixture
sed -i.bak 's/^    permissions:$/    if: always()\n    permissions:/' "${fixture}"
assert_rejected \
	"must not run unconditionally via if: always()" \
	"contract must reject a starchart job gated on if: always()" \
	2

reset_fixture
sed -i.bak 's/^    permissions:$/    if: ${{ always() }}\n    permissions:/' "${fixture}"
# shellcheck disable=SC2016 # Literal failure-message text, not a variable expansion.
assert_rejected \
	"must not run unconditionally via if: always()" \
	'contract must reject a starchart job gated on if: ${{ always() }}' \
	2

reset_fixture
sed -i.bak 's/^    permissions:$/    "if": always()\n    permissions:/' "${fixture}"
assert_rejected \
	"must not run unconditionally via if: always()" \
	'contract must reject a double-quoted "if": key gated on always()' \
	2

reset_fixture
sed -i.bak 's/^    permissions:$/    if : always()\n    permissions:/' "${fixture}"
assert_rejected \
	"must not run unconditionally via if: always()" \
	"contract must reject if : (a space before the colon) gated on always()"

reset_fixture
sed -i.bak 's/^    permissions:$/    if: always( )\n    permissions:/' "${fixture}"
assert_rejected \
	"must not run unconditionally via if: always()" \
	"contract must reject always( ) with a space inside the parens" \
	2

reset_fixture
sed -i.bak 's/^    permissions:$/    secrets: inherit\n    permissions:/' "${fixture}"
assert_rejected \
	"must not declare secrets:" \
	"contract must reject secrets: inherit on the job" \
	2

reset_fixture
sed -i.bak 's/^    permissions:$/    "secrets": inherit\n    permissions:/' "${fixture}"
assert_rejected \
	"must not declare secrets:" \
	'contract must reject a double-quoted "secrets": key' \
	2

reset_fixture
sed -i.bak 's/^    permissions:$/    secrets : inherit\n    permissions:/' "${fixture}"
assert_rejected \
	"must not declare secrets:" \
	"contract must reject secrets : (a space before the colon)"

# --- job key allowlist --------------------------------------------------------
#
# concurrency: naming the reusable workflow's own group, with
# cancel-in-progress: true, could cancel the write path out from under it --
# a check that never had a reason to read job-level keys other than
# permissions:, uses:, secrets:, if:, and continue-on-error: would never have
# caught it. The allowlist rejects any key on the job that isn't one of the
# three it's actually meant to declare.

reset_fixture
insert_after_line "  starchart:" \
	'    concurrency:
      group: starchart
      cancel-in-progress: true'
assert_rejected \
	"starchart job may only declare permissions, uses and with (found: concurrency)" \
	"contract must reject a concurrency: block on the starchart job"

# --- with: key allowlist, and the accent value pin --------------------------
#
# An extra with: input (output-path:, max-pages:, ...) could redirect what
# the reusable workflow writes, or change how much it reads, without the
# branch: value check ever noticing -- that check only ever read branch:'s
# own value, not with:'s full key set. accent:'s value is pinned the same
# way the tag pattern and the reusable-workflow pin are: this file has one
# accent colour today, and a caller that changes it is a decision this
# contract should see.

reset_fixture
insert_after_line '      accent: "#7230d2"' '      output-path: docs/star-history.svg'
assert_rejected \
	"with: may only pass branch and accent (found: output-path)" \
	"contract must reject an extra output-path: input under with:"

reset_fixture
insert_after_line '      accent: "#7230d2"' '      max-pages: 5'
assert_rejected \
	"with: may only pass branch and accent (found: max-pages)" \
	"contract must reject an extra max-pages: input under with:"

reset_fixture
sed -i.bak 's/^      accent: "#7230d2"$/      accent: "#000000"/' "${fixture}"
assert_rejected \
	'with.accent: must be exactly '"'"'"#7230d2"'"'" \
	"contract must reject a changed accent: value"

# --- no quoted keys, anywhere -------------------------------------------------
#
# Every tolerant if:/secrets: regex above grew a double-quote alternative
# because a decoy used a double-quoted key; a single-quoted 'secrets':
# inherit or 'if': always() sails straight past all of them, since none of
# those regexes' "?...?" alternatives spell single quotes. The quoted-key ban
# doesn't need to: it fires on ANY quote as the first non-whitespace
# character on a line, so these mutations trip only the ban, with its
# general "keys must be unquoted" message, not the specific if:/secrets:
# message the equivalent double-quoted mutations above produce.

reset_fixture
sed -i.bak "s/^    permissions:\$/    'secrets': inherit\n    permissions:/" "${fixture}"
assert_rejected \
	"keys must be unquoted" \
	"contract must reject a single-quoted 'secrets': key, which no tolerant regex recognizes"

reset_fixture
sed -i.bak "s/^    permissions:\$/    'if': always()\n    permissions:/" "${fixture}"
assert_rejected \
	"keys must be unquoted" \
	"contract must reject a single-quoted 'if': key gated on always(), which no tolerant regex recognizes"

# A quoted "branch": key isn't recognized by with:'s key allowlist either (a
# quote isn't a valid key character in that extraction), so the with: block
# now reads as having no branch: line at all -- both the quoted-key ban and
# the "expected exactly one branch:" check fire on this single edit.
reset_fixture
sed -i.bak 's/^      branch: dev\/v0\.9$/      "branch": dev\/v0.9/' "${fixture}"
assert_rejected \
	"keys must be unquoted" \
	'contract must reject a double-quoted "branch": key under with:, which the key allowlist can no longer even see as branch:' \
	2

# --- no YAML anchors, aliases, or merge keys, anywhere -----------------------
#
# An alias never puts the text it resolves to on its own line, so no regex
# that reads if:'s own line text (always(), in any spelling) can see past
# `if: *unconditional` pointing at an anchor defined anywhere else in the
# file. The anchor here is planted on a new run-name: key, deliberately far
# from the if: line that aliases it. That if: also sits at the job's own
# 4-space indent, so the job-key allowlist fires on it too ("found: if"),
# alongside the anchor ban firing twice -- once for the anchor's own
# definition, once for the alias that resolves it.

reset_fixture
insert_after_line "name: Star Chart" 'run-name: &unconditional always()'
insert_after_line "  starchart:" '    if: *unconditional'
assert_rejected \
	"YAML anchors, aliases and merge keys are not allowed" \
	"contract must reject if: *unconditional aliasing an always() anchor defined on run-name:" \
	3

# A merge key (<<: *base) splices an anchored mapping's keys into the job
# using it, which would let one anchored mapping inject keys past the job-key
# allowlist without the allowlist ever seeing them arrive on their own line.
# The merge key itself is banned outright, alongside the anchor definition it
# points at.
reset_fixture
insert_after_line "name: Star Chart" \
	'base: &base
  runs-on: ubuntu-24.04'
insert_after_line "  starchart:" '    <<: *base'
assert_rejected \
	"YAML anchors, aliases and merge keys are not allowed" \
	"contract must reject a merge key (<<: *base) splicing an anchored mapping into the job" \
	2

# --- the reusable workflow pin ----------------------------------------------

# Pinned to a branch instead of a commit SHA: exactly the drift the pin
# exists to prevent, since the write path this delegates to could then
# change behaviour with no commit landing in this repo.
reset_fixture
sed -i.bak \
	's|starchart-refresh\.yml@[0-9a-f]\{40\}|starchart-refresh.yml@main|' \
	"${fixture}"
assert_rejected \
	"must be a full 40-hex commit SHA followed by a non-empty comment" \
	"contract must reject the reusable workflow pinned to a branch"

# SHA present but the trailing comment recording why/when is gone.
reset_fixture
sed -i.bak \
	's|\(starchart-refresh\.yml@[0-9a-f]\{40\}\)  # main, 2026-08-21|\1|' \
	"${fixture}"
assert_rejected \
	"must be a full 40-hex commit SHA followed by a non-empty comment" \
	"contract must reject a SHA pin with no trailing comment"

# Comment marker present but empty: closes the gap where a bare "#" with no
# text after it would have satisfied a looser "has a comment" check.
reset_fixture
sed -i.bak \
	's|\(starchart-refresh\.yml@[0-9a-f]\{40\}\)  # main, 2026-08-21|\1  #|' \
	"${fixture}"
assert_rejected \
	"must be a full 40-hex commit SHA followed by a non-empty comment" \
	"contract must reject a SHA pin with an empty trailing comment"

# uses: line deleted outright.
reset_fixture
sed -i.bak '/^    uses:/d' "${fixture}"
assert_rejected \
	"expected exactly one 'uses:' line at 4-space indent inside the starchart job (found 0)" \
	"contract must reject a starchart job with no uses: line"

# A decoy uses:...@main line hiding in a block scalar alongside the real,
# correctly-pinned uses: line. The job-scoped check alone would pass (the
# real line is untouched and correctly anchored); the file-wide count catches
# the second uses: line the decoy adds, and the decoy sits behind a
# `name: |` key, so the job-key allowlist fires too.
reset_fixture
insert_after_line "  starchart:" \
	'    name: |
      Refreshes the star chart.
        uses: CodesWhat/.github/.github/workflows/starchart-refresh.yml@main  # decoy'
assert_rejected \
	"no other 'uses:' lines are allowed anywhere in the file, but found 2 total" \
	"contract must reject a decoy uses: line hiding in a block scalar" \
	2

# --- the target branch -------------------------------------------------------

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

reset_fixture
sed -i.bak '/^      branch: dev\/v0\.9$/d' "${fixture}"
assert_rejected \
	"expected exactly one 'branch:' line at 6-space indent in the starchart job's with: block (found 0)" \
	"contract must reject a with: block with no branch: line"

# A decoy branch: line at a deeper indent than the real key, inside a
# multi-line accent: block scalar, while the real branch: is set to the
# forbidden value "main". The contract must still catch the real problem
# instead of reading the decoy (or getting confused into reporting neither).
# Turning accent: into a block scalar also breaks the round-3 accent-value
# pin, since the block-scalar marker itself is not the pinned value -- so
# this now legitimately trips two independent checks.
reset_fixture
sed -i.bak 's/^      branch: dev\/v0\.9$/      branch: main/' "${fixture}"
sed -i.bak 's/^      accent: "#7230d2"$/      accent: |\n        branch: dev\/v0.9/' "${fixture}"
assert_rejected \
	"branch must not be main/master/HEAD/empty" \
	"contract must reject branch: main even with a decoy branch: line inside accent's block scalar" \
	2

echo "starchart contract self-tests passed."
