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

# The whole `workflow_run:` block, scoped from its own key to the next
# top-level key under `on:` (i.e. any line back at the 2-space indent). Every
# assertion about what the trigger does or doesn't fire on reads from this,
# not the whole file, so a decoy trigger elsewhere in `on:` can't satisfy a
# check that's actually about workflow_run.
workflow_run_block="$(
	awk '
        /^  workflow_run:/ { in_block = 1; next }
        in_block && /^  [^[:space:]]/ { in_block = 0 }
        in_block { print }
    ' "${workflow}"
)"

[ -n "${workflow_run_block}" ] || fail "expected an on.workflow_run trigger"

trigger_workflows="$(
	awk '
        $0 == "    workflows:" { in_list = 1; next }
        in_list && /^      - / { print; next }
        in_list && !/^      - / { in_list = 0 }
    ' <<<"${workflow_run_block}"
)"

for name in "${expected_workflows[@]}"; do
	grep -Fq "      - \"${name}\"" <<<"${trigger_workflows}" ||
		fail "workflow_run trigger must list \"${name}\""
done

trigger_count="$(grep -c '^      - ' <<<"${trigger_workflows}" || true)"
if [ "${trigger_count}" -ne "${#expected_workflows[@]}" ]; then
	fail "workflow_run trigger must list exactly ${#expected_workflows[@]} workflows, found ${trigger_count}"
fi

# Scoped to the workflow_run block itself: a `types: [completed]` that got
# moved onto some other trigger (a decoy `push:`/`pull_request:` block added
# alongside workflow_run) must not satisfy this. It has to gate the trigger
# that actually fires this job.
grep -Eq '^    types: \[completed\]$' <<<"${workflow_run_block}" ||
	fail "workflow_run trigger must fire only on types: [completed]"

# --- top-level permissions: {} ----------------------------------------------

grep -Eq '^permissions: \{\}$' "${workflow}" ||
	fail "top-level permissions must be the empty set"

# --- concurrency: keyed on the source workflow, queued not cancelled -------
#
# Keying on the run id (or leaving cancel-in-progress unset/true) lets two
# runs of the SAME lane's workflow race their find_open_issue calls, which
# quality-fuzz-monthly.yml explicitly allows to happen (manual dispatch +
# schedule). Keying on workflow_id and queueing serializes them instead.

# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'group: quality-lane-notify-${{ github.event.workflow_run.workflow_id }}' "${workflow}" ||
	fail "concurrency group must key on the source workflow_id, not the run id or name"
grep -Eq '^  cancel-in-progress: false$' "${workflow}" ||
	fail "concurrency must not cancel an in-progress notification"

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

# Exact block match, not "has these two and lacks contents": a widened grant
# (e.g. id-token: write tacked on for some unrelated reason) is just as much
# a violation of least privilege as a missing one, and neither is caught by
# substring presence checks alone.
job_permissions_trimmed="$(grep -v '^[[:space:]]*$' <<<"${job_permissions}")"
expected_job_permissions=$'      issues: write\n      actions: read'
if [ "${job_permissions_trimmed}" != "${expected_job_permissions}" ]; then
	fail "notify job permissions must be exactly: issues: write, actions: read (found: $(tr '\n' ';' <<<"${job_permissions_trimmed}"))"
fi

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

# The exact predicate, normalized to one line (trim each line, collapse
# runs of whitespace, join with single spaces), not four independent
# substring greps. A substring check can't tell `&&` from `||`, or notice a
# stray clause; string equality against the whole predicate can.
job_if_normalized="$(
	sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' <<<"${job_if}" |
		tr '\n' ' ' |
		sed -e 's/[[:space:]]\+/ /g' -e 's/[[:space:]]*$//'
)"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
expected_job_if="!(github.event.workflow_run.name == 'Quality: Deep Fuzz (monthly)' && github.event.workflow_run.conclusion == 'failure' && github.event.workflow_run.run_attempt == 1)"
if [ "${job_if_normalized}" != "${expected_job_if}" ]; then
	fail "notify job condition must be exactly: ${expected_job_if}"
fi

# --- failure and success branches -------------------------------------------

# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'case "${CONCLUSION}" in' "${workflow}" ||
	fail "notify step must branch on the run conclusion"
grep -Fq "failure)" "${workflow}" ||
	fail "notify step must handle a failure conclusion"
grep -Fq "success)" "${workflow}" ||
	fail "notify step must handle a success conclusion"

# Each arm's body, scoped from its own `case` label to its own `;;`, not the
# whole file. A `gh issue create`/`comment`/`close` anywhere in the step used
# to satisfy these; scoping means the arms actually being swapped (bodies
# moved under the wrong label) shows up as a missing capability in the arm
# that's supposed to have it.
failure_branch="$(
	awk '
        /^          failure\)$/ { in_branch = 1; next }
        in_branch && /^            ;;$/ { in_branch = 0 }
        in_branch { print }
    ' "${workflow}"
)"
success_branch="$(
	awk '
        /^          success\)$/ { in_branch = 1; next }
        in_branch && /^            ;;$/ { in_branch = 0 }
        in_branch { print }
    ' "${workflow}"
)"

[ -n "${failure_branch}" ] || fail "expected a failure) case arm with a body"
[ -n "${success_branch}" ] || fail "expected a success) case arm with a body"

grep -Fq "gh issue create" <<<"${failure_branch}" ||
	fail "failure branch must be able to create a tracking issue"
grep -Fq "gh issue comment" <<<"${failure_branch}" ||
	fail "failure branch must be able to comment on an existing tracking issue"
grep -Fq "gh issue close" <<<"${success_branch}" ||
	fail "success branch must close the tracking issue"

# The `gh issue create` invocation itself, not the file at large: the label
# and title have to be on the call that actually creates the issue.
create_call="$(
	awk '
        /gh issue create \\$/ { in_call = 1 }
        in_call { print }
        in_call && !/\\$/ { in_call = 0 }
    ' "${workflow}"
)"

[ -n "${create_call}" ] || fail "expected a multi-line gh issue create invocation"

grep -Fq -- "--label quality-lane" <<<"${create_call}" ||
	fail "the gh issue create invocation must carry the quality-lane label"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
if ! grep -Fq -- '--title "${TITLE}"' <<<"${create_call}"; then
	# shellcheck disable=SC2016 # ${TITLE} here names the workflow's variable, not ours.
	fail 'the gh issue create invocation must set --title to ${TITLE}'
fi

# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'TITLE="[quality] ${WORKFLOW_NAME} is failing"' "${workflow}" ||
	fail "tracking issue title must follow the [quality] <workflow> is failing convention"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'select(.title == $t)' "${workflow}" ||
	fail "the open-issue search must filter by the same title it creates with"

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
