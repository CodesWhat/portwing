#!/usr/bin/env bash
set -euo pipefail

# Self-test for scripts/starchart-config-test.sh. Each case breaks the real
# workflow in one specific way and asserts the contract rejects it with the
# message that names that specific breakage. A contract nobody has watched
# fail is a contract nobody knows works.
#
# Several cases plant a decoy: text that LOOKS like the real key somewhere
# the contract must not be reading from (a block scalar body, an unrelated
# job, a different indentation level). Those exist because the contract used
# to match text anywhere in the job block instead of the specific key line at
# its exact indentation, so a decoy satisfied it. Each decoy case must still
# fail, and it must fail with the message for the check the decoy was aimed
# at, not some other check catching it by accident.
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

assert_rejected() {
	local expected="$1"
	local failure_message="$2"
	local output
	local status

	set +e
	output="$(run_contract)"
	status=$?
	set -e

	if [ "${status}" -eq 0 ] || ! grep -Fq "${expected}" <<<"${output}"; then
		echo "FAIL: ${failure_message}" >&2
		echo "--- actual output ---" >&2
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

# --- contents: write scoped to exactly one job, structurally ---------------

# Dropped entirely: the reusable workflow's commit-back step would fail on
# every run with no permission to push. This must fail on the file-wide count
# message specifically (the job-scoping message below is a different check).
reset_fixture
sed -i.bak '/^      contents: write$/d' "${fixture}"
assert_rejected \
	"exactly one line in the file may read '      contents: write' (found 0)" \
	"contract must reject a starchart job missing contents: write, on its own message"

# Granted a second time by another job: this simulates a future job in the
# same file quietly picking up write access it doesn't need.
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
	"exactly one line in the file may read '      contents: write' (found 2)" \
	"contract must reject a second job also granting contents: write"

# Moved, not dropped: the starchart job's own grant is gone but the file-wide
# count still reads 1, because an unrelated job now carries it. Only the
# job-scoped structural check can catch this -- the file-wide count alone
# would report the file as fine.
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
	"contract must reject contents: write moved to an unrelated job, with the job-scoping message"

# permissions: {} on the job, plus a decoy "contents: write" line sitting in
# a block scalar at the exact indentation the real grant would use. The
# file-wide count reads 1 (the decoy), so only the job-scoped structural
# check -- which reads the line immediately after the starchart job's own
# `    permissions:` key -- can catch that the job itself grants nothing.
reset_fixture
sed -i.bak '/^      contents: write$/d' "${fixture}"
sed -i.bak 's/^    permissions:$/    permissions: {}/' "${fixture}"
insert_after_line "  starchart:" \
	'    name: |
      contents: write'
assert_rejected \
	"a grant recorded elsewhere in the file (a different job, or text inside a block scalar) does not satisfy this contract" \
	"contract must reject a decoy contents: write hiding in a block scalar while the job's own grant is permissions: {}"

# --- no unconditional escape hatch ------------------------------------------

reset_fixture
sed -i.bak 's/^    permissions:$/    continue-on-error: true\n    permissions:/' "${fixture}"
assert_rejected \
	"must not carry continue-on-error" \
	"contract must reject a starchart job with continue-on-error"

reset_fixture
sed -i.bak 's/^    permissions:$/    if: always()\n    permissions:/' "${fixture}"
assert_rejected \
	"must not run unconditionally via if: always()" \
	"contract must reject a starchart job gated on if: always()"

reset_fixture
sed -i.bak 's/^    permissions:$/    if: ${{ always() }}\n    permissions:/' "${fixture}"
# shellcheck disable=SC2016 # Literal failure-message text, not a variable expansion.
assert_rejected \
	"must not run unconditionally via if: always()" \
	'contract must reject a starchart job gated on if: ${{ always() }}'

# --- no secrets forwarded ----------------------------------------------------

reset_fixture
sed -i.bak 's/^    permissions:$/    secrets: inherit\n    permissions:/' "${fixture}"
assert_rejected \
	"must not declare secrets:" \
	"contract must reject secrets: inherit on the job"

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
# real line is untouched and correctly anchored); only the file-wide count
# catches the second uses: line the decoy adds.
reset_fixture
insert_after_line "  starchart:" \
	'    name: |
      Refreshes the star chart.
        uses: CodesWhat/.github/.github/workflows/starchart-refresh.yml@main  # decoy'
assert_rejected \
	"no other 'uses:' lines are allowed anywhere in the file, but found 2 total" \
	"contract must reject a decoy uses: line hiding in a block scalar"

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
reset_fixture
sed -i.bak 's/^      branch: dev\/v0\.9$/      branch: main/' "${fixture}"
sed -i.bak 's/^      accent: "#7230d2"$/      accent: |\n        branch: dev\/v0.9/' "${fixture}"
assert_rejected \
	"branch must not be main/master/HEAD/empty" \
	"contract must reject branch: main even with a decoy branch: line inside accent's block scalar"

echo "starchart contract self-tests passed."
