#!/usr/bin/env bash
#
# Append one scheduled quality lane's headline numbers to the repository's
# `quality-history` orphan branch.
#
# Usage: quality-history-append.sh <lane> <json-object>
#
#   <lane>         soak | mutation | mutation-survivors | fuzz-nightly | bench.
#                  Names the JSONL file on the orphan branch (`<lane>.jsonl`).
#   <json-object>  A single JSON object of that lane's numbers. The common
#                  envelope (run id, attempt, workflow, event, sha, UTC
#                  timestamp) is added here so every lane records it the same
#                  way, and it wins on a key collision so a lane can't
#                  overwrite `run_id` with something of its own.
#
# Why an orphan branch rather than a file on dev/main: the house rule is that
# committed generated artifacts change at a release cut and nowhere else, so a
# cron job that rewrites a tracked file on dev would mutate the tree underneath
# a tag. An orphan branch shares the repository (no new repo, no new secret)
# while sitting outside every released tree — `git log dev/v0.9` never sees it,
# a release archive never contains it, and a checkout never carries its weight.
#
# Why a temporary clone rather than the checkout in the workspace: the lanes
# check out with `persist-credentials: false`, and the workspace holds the
# lane's own build. A throwaway single-branch clone keeps the credential and
# the history commit entirely out of the job's working tree, and it is removed
# on exit.
#
# This never fails the caller. History is a trend surface, not a gate: an
# append that can't reach the remote must not turn a green quality lane red, or
# the mechanism that exists to make a slow regression visible becomes a new way
# to hide one. Every failure exits 0 behind a `::warning::` annotation.
#
# Environment overrides (all optional; the defaults are the CI values):
#   QUALITY_HISTORY_REMOTE          git remote to push to
#                                   (default https://github.com/$GITHUB_REPOSITORY.git)
#   QUALITY_HISTORY_BRANCH          branch name (default quality-history)
#   QUALITY_HISTORY_CREDENTIAL      credential for the HTTPS remote; on CI this
#                                   is ${{ github.token }}. Omitted for a local
#                                   filesystem remote.
#   QUALITY_HISTORY_PUSH_ATTEMPTS   CAS attempts before giving up (default 20)
#   QUALITY_HISTORY_EVENT           event name to gate on (default $GITHUB_EVENT_NAME)
#   QUALITY_HISTORY_TIMESTAMP       fixed record timestamp. Exists so the
#                                   duplicate-suppression path below can be
#                                   tested: it needs two invocations that
#                                   produce a byte-identical record.
#   QUALITY_HISTORY_RETAIN          keep only the newest N records in this
#                                   lane's file, applied after every append.
#                                   Unset means today's behaviour for every
#                                   lane: unbounded. A value below 1 is
#                                   refused (a warning, pruning skipped for
#                                   that invocation; the append itself still
#                                   happens).
#   QUALITY_HISTORY_RETAIN_BYTES    byte ceiling enforced alongside RETAIN,
#                                   default 1048576 (1 MiB) once RETAIN is
#                                   set. Whole oldest records are dropped
#                                   until the file fits or only the newest
#                                   record remains; the newest record is
#                                   never dropped, even if it alone exceeds
#                                   the ceiling (a `::warning::` is emitted
#                                   instead).

set -euo pipefail
export LC_ALL=C

work_dir=""

warn() {
	printf '::warning::quality-history: %s\n' "$*" >&2
}

# Any nonzero exit becomes a warning and a zero status. Cleanup happens here
# too so the credential in the temporary clone's config never outlives the run.
#
# `set +e` first, and every command in here is failure-tolerant. errexit still
# applies inside an EXIT trap, so a cleanup that failed (a read-only temp dir,
# an NFS mount that will not unlink) would abort the trap before `exit 0` and
# hand the caller the very nonzero status this whole function exists to
# prevent.
soft_exit() {
	local status=$?
	set +e
	if [ -n "${work_dir}" ] && [ -d "${work_dir}" ]; then
		rm -rf "${work_dir}" || warn "could not remove the temporary clone at ${work_dir}"
	fi
	if [ "${status}" -ne 0 ]; then
		warn "append did not complete (exit ${status}); the lane's own result is unaffected"
	fi
	exit 0
}
trap soft_exit EXIT

