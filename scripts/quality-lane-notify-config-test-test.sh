#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-quality-lane-notify.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${test_root}/scripts"
cp scripts/quality-lane-notify-config-test.sh "${test_root}/scripts/"
fixture="${test_root}/workflow.yml"
rerun_fixture="${test_root}/rerun.yml"

reset_fixture() {
	cp .github/workflows/quality-lane-notify.yml "${fixture}"
}

reset_rerun_fixture() {
	cp .github/workflows/quality-fuzz-monthly-rerun.yml "${rerun_fixture}"
}

assert_passes() {
	local failure_message="$1"
	if ! (cd "${test_root}" && bash scripts/quality-lane-notify-config-test.sh workflow.yml rerun.yml >/dev/null 2>&1); then
		echo "FAIL: ${failure_message}" >&2
		exit 1
	fi
}

assert_rejected() {
	local expected="$1"
	local failure_message="$2"
	local output
	local status

	set +e
	output="$(cd "${test_root}" && bash scripts/quality-lane-notify-config-test.sh workflow.yml rerun.yml 2>&1)"
	status=$?
	set -e

	if [ "${status}" -eq 0 ] || ! grep -Fq "${expected}" <<<"${output}"; then
		echo "FAIL: ${failure_message}" >&2
		echo "--- actual output ---" >&2
		echo "${output}" >&2
		exit 1
	fi
}

# The real workflow must pass its own contract.
reset_fixture
reset_rerun_fixture
assert_passes "the real quality-lane-notify.yml must pass its own contract"

# Missing a required lane from the trigger list.
reset_fixture
sed -i.bak '/      - "Quality: Soak"/d' "${fixture}"
assert_rejected \
	'workflow_run trigger must list "Quality: Soak"' \
	"contract must reject a trigger list missing a required lane"

# An extra, unlisted lane widens the trigger without updating the contract.
reset_fixture
sed -i.bak 's/^    types: \[completed\]$/      - "Quality: Extra Lane"\n&/' "${fixture}"
assert_rejected \
	"workflow_run trigger must list exactly 6 workflows, found 7" \
	"contract must reject a trigger list with an extra, unaccounted-for lane"

# Trigger fires on more than just completed runs.
reset_fixture
sed -i.bak 's/^    types: \[completed\]$/    types: [completed, requested]/' "${fixture}"
assert_rejected \
	"workflow_run trigger must fire only on types: [completed]" \
	"contract must reject a trigger that reacts to more than completed runs"

# Top-level permissions widened beyond the empty set.
reset_fixture
sed -i.bak 's/^permissions: {}$/permissions:\n  contents: read/' "${fixture}"
assert_rejected \
	"top-level permissions must be the empty set" \
	"contract must reject a widened top-level permissions block"

# Job loses issues: write.
reset_fixture
sed -i.bak 's/^      issues: write$/      pull-requests: write/' "${fixture}"
assert_rejected \
	"notify job must grant issues: write" \
	"contract must reject a notify job missing issues: write"

# Job loses actions: read.
reset_fixture
sed -i.bak 's/^      actions: read$/      packages: read/' "${fixture}"
assert_rejected \
	"notify job must grant actions: read" \
	"contract must reject a notify job missing actions: read"

# Job gains a contents permission it doesn't need.
reset_fixture
sed -i.bak 's/^      actions: read$/&\n      contents: read/' "${fixture}"
assert_rejected \
	"notify job must not grant a contents permission" \
	"contract must reject a notify job that grants contents"

# Exclusion condition drops the run_attempt == 1 check, which would also skip
# retried and final-failed monthly fuzz runs forever.
reset_fixture
sed -i.bak 's/run_attempt == 1/run_attempt == 2/' "${fixture}"
assert_rejected \
	"notify job condition must check for run_attempt == 1" \
	"contract must reject an exclusion that doesn't gate on the first attempt"

# Exclusion condition inverted: requires the excluded case instead of skipping it.
reset_fixture
sed -i.bak 's/      !(github.event.workflow_run.name/      (github.event.workflow_run.name/' "${fixture}"
assert_rejected \
	"notify job condition must negate the excluded case, not require it" \
	"contract must reject a condition that runs only for the excluded case"

# Success branch dropped entirely.
reset_fixture
sed -i.bak 's/^          success)$/          not-a-real-conclusion)/' "${fixture}"
assert_rejected \
	"notify step must handle a success conclusion" \
	"contract must reject a workflow missing the success branch"

# Missing the pre-create idempotency search.
reset_fixture
sed -i.bak 's/gh issue list/gh issue search/g' "${fixture}"
assert_rejected \
	"notify step must search for an existing open issue before creating one" \
	"contract must reject a workflow that never searches for an existing issue"

# Rerun lane loses issues: write, so it could no longer file the tracking
# issue for a refusal the notifier structurally cannot see.
reset_fixture
reset_rerun_fixture
sed -i.bak 's/^      issues: write$/      pull-requests: write/' "${rerun_fixture}"
assert_rejected \
	"rerun workflow's triage job must grant issues: write" \
	"contract must reject a rerun workflow missing issues: write"

echo "Quality lane notify contract self-tests passed."
