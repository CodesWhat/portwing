#!/usr/bin/env bash
set -euo pipefail

workflow="${1:-.github/workflows/quality-lane-notify.yml}"

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

if [ ! -f "${workflow}" ]; then
	fail "workflow not found: ${workflow}"
	exit 1
fi

expected_workflows=(
	"Quality: Soak"
	"Quality: Deep Fuzz"
	"Quality: Deep Fuzz (monthly)"
	"Quality: Mutation Testing"
	"Quality: Benchmarks (monthly)"
	"Quality: Deep Fuzz Infra Rerun"
)

# --- trigger list: on.workflow_run.workflows -------------------------------

trigger_workflows="$(
	awk '
		$0 == "on:" { in_on = 1; next }
		in_on && /^  workflow_run:/ { in_run = 1; next }
		in_on && /^jobs:[[:space:]]*$/ { in_on = 0; in_run = 0 }
		in_run && /^  [^[:space:]]/ { in_run = 0 }
		in_run && $0 == "    workflows:" { in_list = 1; next }
		in_run && in_list && /^      - / { print; next }
		in_run && in_list && !/^      - / { in_list = 0 }
	' "${workflow}"
)"

for name in "${expected_workflows[@]}"; do
	grep -Fq "      - \"${name}\"" <<<"${trigger_workflows}" ||
		fail "workflow_run trigger must list \"${name}\""
done

trigger_count="$(grep -c '^      - ' <<<"${trigger_workflows}" || true)"
if [ "${trigger_count}" -ne "${#expected_workflows[@]}" ]; then
	fail "workflow_run trigger must list exactly ${#expected_workflows[@]} workflows, found ${trigger_count}"
fi

grep -Eq '^    types: \[completed\]' "${workflow}" ||
	fail "workflow_run trigger must fire only on types: [completed]"

# --- top-level permissions: {} ----------------------------------------------

grep -Eq '^permissions: \{\}$' "${workflow}" ||
	fail "top-level permissions must be the empty set"

# --- notify job permissions --------------------------------------------------

job_block="$(
	awk '
		$0 == "  notify:" { in_job = 1; next }
		in_job && /^  [^[:space:]]/ { in_job = 0 }
		in_job { print }
	' "${workflow}"
)"

[ -n "${job_block}" ] || fail "expected a top-level 'notify' job"

job_permissions="$(
	awk '
		$0 == "    permissions:" { in_perms = 1; next }
		in_perms && /^    [^[:space:]]/ { in_perms = 0 }
		in_perms { print }
	' <<<"${job_block}"
)"

grep -Fq "      issues: write" <<<"${job_permissions}" ||
	fail "notify job must grant issues: write"
grep -Fq "      actions: read" <<<"${job_permissions}" ||
	fail "notify job must grant actions: read"
grep -Eq '^      contents:' <<<"${job_permissions}" &&
	fail "notify job must not grant a contents permission"

# --- attempt-1 monthly fuzz exclusion ---------------------------------------

job_if="$(
	awk '
		$0 == "  notify:" { in_job = 1; next }
		in_job && /^  [^[:space:]]/ { in_job = 0 }
		in_job && /^    if: >-$/ { in_if = 1; next }
		in_job && in_if && /^    runs-on:/ { in_if = 0 }
		in_job && in_if { print }
	' "${workflow}"
)"

grep -Fq "Quality: Deep Fuzz (monthly)" <<<"${job_if}" ||
	fail "notify job condition must reference the monthly fuzz workflow name"
grep -Fq "conclusion == 'failure'" <<<"${job_if}" ||
	fail "notify job condition must check for a failure conclusion"
grep -Fq "run_attempt == 1" <<<"${job_if}" ||
	fail "notify job condition must check for run_attempt == 1"
grep -Eq '^\s*!\(' <<<"${job_if}" ||
	fail "notify job condition must negate the excluded case, not require it"

# --- failure and success branches -------------------------------------------

# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'case "${CONCLUSION}" in' "${workflow}" ||
	fail "notify step must branch on the run conclusion"
grep -Fq "failure)" "${workflow}" ||
	fail "notify step must handle a failure conclusion"
grep -Fq "success)" "${workflow}" ||
	fail "notify step must handle a success conclusion"
grep -Fq "gh issue create" "${workflow}" ||
	fail "failure branch must be able to create a tracking issue"
grep -Fq "gh issue comment" "${workflow}" ||
	fail "failure branch must be able to comment on an existing tracking issue"
grep -Fq "gh issue close" "${workflow}" ||
	fail "success branch must close the tracking issue"
grep -Fq -- "--label quality-lane" "${workflow}" ||
	fail "created issues must carry the quality-lane label"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq '"[quality] ${WORKFLOW_NAME} is failing"' "${workflow}" ||
	fail "tracking issue title must follow the [quality] <workflow> is failing convention"

# Idempotency: a search for an existing open issue must happen before create.
grep -Fq "gh issue list" "${workflow}" ||
	fail "notify step must search for an existing open issue before creating one"

# --- rerun lane: quality-fuzz-monthly-rerun.yml must also file for refusals -
#
# quality-fuzz-monthly-rerun.yml owns the crash/error/none classification the
# notifier structurally cannot see (no attempt 2 means no completed event for
# it to react to). These assertions lock the step it uses to close that gap:
# it must fire for exactly the three refusal verdicts, the job must be able
# to write the issue, and the title must stay byte-identical to the
# notifier's so the two never open competing issues.

rerun_workflow="${2:-.github/workflows/quality-fuzz-monthly-rerun.yml}"

if [ ! -f "${rerun_workflow}" ]; then
	fail "rerun workflow not found: ${rerun_workflow}"
	exit 1
fi

grep -Fq "      issues: write" "${rerun_workflow}" ||
	fail "rerun workflow's triage job must grant issues: write"

rerun_step="$(
	awk '
		/^      - name: Open or update the quality-lane tracking issue$/ { in_step = 1 }
		in_step { print }
	' "${rerun_workflow}"
)"

[ -n "${rerun_step}" ] ||
	fail "rerun workflow must define the 'Open or update the quality-lane tracking issue' step"

grep -Fq "steps.triage.outputs.kind == 'crash'" <<<"${rerun_step}" ||
	fail "rerun tracking-issue step must fire for kind=crash"
grep -Fq "steps.triage.outputs.kind == 'error'" <<<"${rerun_step}" ||
	fail "rerun tracking-issue step must fire for kind=error"
grep -Fq "steps.triage.outputs.kind == 'none'" <<<"${rerun_step}" ||
	fail "rerun tracking-issue step must fire for kind=none"

# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'TITLE="[quality] ${WORKFLOW_NAME} is failing"' <<<"${rerun_step}" ||
	fail "rerun tracking-issue step title must match the notifier's title literal"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} quality-lane-notify contract check(s) failed" >&2
	exit 1
fi

echo "Quality lane notify contract checks passed."
