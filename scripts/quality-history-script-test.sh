#!/usr/bin/env bash
#
# Functional test for scripts/ci/quality-history-append.sh and
# scripts/quality-history.sh, run against a throwaway bare repository.
#
# The mechanism these two scripts implement has exactly one live rehearsal a
# week (the soak lane) and one a month (mutation), and it is deliberately
# incapable of failing its caller, so a broken appender reports as a warning
# buried in a scheduled run's log. That is the same shape of silence PW-6.1
# found in the mutation gate. This test is the standing check instead: it
# drives the real scripts over a real git remote and asserts the three paths
# that matter — bootstrap, ordinary append, and a push rejected by a
# concurrent writer — plus the refusals that keep junk out of the series.
#
# The concurrency case is the one worth the setup. Sixteen mutation matrix
# legs push to one branch, so a rejected push is routine rather than
# exceptional, and the failure mode is silent data loss: a retry that force-
# pushed, or that replayed a stale fetch, would drop the record that won the
# race and nobody would ever see the gap. The competing commit is staged in
# the bare repo before the push and a pre-receive hook swaps the branch onto
# it once, which reproduces "someone else landed between your fetch and your
# push" deterministically.

set -euo pipefail
export LC_ALL=C

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
append="${repo_root}/scripts/ci/quality-history-append.sh"
query="${repo_root}/scripts/quality-history.sh"

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-quality-history.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

bare="${test_root}/qh.git"
failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

# The appender never exits nonzero by design, so every assertion has to read
# the remote's state (or the annotation on stderr), never the exit code alone.
run_append() {
	local lane="$1"
	local numbers="$2"
	shift 2
	env \
		QUALITY_HISTORY_REMOTE="${bare}" \
		QUALITY_HISTORY_EVENT="schedule" \
		QUALITY_HISTORY_PUSH_ATTEMPTS="${QUALITY_HISTORY_PUSH_ATTEMPTS:-4}" \
		QUALITY_HISTORY_AUTHOR_NAME="test" \
		QUALITY_HISTORY_AUTHOR_EMAIL="test@example.invalid" \
		GITHUB_RUN_ID="${GITHUB_RUN_ID_OVERRIDE:-100}" \
		GITHUB_RUN_ATTEMPT="1" \
		GITHUB_RUN_NUMBER="7" \
		GITHUB_WORKFLOW="Quality: Test" \
		GITHUB_SHA="0000000000000000000000000000000000000000" \
		GITHUB_REF_NAME="dev/v0.9" \
		"$@" \
		bash "${append}" "${lane}" "${numbers}" 2>&1
}

branch_file() {
	git -C "${bare}" cat-file blob "quality-history:$1" 2>/dev/null || true
}

git init --quiet --bare "${bare}"

# --- bootstrap: the branch does not exist yet -------------------------------

output="$(run_append soak '{"rss_growth_bytes":1024,"outcome":"success"}')" || true

if ! git -C "${bare}" rev-parse --verify --quiet refs/heads/quality-history >/dev/null; then
	fail "bootstrap must create the quality-history branch (output: ${output})"
fi

soak_lines="$(branch_file soak.jsonl | grep -c . || true)"
[ "${soak_lines}" = "1" ] ||
	fail "bootstrap must write exactly one soak record, found ${soak_lines}"

[ -n "$(branch_file README.md)" ] ||
	fail "bootstrap must write a README.md explaining the branch"

# The branch has to be an orphan: sharing history with a trunk branch is what
# would drag these commits into a release tree.
parents="$(git -C "${bare}" rev-list --max-parents=0 --count refs/heads/quality-history)"
[ "${parents}" = "1" ] ||
	fail "the history branch must start from a root commit, found ${parents} roots"

# --- the envelope the appender adds ------------------------------------------

record="$(branch_file soak.jsonl | tail -n1)"
for key in lane timestamp workflow event run_id run_attempt sha ref rss_growth_bytes; do
	if ! jq -e --arg k "${key}" 'has($k)' >/dev/null 2>&1 <<<"${record}"; then
		fail "record must carry '${key}' (record: ${record})"
	fi
done
[ "$(jq -r '.lane' <<<"${record}")" = "soak" ] || fail "record lane must be the lane argument"
[ "$(jq -r '.run_id' <<<"${record}")" = "100" ] || fail "record must carry the run id"
[ "$(jq -r '.run_attempt' <<<"${record}")" = "1" ] || fail "record must carry the run attempt"
jq -e '.timestamp | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")' \
	>/dev/null <<<"${record}" || fail "timestamp must be UTC RFC3339 (got $(jq -r '.timestamp' <<<"${record}"))"

