#!/usr/bin/env bash
#
# Contract for the quality-history recording jobs (PW-5.5).
#
# The jobs this guards run once a week in one lane and once a month in
# another, cannot fail their caller by design, and write to a branch nobody
# checks out. Every property below is one that would otherwise be discovered
# weeks later, or never:
#
#   * the recording job still exists, in both lanes, calling the shared script;
#   * it fires only on schedule/workflow_dispatch, so a branch's numbers never
#     land in the trunk's series;
#   * `contents: write` is the recording job's alone. It is the only write
#     scope in either lane, and the measuring jobs run code — Gremlins
#     literally executes mutated source, the soak job runs the agent for four
#     hours — so a write-scoped credential in one of those is a real
#     escalation and not a style point;
#   * the recording job builds and runs nothing. A job that checks out, calls
#     jq and pushes has no way to reach a credential through code it executed;
#     the moment it grows a `setup-go` or a `go test`, that stops being true.
#
# Usage: quality-history-config-test.sh [soak.yml] [mutation.yml] [append.sh]

set -euo pipefail
export LC_ALL=C

soak_workflow="${1:-.github/workflows/quality-soak-weekly.yml}"
mutation_workflow="${2:-.github/workflows/quality-mutation-monthly.yml}"
append_script="${3:-scripts/ci/quality-history-append.sh}"

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

for path in "${soak_workflow}" "${mutation_workflow}" "${append_script}"; do
	if [ ! -f "${path}" ]; then
		fail "file not found: ${path}"
		exit 1
	fi
done

# Comment lines are not code. Every "must not contain" assertion below reads
# through this, because the jobs explain themselves at length and a job whose
# comment names soak.sh is not a job that runs soak.sh.
strip_comments() {
	grep -v '^[[:space:]]*#' || true
}

# A job's own block, from its two-space key to the next one. Every scoped
# assertion below reads from this rather than from the whole file, so a
# `contents: write` that migrated to a different job can't satisfy a check
# that is about the recording job.
job_block() {
	awk -v job="  $2:" '
        $0 == job { in_job = 1; next }
        in_job && /^  [^[:space:]]/ { in_job = 0 }
        in_job { print }
    ' "$1"
}

# The gate is asserted as one exact string rather than as three substring
# greps: a substring check cannot tell `||` from `&&`, and the difference
# between "schedule or dispatch" and "schedule and dispatch" is the
# difference between a series that records every run and one that records
# none.
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
gate="always() && (github.event_name == 'schedule' || github.event_name == 'workflow_dispatch')"

check_lane() {
	local workflow="$1"
	local measuring_job="$2"
	local lane="$3"
	local label="$4"

	local block
	block="$(job_block "${workflow}" "history")"
	if [ -z "${block}" ]; then
		fail "${label}: expected a top-level 'history' job"
		return
	fi

	grep -Fq "    needs: ${measuring_job}" <<<"${block}" ||
		fail "${label}: the history job must run after '${measuring_job}'"

	grep -Fq "    if: ${gate}" <<<"${block}" ||
		fail "${label}: the history job condition must be exactly: ${gate}"

	grep -Fq "scripts/ci/quality-history-append.sh ${lane} " <<<"${block}" ||
		fail "${label}: the history job must call the shared script with lane '${lane}'"

	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	grep -Fq 'QUALITY_HISTORY_CREDENTIAL: ${{ secrets.GITHUB_TOKEN }}' <<<"${block}" ||
		fail "${label}: the history job must pass the credential through QUALITY_HISTORY_CREDENTIAL"

	# The event is passed explicitly rather than left to the ambient
	# GITHUB_EVENT_NAME, because the script now refuses an empty event
	# instead of treating it as "probably fine".
	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	grep -Fq 'QUALITY_HISTORY_EVENT: ${{ github.event_name }}' <<<"${block}" ||
		fail "${label}: the history job must pass the event through QUALITY_HISTORY_EVENT"

	# The property that makes the split worth having: the job holding the
	# credential executes nothing it could have been made to execute.
	local code
	code="$(strip_comments <<<"${block}")"
	local forbidden
	# Invocations, not job names: `needs: gremlins` is a dependency edge, not
	# something this job executes.
	for forbidden in "actions/setup-go" "gremlins unleash" "scripts/soak.sh" \
		"go test" "go install" "go build" "go run"; do
		if grep -Fq "${forbidden}" <<<"${code}"; then
			fail "${label}: the history job must not run '${forbidden}'; it holds the write credential"
		fi
	done

	local job_permissions
	job_permissions="$(
		awk '
            $0 == "    permissions:" { in_perms = 1; next }
            in_perms && /^    [^[:space:]]/ { in_perms = 0 }
            in_perms { print }
        ' <<<"${block}" | grep -v '^[[:space:]]*$'
	)"
	if [ "${job_permissions}" != "      contents: write" ]; then
		fail "${label}: the history job's permissions must be exactly contents: write (found: $(tr '\n' ';' <<<"${job_permissions}"))"
	fi

	# The measuring job must not have grown a write scope of its own, and
	# neither may anything else in the file.
	if job_block "${workflow}" "${measuring_job}" | grep -q '^    permissions:'; then
		fail "${label}: the '${measuring_job}' job must not declare permissions of its own"
	fi

	local write_count
	write_count="$(grep -c '^[[:space:]]*contents: write$' "${workflow}" || true)"
	if [ "${write_count}" -ne 1 ]; then
		fail "${label}: contents: write must appear exactly once in the file, found ${write_count}"
	fi

	grep -Eq '^  contents: read$' "${workflow}" ||
		fail "${label}: the workflow-level permissions must stay contents: read"
}