lane="${1:-}"
numbers="${2:-}"

if [ -z "${lane}" ] || [ -z "${numbers}" ]; then
	warn "usage: $0 <lane> <json-object>"
	exit 1
fi

case "${lane}" in
soak | mutation | mutation-survivors | fuzz-nightly | bench) ;;
*)
	warn "unknown lane '${lane}'; expected one of soak, mutation, mutation-survivors, fuzz-nightly, bench"
	exit 1
	;;
esac

if ! command -v jq >/dev/null 2>&1; then
	warn "jq is required to build a history record"
	exit 1
fi

# Only scheduled and manually dispatched runs write history. A pull_request or
# push run measures a branch, not the trunk's trend, and mixing the two makes
# the series unreadable. The calling workflows gate the step on the same
# condition; this is the second lock, and it is the one a copy-pasted step into
# some future PR-triggered lane still hits.
#
# An unset or empty event is refused rather than allowed through. Allowing it
# made the gate fail open in exactly the case where it is most needed: a caller
# that forgot to pass the event, or a context where GITHUB_EVENT_NAME is not
# set, would record unconditionally. Both workflows pass it explicitly.
event="${QUALITY_HISTORY_EVENT:-${GITHUB_EVENT_NAME:-}}"
case "${event}" in
schedule | workflow_dispatch) ;;
*)
	echo "quality-history: event '${event}' is not schedule or workflow_dispatch; not recording."
	exit 0
	;;
esac

if ! printf '%s' "${numbers}" | jq -e 'type == "object"' >/dev/null 2>&1; then
	warn "the numbers argument is not a JSON object: ${numbers}"
	exit 1
fi

branch="${QUALITY_HISTORY_BRANCH:-quality-history}"
remote="${QUALITY_HISTORY_REMOTE:-}"
if [ -z "${remote}" ]; then
	if [ -z "${GITHUB_REPOSITORY:-}" ]; then
		warn "no QUALITY_HISTORY_REMOTE and no GITHUB_REPOSITORY to derive one from"
		exit 1
	fi
	remote="https://github.com/${GITHUB_REPOSITORY}.git"
fi

attempts="${QUALITY_HISTORY_PUSH_ATTEMPTS:-20}"
case "${attempts}" in
'' | *[!0-9]*)
	warn "QUALITY_HISTORY_PUSH_ATTEMPTS must be a positive integer, got '${attempts}'"
	exit 1
	;;
esac
[ "${attempts}" -ge 1 ] || attempts=1

# QUALITY_HISTORY_RETAIN caps the newest N records this lane keeps; unset
# means today's unbounded behaviour for every lane. Invalid is refused, not
# fatal: pruning is skipped for this invocation and the record is still
# appended, because a bad cap is not a reason to lose the measurement.
retain="${QUALITY_HISTORY_RETAIN:-}"
case "${retain}" in
'') ;;
*[!0-9]*)
	warn "QUALITY_HISTORY_RETAIN must be a positive integer, got '${retain}'; not pruning."
	retain=""
	;;
*)
	if [ "${retain}" -lt 1 ]; then
		warn "QUALITY_HISTORY_RETAIN must be at least 1, got '${retain}'; not pruning."
		retain=""
	fi
	;;
esac

retain_bytes="${QUALITY_HISTORY_RETAIN_BYTES:-}"
if [ -n "${retain}" ] && [ -z "${retain_bytes}" ]; then
	retain_bytes=1048576
fi
case "${retain_bytes}" in
'') ;;
*[!0-9]*)
	warn "QUALITY_HISTORY_RETAIN_BYTES must be a positive integer, got '${retain_bytes}'; not enforcing a byte ceiling."
	retain_bytes=""
	;;
esac