# The envelope wins a key collision: a lane must not be able to overwrite the
# provenance fields with numbers of its own.
GITHUB_RUN_ID_OVERRIDE=101 run_append soak '{"run_id":"spoofed","rss_growth_bytes":2048}' >/dev/null || true
[ "$(branch_file soak.jsonl | tail -n1 | jq -r '.run_id')" = "101" ] ||
	fail "the envelope must win a key collision with the lane's own numbers"

# --- ordinary append onto an existing branch ---------------------------------

run_append mutation '{"package":"./internal/auth","efficacy":79.17}' >/dev/null || true

[ "$(branch_file mutation.jsonl | grep -c . || true)" = "1" ] ||
	fail "a second lane must get its own JSONL file"
[ "$(branch_file soak.jsonl | grep -c . || true)" = "2" ] ||
	fail "appending one lane must not disturb another lane's file"

# --- push rejected by a concurrent writer ------------------------------------
#
# Stage a competing commit on top of the current tip, then reject exactly one
# push and swap the branch onto it. A correct appender refetches and replays;
# an appender that force-pushed or reused its first fetch would drop the
# competitor's line.

staging="${test_root}/competitor"
git clone --quiet --branch quality-history "${bare}" "${staging}"
git -C "${staging}" config user.name "competitor"
git -C "${staging}" config user.email "competitor@example.invalid"
echo '{"lane":"soak","competing":true}' >>"${staging}/soak.jsonl"
git -C "${staging}" add soak.jsonl
git -C "${staging}" commit --quiet -m "competing writer"
git -C "${staging}" push --quiet origin HEAD:refs/competitors/staged

cat >"${bare}/hooks/pre-receive" <<'HOOK'
#!/bin/sh
# Reject the first push only, having moved the branch to the staged competing
# commit.
#
# Two things about the quarantine a pre-receive hook runs inside. It refuses
# ref updates outright ("ref updates forbidden inside quarantine
# environment"), so GIT_QUARANTINE_PATH has to go before update-ref will
# move the branch. And any object the hook wrote would be discarded along
# with the rejected push, which is why the competing commit is staged into
# the repository beforehand and only pointed at from here.
marker="$(git rev-parse --git-dir)/rejected-once"
if [ -f "${marker}" ]; then
    exit 0
fi
: >"${marker}"
unset GIT_QUARANTINE_PATH
git update-ref refs/heads/quality-history refs/competitors/staged
echo "simulated concurrent writer" >&2
exit 1
HOOK
chmod +x "${bare}/hooks/pre-receive"

before="$(branch_file soak.jsonl | grep -c . || true)"
output="$(run_append soak '{"rss_growth_bytes":4096,"outcome":"success"}')" || true
after="$(branch_file soak.jsonl | grep -c . || true)"

grep -Fq "did not land, retrying" <<<"${output}" ||
	fail "a rejected push must announce the retry (output: ${output})"
[ "${after}" = "$((before + 2))" ] ||
	fail "after a rejected push the file must hold both the competitor's line and ours (${before} -> ${after})"
branch_file soak.jsonl | grep -Fq '"competing":true' ||
	fail "the retry must replay onto the competitor's commit, not overwrite it"
[ "$(branch_file soak.jsonl | tail -n1 | jq -r '.rss_growth_bytes')" = "4096" ] ||
	fail "the retried record must be the last line"

rm -f "${bare}/hooks/pre-receive"

# --- refusals ----------------------------------------------------------------

lines_before="$(branch_file soak.jsonl | grep -c . || true)"

# A pull_request or push run must never write to the series.
output="$(env QUALITY_HISTORY_EVENT="pull_request" \
	QUALITY_HISTORY_REMOTE="${bare}" \
	bash "${append}" soak '{"rss_growth_bytes":1}' 2>&1)" || true
grep -Fq "not schedule or workflow_dispatch" <<<"${output}" ||
	fail "a pull_request run must be refused by name (output: ${output})"
[ "$(branch_file soak.jsonl | grep -c . || true)" = "${lines_before}" ] ||
	fail "a pull_request run must not append"

# Junk arguments warn and change nothing.
for bad in 'not json' '[1,2,3]' '"a string"'; do
	output="$(run_append soak "${bad}")" || true
	grep -Fq "::warning::" <<<"${output}" ||
		fail "a non-object numbers argument must warn (input: ${bad})"
done
output="$(run_append not-a-lane '{"x":1}')" || true
grep -Fq "unknown lane" <<<"${output}" || fail "an unknown lane must be refused"
[ "$(branch_file soak.jsonl | grep -c . || true)" = "${lines_before}" ] ||
	fail "a refused append must leave the series untouched"