check_lane "${soak_workflow}" "soak" "soak" "soak"
check_lane "${mutation_workflow}" "gremlins" "mutation" "mutation"

# The mutation matrix hands its numbers over as an artifact, so the record has
# to actually be produced and uploaded or the history job downloads nothing and
# the whole lane records silence.
mutation_gremlins="$(job_block "${mutation_workflow}" "gremlins")"
grep -Fq "      - name: Record this package for the quality history" <<<"${mutation_gremlins}" ||
	fail "mutation: the matrix leg must write its record for the history job"
grep -Fq "      - name: Upload the quality history record" <<<"${mutation_gremlins}" ||
	fail "mutation: the matrix leg must upload its record for the history job"
grep -Fq "        if: ${gate}" <<<"${mutation_gremlins}" ||
	fail "mutation: the record steps must carry the same schedule/dispatch gate"
if grep -Fq "QUALITY_HISTORY_CREDENTIAL" <<<"$(strip_comments <<<"${mutation_gremlins}")"; then
	fail "mutation: the matrix leg must never see the write credential"
fi

# A bare `jq -e .` exits 0 for an array, a string or a number. Those are valid
# JSON and are not Gremlins reports: the metric extraction errors into `{}` and
# the row still claims it was gated on a real measurement. The type check is
# the only thing standing between that and a lying series.
# shellcheck disable=SC2016 # asserting the workflow's literal jq program text
if grep -Fq "elif jq -e . mutation-report.json" <<<"${mutation_gremlins}"; then
	fail "mutation: the gated-mode test must require a JSON object, not any JSON value"
fi
# shellcheck disable=SC2016 # same, the expected form
grep -Fq "elif jq -e 'type == \"object\"' mutation-report.json" <<<"${mutation_gremlins}" ||
	fail "mutation: the gated-mode test must be jq -e 'type == \"object\"'"

# The advisory and canary jobs measure things and record nothing. Naming them
# keeps the "only the recording job can write" property from decaying into
# "some job in this file can write".
for job in mutation-advisory gate-canary; do
	if job_block "${mutation_workflow}" "${job}" | grep -q '^    permissions:'; then
		fail "mutation: the '${job}' job must not declare permissions of its own"
	fi
done

# --- the appender's own second lock ------------------------------------------
#
# The workflow `if:` is the first gate and a copy-paste away from being wrong.
# The script refuses a non-scheduled event on its own, which is what a lane
# added later still hits even if its author forgets the condition. The empty
# case is refused too: allowing it made the gate fail open exactly when a
# caller forgot to pass the event.

grep -Eq '^schedule \| workflow_dispatch\) ;;$' "${append_script}" ||
	fail "the append script must refuse any event that is not schedule or workflow_dispatch"

if grep -Eq '^schedule \| workflow_dispatch \| ""\) ;;$' "${append_script}"; then
	fail "the append script must not treat an empty event as permission to record"
fi

grep -Fq 'trap soft_exit EXIT' "${append_script}" ||
	fail "the append script must convert its own failures into a warning, never a nonzero exit"

# The trap runs under errexit, so a cleanup failure inside it would abort
# before `exit 0` and hand the caller the nonzero status the trap exists to
# swallow.
grep -Eq '^\tset \+e$' "${append_script}" ||
	fail "the append script's exit trap must disable errexit before cleaning up"

# A duplicate row is indistinguishable from a real second measurement once it
# is in the series, and a push that lands but loses its response produces one.
# shellcheck disable=SC2016 # Asserting the literal text of the append script.
grep -Fq 'grep -Fxq "${record}"' "${append_script}" ||
	fail "the append script must not append a record the branch already carries"

# seq is not guaranteed present; an empty expansion would skip the retry loop
# entirely and exit 0 having recorded nothing.
if strip_comments <"${append_script}" | grep -Fq 'seq 1'; then
	fail "the append script's retry loop must not depend on seq"
fi

[ -x "${append_script}" ] ||
	fail "the append script must be executable"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} quality-history contract check(s) failed" >&2
	exit 1
fi

echo "Quality history contract checks passed."
