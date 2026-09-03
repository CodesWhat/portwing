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
	"workflow_run trigger must list exactly 9 workflows, found 10" \
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

# Concurrency group keyed on the run id instead of the source workflow id:
# two runs of the same lane (manual dispatch + schedule) would no longer
# serialize.
reset_fixture
sed -i.bak \
	's/group: quality-lane-notify-\${{ github\.event\.workflow_run\.workflow_id }}/group: quality-lane-notify-${{ github.event.workflow_run.id }}/' \
	"${fixture}"
assert_rejected \
	"concurrency group must key on the source workflow_id, not the run id or name" \
	"contract must reject a concurrency group keyed on the run id"

# Concurrency set to cancel-in-progress, which would kill a decision that
# already called gh issue create/close.
reset_fixture
sed -i.bak 's/^  cancel-in-progress: false$/  cancel-in-progress: true/' "${fixture}"
assert_rejected \
	"concurrency must not cancel an in-progress notification" \
	"contract must reject cancel-in-progress: true"

# Job loses issues: write.
reset_fixture
sed -i.bak 's/^      issues: write$/      pull-requests: write/' "${fixture}"
assert_rejected \
	"notify job permissions must be exactly: issues: write, actions: read" \
	"contract must reject a notify job missing issues: write"

# Job loses actions: read.
reset_fixture
sed -i.bak 's/^      actions: read$/      packages: read/' "${fixture}"
assert_rejected \
	"notify job permissions must be exactly: issues: write, actions: read" \
	"contract must reject a notify job missing actions: read"

# Job gains a contents permission it doesn't need.
reset_fixture
sed -i.bak 's/^      actions: read$/&\n      contents: read/' "${fixture}"
assert_rejected \
	"notify job permissions must be exactly: issues: write, actions: read" \
	"contract must reject a notify job that grants contents"

# Job gains id-token: write it has no business asking for.
reset_fixture
sed -i.bak 's/^      actions: read$/&\n      id-token: write/' "${fixture}"
assert_rejected \
	"notify job permissions must be exactly: issues: write, actions: read" \
	"contract must reject a notify job that grants id-token: write"

# Exclusion condition drops the run_attempt == 1 check, which would also skip
# retried and final-failed monthly fuzz runs forever.
reset_fixture
sed -i.bak 's/run_attempt == 1/run_attempt == 2/' "${fixture}"
assert_rejected \
	"notify job condition must be exactly:" \
	"contract must reject an exclusion that doesn't gate on the first attempt"

# Exclusion condition inverted: requires the excluded case instead of skipping it.
reset_fixture
sed -i.bak 's/      !(github.event.workflow_run.name/      (github.event.workflow_run.name/' "${fixture}"
assert_rejected \
	"notify job condition must be exactly:" \
	"contract must reject a condition that runs only for the excluded case"

# Exclusion condition weakened from && to ||: independent substring checks on
# each clause wouldn't notice, since all three clauses are still present.
reset_fixture
sed -i.bak "s/github.event.workflow_run.conclusion == 'failure' &&/github.event.workflow_run.conclusion == 'failure' ||/" "${fixture}"
assert_rejected \
	"notify job condition must be exactly:" \
	"contract must reject an exclusion that ORs its clauses instead of ANDing them"

# The pull_request exclusion dropped: a green PR run of the engine-matrix lane
# would close a tracking issue the weekly run is still legitimately failing on.
reset_fixture
sed -i.bak "/github.event.workflow_run.event != 'pull_request' &&/d" "${fixture}"
assert_rejected \
	"notify job condition must be exactly:" \
	"contract must reject a condition that lets pull_request runs drive the issue state machine"

# The exclusion narrowed to an allow-list of the two scheduled triggers, which
# reads equivalent and silently drops quality-fuzz-monthly-rerun.yml, whose own
# events arrive as workflow_run.
reset_fixture
sed -i.bak \
	-e "s/      github.event.workflow_run.event != 'pull_request' &&/      (github.event.workflow_run.event == 'schedule' ||/" \
	-e "s/      github.event.workflow_run.event != 'push' &&/       github.event.workflow_run.event == 'workflow_dispatch') \&\&/" \
	"${fixture}"
assert_rejected \
	"notify job condition must be exactly:" \
	"contract must reject an allow-list that drops the rerun lane's workflow_run events"

# Success branch dropped entirely.
reset_fixture
sed -i.bak 's/^          success)$/          not-a-real-conclusion)/' "${fixture}"
assert_rejected \
	"notify step must handle a success conclusion" \
	"contract must reject a workflow missing the success branch"

# Case arms swapped: the failure) label now guards the close body and the
# success) label guards the create/comment body. A file-wide substring check
# for "gh issue create" would still pass; the scoped per-arm check must not.
reset_fixture
sed -i.bak \
	-e 's/^          failure)$/          __TMP_ARM__/' \
	-e 's/^          success)$/          failure)/' \
	-e 's/^          __TMP_ARM__$/          success)/' \
	"${fixture}"
assert_rejected \
	"failure branch must be able to create a tracking issue" \
	"contract must reject swapped failure/success case arm bodies"

# --label dropped from the gh issue create invocation: newly-opened issues
# would no longer be findable by find_open_issue on the next run.
reset_fixture
sed -i.bak '/--label quality-lane \\$/d' "${fixture}"
assert_rejected \
	"the gh issue create invocation must carry the quality-lane label" \
	"contract must reject a gh issue create call missing --label quality-lane"

# types: [completed] moved off workflow_run onto a decoy trigger: the
# unscoped version of this check would still find the string in the file.
reset_fixture
sed -i.bak \
	-e '/^    types: \[completed\]$/d' \
	-e '/^on:$/a\
  push:\
    types: [completed]
' \
	"${fixture}"
assert_rejected \
	"workflow_run trigger must fire only on types: [completed]" \
	"contract must reject types: [completed] moved onto a decoy trigger"

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