# An unset event is refused rather than treated as "probably fine". Allowing
# it made the gate fail open exactly where it mattered: a caller that forgot
# to pass the event recorded unconditionally.
output="$(env -u QUALITY_HISTORY_EVENT -u GITHUB_EVENT_NAME \
	QUALITY_HISTORY_REMOTE="${bare}" \
	bash "${append}" soak '{"rss_growth_bytes":1}' 2>&1)" || true
grep -Fq "not schedule or workflow_dispatch" <<<"${output}" ||
	fail "an unset event must be refused (output: ${output})"
[ "$(branch_file soak.jsonl | grep -c . || true)" = "${lines_before}" ] ||
	fail "an unset event must not append"

# A push can land server-side and lose its response, and the retry would then
# write the same run twice. Two invocations that produce a byte-identical
# record must leave one row, not two. QUALITY_HISTORY_TIMESTAMP exists so this
# is reproducible; everything else about the record is already fixed by the
# run id, attempt and lane.
duplicate_numbers='{"rss_growth_bytes":777,"outcome":"success"}'
run_append_fixed() {
	env \
		QUALITY_HISTORY_REMOTE="${bare}" \
		QUALITY_HISTORY_EVENT="schedule" \
		QUALITY_HISTORY_TIMESTAMP="2026-09-03T00:00:00Z" \
		QUALITY_HISTORY_AUTHOR_NAME="test" \
		QUALITY_HISTORY_AUTHOR_EMAIL="test@example.invalid" \
		GITHUB_RUN_ID="900" \
		GITHUB_RUN_ATTEMPT="1" \
		GITHUB_RUN_NUMBER="9" \
		GITHUB_WORKFLOW="Quality: Test" \
		GITHUB_SHA="1111111111111111111111111111111111111111" \
		GITHUB_REF_NAME="dev/v0.9" \
		bash "${append}" soak "${duplicate_numbers}" 2>&1
}

run_append_fixed >/dev/null || true
after_first="$(branch_file soak.jsonl | grep -c . || true)"
output="$(run_append_fixed)" || true
after_second="$(branch_file soak.jsonl | grep -c . || true)"

grep -Fq "already on quality-history" <<<"${output}" ||
	fail "a repeated identical record must be recognised, not re-appended (output: ${output})"
[ "${after_second}" = "${after_first}" ] ||
	fail "a repeated identical record must not grow the file (${after_first} -> ${after_second})"
[ "$(branch_file soak.jsonl | grep -c '"rss_growth_bytes":777' || true)" = "1" ] ||
	fail "the duplicated run must appear exactly once in the series"

lines_before="$(branch_file soak.jsonl | grep -c . || true)"

# An unreachable remote warns and exits 0. This is the property that keeps a
# history outage from turning a green quality lane red.
set +e
env QUALITY_HISTORY_REMOTE="${test_root}/does-not-exist.git" \
	QUALITY_HISTORY_EVENT="schedule" \
	QUALITY_HISTORY_PUSH_ATTEMPTS=2 \
	bash "${append}" soak '{"rss_growth_bytes":1}' >"${test_root}/unreachable.log" 2>&1
status=$?
set -e
[ "${status}" -eq 0 ] ||
	fail "an unreachable remote must still exit 0, got ${status}"
grep -Fq "::warning::" "${test_root}/unreachable.log" ||
	fail "an unreachable remote must emit a warning annotation"

# --- the query helper reads what the appender wrote --------------------------

table="$(env QUALITY_HISTORY_REMOTE="${bare}" bash "${query}" soak --last 2)"
head -n1 <<<"${table}" | grep -Fq "timestamp" ||
	fail "the query helper must print a header row (output: ${table})"
grep -Fq "4096" <<<"${table}" ||
	fail "the query helper must show the most recent soak numbers (output: ${table})"
[ "$(grep -c . <<<"${table}")" = "3" ] ||
	fail "--last 2 must print a header and two rows, got $(grep -c . <<<"${table}") lines"

json="$(env QUALITY_HISTORY_REMOTE="${bare}" bash "${query}" soak --last 1 --json)"
jq -e '.lane == "soak"' >/dev/null <<<"${json}" ||
	fail "--json must print raw JSONL records (output: ${json})"

set +e
env QUALITY_HISTORY_REMOTE="${bare}" bash "${query}" bench >"${test_root}/bench.log" 2>&1
status=$?
set -e
[ "${status}" -ne 0 ] || fail "querying a lane with no records must fail loudly"
grep -Fq "has not recorded" "${test_root}/bench.log" ||
	fail "querying an unrecorded lane must say so (output: $(cat "${test_root}/bench.log"))"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} quality-history script check(s) failed" >&2
	exit 1
fi

echo "Quality history script checks passed."