record="$(
	jq -cn \
		--argjson numbers "${numbers}" \
		--arg lane "${lane}" \
		--arg timestamp "${QUALITY_HISTORY_TIMESTAMP:-$(date -u '+%Y-%m-%dT%H:%M:%SZ')}" \
		--arg workflow "${GITHUB_WORKFLOW:-local}" \
		--arg event "${event:-local}" \
		--arg run_id "${GITHUB_RUN_ID:-}" \
		--arg run_attempt "${GITHUB_RUN_ATTEMPT:-}" \
		--arg run_number "${GITHUB_RUN_NUMBER:-}" \
		--arg sha "${GITHUB_SHA:-}" \
		--arg ref "${GITHUB_REF_NAME:-}" \
		'$numbers + {
            lane: $lane,
            timestamp: $timestamp,
            workflow: $workflow,
            event: $event,
            run_id: (if $run_id == "" then null else $run_id end),
            run_attempt: ($run_attempt | tonumber? // null),
            run_number: ($run_number | tonumber? // null),
            sha: (if $sha == "" then null else $sha end),
            ref: (if $ref == "" then null else $ref end)
        }'
)"

work_dir="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/quality-history.XXXXXX")"
repo_dir="${work_dir}/repo"
file="${lane}.jsonl"

readme_text() {
	cat <<'EOF'
# quality-history

Machine-written trend data for Portwing's scheduled quality lanes. One
append-only JSONL file per lane, one record per lane run:

| File | Lane | Cadence |
|---|---|---|
| `soak.jsonl` | `quality-soak-weekly.yml` | weekly |
| `mutation.jsonl` | `quality-mutation-monthly.yml` | monthly, one record per matrix package |
| `mutation-survivors.jsonl` | `quality-mutation-monthly.yml` | monthly, one record per run, capped to the newest 12 |
| `fuzz-nightly.jsonl` | `quality-fuzz-nightly.yml` | nightly |
| `bench.jsonl` | `quality-bench-monthly.yml` | monthly |

This is an orphan branch. It shares no history with `main` or any `dev/*`
branch, it is never merged, and nothing here is part of a release. Records are
written by `scripts/ci/quality-history-append.sh` on the trunk branches; read
them with `scripts/quality-history.sh <lane> [--last N]`.

Every record carries the same envelope: `lane`, `timestamp` (UTC), `workflow`,
`event`, `run_id`, `run_attempt`, `run_number`, `sha`, `ref`. The remaining
keys are the lane's own headline numbers.

Only `schedule` and `workflow_dispatch` runs append. Rewriting history here is
safe: nothing depends on these commits, and the branch can be deleted and
allowed to bootstrap itself again.
EOF
}

# One compare-and-swap attempt: fetch the branch (or bootstrap it), append,
# commit, push. The whole tree is rebuilt per attempt rather than reused, so a
# rejected attempt can't carry a stale index or a half-applied append into the
# next one.
#
# Every step is checked explicitly instead of leaning on `set -e`: bash
# suppresses errexit for the whole call chain of a function invoked as an `if`
# condition, which this one is. Without the explicit returns, a failed `git
# commit` would fall through to a push of the unchanged fetched head, and that
# push would succeed — reporting a recorded run that recorded nothing.
attempt_append() {
	rm -rf "${repo_dir}" || return 1
	mkdir -p "${repo_dir}" || return 1

	git init --quiet --initial-branch=history "${repo_dir}" || return 1
	git -C "${repo_dir}" config user.name \
		"${QUALITY_HISTORY_AUTHOR_NAME:-github-actions[bot]}" || return 1
	git -C "${repo_dir}" config user.email \
		"${QUALITY_HISTORY_AUTHOR_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}" || return 1
	git -C "${repo_dir}" config commit.gpgsign false || return 1

	if [ -n "${QUALITY_HISTORY_CREDENTIAL:-}" ]; then
		# Written into the throwaway clone's own config, the way
		# actions/checkout does it, so it is removed with the temporary
		# directory instead of living in the workspace checkout.
		git -C "${repo_dir}" config http.extraheader \
			"AUTHORIZATION: basic $(printf 'x-access-token:%s' "${QUALITY_HISTORY_CREDENTIAL}" | base64 | tr -d '\n')" ||
			return 1
	fi

	git -C "${repo_dir}" remote add origin "${remote}" || return 1

	# A shallow fetch keeps the clone one commit deep however long the
	# series gets; the push that follows is a normal fast-forward because
	# the remote already has the parent.
	if git -C "${repo_dir}" fetch --quiet --depth=1 origin \
		"+refs/heads/${branch}:refs/remotes/origin/${branch}" 2>/dev/null; then
		git -C "${repo_dir}" checkout --quiet -B history \
			"refs/remotes/origin/${branch}" || return 1
	else
		echo "quality-history: ${branch} does not exist yet; creating it."
	fi

	# A push can land server-side and still report failure to the client: the
	# ref moves, the response is lost, and the retry below would append the
	# same run a second time. The record is unique per lane, run, attempt and
	# package, so finding this exact line already present means the previous
	# attempt did land and there is nothing left to do. Checked on every
	# attempt, which also makes a re-invocation of a run that already
	# recorded a no-op rather than a duplicate row.
	if [ -f "${repo_dir}/${file}" ] && grep -Fxq "${record}" "${repo_dir}/${file}"; then
		echo "quality-history: this record is already on ${branch}; not appending it twice."
		return 0
	fi

	# Unconditional: a `[ ! -f ]` guard means an edit to readme_text() never
	# reaches an existing branch, so a lane added after the branch already
	# exists (this one included) would go undocumented forever.
	readme_text >"${repo_dir}/README.md" || return 1
	printf '%s\n' "${record}" >>"${repo_dir}/${file}" || return 1

	# QUALITY_HISTORY_RETAIN, applied after the append and before staging:
	# a CAS retry replays this on top of whoever won, so the pruned result
	# is always relative to the branch's current tip rather than a stale
	# fetch. Whole oldest records are dropped, never part of one, so every
	# retained record stays self-consistent; the newest record (the one
	# this attempt just wrote) is never dropped.
	if [ -n "${retain}" ]; then
		local pruned="${repo_dir}/${file}.pruned"
		tail -n "${retain}" "${repo_dir}/${file}" >"${pruned}" || return 1
		mv "${pruned}" "${repo_dir}/${file}" || return 1

		if [ -n "${retain_bytes}" ]; then
			while [ "$(wc -l <"${repo_dir}/${file}")" -gt 1 ] &&
				[ "$(wc -c <"${repo_dir}/${file}")" -gt "${retain_bytes}" ]; do
				tail -n +2 "${repo_dir}/${file}" >"${pruned}" || return 1
				mv "${pruned}" "${repo_dir}/${file}" || return 1
			done

			if [ "$(wc -c <"${repo_dir}/${file}")" -gt "${retain_bytes}" ]; then
				echo "::warning::quality-history: the newest ${lane} record alone exceeds the ${retain_bytes}-byte ceiling; keeping it anyway."
			fi
		fi
	fi

	git -C "${repo_dir}" add README.md "${file}" || return 1
	git -C "${repo_dir}" commit --quiet \
		-m "chore(quality-history): ${lane} ${GITHUB_RUN_ID:-local} attempt ${GITHUB_RUN_ATTEMPT:-1}" ||
		return 1

	git -C "${repo_dir}" push --quiet origin "history:refs/heads/${branch}" || return 1
}

# An arithmetic loop, not `for attempt in $(seq 1 N)`. A missing or shadowed
# seq expands to nothing, the loop body never runs, and the script exits 0
# having recorded nothing at all: a silent no-op is the one failure mode this
# script's own soft-failure design cannot distinguish from success.
attempt=1
while [ "${attempt}" -le "${attempts}" ]; do
	if attempt_append; then
		echo "quality-history: recorded ${lane} on ${branch} (attempt ${attempt}/${attempts})."
		exit 0
	fi

	if [ "${attempt}" -eq "${attempts}" ]; then
		warn "could not append the ${lane} record after ${attempts} attempts"
		exit 1
	fi

	# Another lane's job can be pushing to this same branch, so a rejected
	# push is an expected case rather than an error. Refetch and replay on
	# top of whoever won; jittered so two racers don't retry in lockstep.
	echo "quality-history: attempt ${attempt}/${attempts} did not land, retrying."
	sleep "$(((RANDOM % 3) + 1))"
	attempt=$((attempt + 1))
done
