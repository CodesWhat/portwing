#!/usr/bin/env bash
set -euo pipefail

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

escape_ere() {
	printf '%s\n' "$1" | sed 's/[][(){}.^$*+?|\\]/\\&/g'
}

fuzzers=(
	"FuzzParsePHC|./internal/server/"
	"FuzzParseTrustedProxies|./internal/server/"
	"FuzzParseImageRef|./internal/adapter/"
	"FuzzParseLabels|./internal/adapter/drydock/"
	"FuzzMCPHandler|./internal/mcp/"
	"FuzzEnvelope|./internal/protocol/"
	"FuzzVerifyRequest|./internal/auth/"
	"FuzzDecodeContainerLogStream|./internal/docker/"
	"FuzzComposeRequestValidate|./internal/docker/"
	"FuzzParseKeyLine|./internal/auth/"
)

lefthook_fuzz_entries="$(
	awk '
		/^[[:space:]]*for entry in \\[[:space:]]*$/ { in_entries = 1 }
		in_entries { print }
		in_entries && /;[[:space:]]*do[[:space:]]*$/ { exit }
	' lefthook.yml
)"

caller_fuzz_inventory="$(
	awk '
		$0 == "      fuzzers-json: >-" {
			getline
			print
		}
	' .github/workflows/ci-verify.yml
)"

# PW-2.1. A seed corpus regression is invisible from the outside: deleting
# testdata/fuzz/<Target>/ still passes `go test`, and dropping the cache steps
# still produces a green fuzz run that quietly starts from f.Add() every night.
# Both get asserted here.
corpus_max_bytes=4096

for spec in "${fuzzers[@]}"; do
	fuzzer="${spec%%|*}"
	pkg="${spec#*|}"
	fuzzer_regex="$(escape_ere "${fuzzer}")"
	pkg_regex="$(escape_ere "${pkg}")"
	workflow_mapping="^[[:space:]]*-[[:space:]]*\\{[[:space:]]*name:[[:space:]]*${fuzzer_regex}[[:space:]]*,[[:space:]]*pkg:[[:space:]]*${pkg_regex}[[:space:]]*\\}[[:space:]]*$"
	caller_mapping="{\"name\":\"${fuzzer}\",\"pkg\":\"${pkg}\"}"
	lefthook_entry="^[[:space:]]*\"${fuzzer_regex}[[:space:]]+${pkg_regex}\"([[:space:]]+\\\\|;[[:space:]]*do)[[:space:]]*$"

	grep -Fq "${caller_mapping}" <<<"${caller_fuzz_inventory}" ||
		fail "ci-verify.yml must run ${fuzzer} in ${pkg}"
	grep -Eq "${lefthook_entry}" <<<"${lefthook_fuzz_entries}" ||
		fail "lefthook.yml must run ${fuzzer} in ${pkg}"
	grep -Eq "${workflow_mapping}" .github/workflows/quality-fuzz-nightly.yml ||
		fail "quality-fuzz-nightly.yml must run ${fuzzer} in ${pkg}"
	grep -Eq "${workflow_mapping}" .github/workflows/quality-fuzz-monthly.yml ||
		fail "quality-fuzz-monthly.yml must run ${fuzzer} in ${pkg}"

	corpus_dir="${pkg#./}"
	corpus_dir="${corpus_dir%/}/testdata/fuzz/${fuzzer}"
	corpus_count=0
	if [ -d "${corpus_dir}" ]; then
		corpus_count="$(find "${corpus_dir}" -type f | wc -l | tr -d ' ')"
	fi
	if [ "${corpus_count}" -eq 0 ]; then
		fail "${fuzzer} must ship a non-empty committed seed corpus at ${corpus_dir}/"
	else
		while IFS= read -r corpus_file; do
			[ "$(head -n 1 "${corpus_file}")" = "go test fuzz v1" ] ||
				fail "${corpus_file} is missing the 'go test fuzz v1' header, so the engine ignores it"
			corpus_size="$(wc -c <"${corpus_file}" | tr -d ' ')"
			[ "${corpus_size}" -le "${corpus_max_bytes}" ] ||
				fail "${corpus_file} is ${corpus_size} bytes; seed entries are capped at ${corpus_max_bytes}"
		done < <(find "${corpus_dir}" -type f)
	fi
done

# Corpus persistence across the two scheduled lanes. Everything below is a
# mechanical line the workflows have to agree on; the reasoning for each choice
# lives in the workflow comments, not here.
cache_action_sha="55cc8345863c7cc4c66a329aec7e433d2d1c52a9"
upload_artifact_sha="043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
cache_key='          key: fuzz-corpus-v1-${{ runner.os }}-${{ matrix.fuzzer.name }}-${{ github.run_id }}-${{ github.run_attempt }}'
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
cache_restore_prefix='            fuzz-corpus-v1-${{ runner.os }}-${{ matrix.fuzzer.name }}-'
cache_save_guard="        if: always() && steps.corpus.outputs.generated != ''"

# Crash classification (PW-5.10): scripts/ci/fuzz-run.sh is the single owner
# of the retry/classify loop for all four callers — lefthook.yml,
# scripts/ci/go-fuzz.sh, quality-fuzz-nightly.yml and quality-fuzz-monthly.yml
# — instead of each one reimplementing it. Go prints a different message once
# a crasher already lives in the committed seed corpus (re-tested before
# anything new runs) than it does on first discovery, so the shared script
# has to grep both or every run after the first silently falls through to
# kind=error.
fuzz_run_script="scripts/ci/fuzz-run.sh"
crash_pattern_first_discovery="Failing input written to testdata"
crash_pattern_seed_regression="failure while testing seed corpus entry"

# Every check below runs against comment-stripped content, not the raw file:
# a caller could otherwise satisfy "must invoke fuzz-run.sh" or "must not
# reimplement the classifier" by leaving a comment behind — e.g. commenting
# out the real call while keeping a comment that mentions the script path, or
# discussing a crash phrase in prose. Both the bash `run:` blocks and the
# YAML around them use `#` for a full-line comment, so one strip covers all
# four caller files.
strip_comments() {
	grep -Ev '^[[:space:]]*#' "$1"
}

fuzz_run_script_code="$(strip_comments "${fuzz_run_script}")"

# Anchored to the classifier's own `if grep -q ...` line, not just "appears
# somewhere in the file": both phrases are echoed again in the script's own
# human-facing messages, so a bare `grep -Fq` would still pass after the
# classifier condition itself stopped matching.
# shellcheck disable=SC2016 # Asserting the literal text of the script.
crash_classifier_first_discovery='	if grep -q "Failing input written to testdata" "${attempt_log}"; then'
# shellcheck disable=SC2016 # Asserting the literal text of the script.
crash_classifier_seed_regression='	if grep -q "failure while testing seed corpus entry" "${attempt_log}"; then'

grep -Fxq "${crash_classifier_first_discovery}" <<<"${fuzz_run_script_code}" ||
	fail "${fuzz_run_script} must classify '${crash_pattern_first_discovery}' as kind=crash in the classifier condition itself"
grep -Fxq "${crash_classifier_seed_regression}" <<<"${fuzz_run_script_code}" ||
	fail "${fuzz_run_script} must classify '${crash_pattern_seed_regression}' as kind=crash in the classifier condition itself"

# None of the four callers may reimplement the classifier inline — that is
# exactly the duplication PW-5.10 removed, and a caller that grows its own
# copy of either phrase can silently drift from the shared one. Matched as
# "grep" and the phrase on the same non-comment line, in any quoting style
# (double quotes, single quotes, or none) — not the bare phrase text: the
# nightly/monthly step-summary steps legitimately echo the same phrases into
# their human-facing output, and that is reporting, not reclassification, and
# it never puts the word "grep" on that line.
#
# Each caller must instead invoke the shared script. The invocation is
# anchored to a non-comment line that starts (after leading whitespace) with
# the FUZZ_RETRIES= env-assignment prefix every caller leads its call with,
# and separately to a non-comment `bash .../fuzz-run.sh` line — not just "the
# path or FUZZ_RETRIES=2 appear somewhere in the file", which a stale comment
# can satisfy on its own once a caller's real call is commented out.
for caller in lefthook.yml scripts/ci/go-fuzz.sh .github/workflows/quality-fuzz-nightly.yml .github/workflows/quality-fuzz-monthly.yml; do
	caller_code="$(strip_comments "${caller}")"

	grep -Eq "grep.*${crash_pattern_first_discovery}" <<<"${caller_code}" &&
		fail "${caller} must not reimplement the '${crash_pattern_first_discovery}' classifier inline; it must call ${fuzz_run_script}"
	grep -Eq "grep.*${crash_pattern_seed_regression}" <<<"${caller_code}" &&
		fail "${caller} must not reimplement the '${crash_pattern_seed_regression}' classifier inline; it must call ${fuzz_run_script}"
	grep -Eq 'bash[[:space:]]+.*fuzz-run\.sh' <<<"${caller_code}" ||
		fail "${caller} must invoke ${fuzz_run_script} rather than its own retry/classify loop"
	grep -Eq '^[[:space:]]*FUZZ_RETRIES=2([[:space:]]|\\$|"|$)' <<<"${caller_code}" ||
		fail "${caller} must pass FUZZ_RETRIES=2 explicitly to ${fuzz_run_script}, anchored at the call itself"
done

for workflow in .github/workflows/quality-fuzz-nightly.yml .github/workflows/quality-fuzz-monthly.yml; do
	grep -Fq "uses: actions/cache/restore@${cache_action_sha}" "${workflow}" ||
		fail "${workflow} must restore the fuzz corpus from actions/cache pinned to ${cache_action_sha}"
	grep -Fq "uses: actions/cache/save@${cache_action_sha}" "${workflow}" ||
		fail "${workflow} must save the fuzz corpus to actions/cache pinned to ${cache_action_sha}"

	# Restore and save have to name the same key. If they drift, every run
	# writes an entry the next run cannot find and the lane is back to zero.
	key_uses="$(grep -Fxc "${cache_key}" "${workflow}" || true)"
	[ "${key_uses}" -eq 2 ] ||
		fail "${workflow} must use one run-scoped corpus cache key on both restore and save (found ${key_uses})"

	# No workflow name in the prefix, on purpose: a cache entry is deleted
	# after 7 days without an access, so the monthly lane can only ever hit a
	# key the nightly keeps warm.
	grep -Fxq "${cache_restore_prefix}" "${workflow}" ||
		fail "${workflow} must share the nightly/monthly corpus restore-keys prefix"

	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	corpus_path='            ${{ steps.corpus.outputs.generated }}'
	path_uses="$(grep -Fxc "${corpus_path}" "${workflow}" || true)"
	[ "${path_uses}" -eq 2 ] ||
		fail "${workflow} must cache ${corpus_path# *} on both restore and save (found ${path_uses})"

	# Regression guard: the git-tracked seed corpus must never be cached
	# alongside the generated corpus. actions/cache restore extracts over the
	# checkout without cleaning the destination, so a stale cache entry would
	# silently outrank HEAD for that directory.
	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	seed_in_cache="$(grep -Fxc '            ${{ steps.corpus.outputs.seed }}' "${workflow}" || true)"
	[ "${seed_in_cache}" -eq 0 ] ||
		fail "${workflow} must not cache the git-tracked seed corpus; the cache restore extracts over the checkout and would outrank HEAD"

	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	grep -Fq 'generated="$(go env GOCACHE)/fuzz/${module}/${rel}/${FUZZER}"' "${workflow}" ||
		fail "${workflow} must resolve the generated corpus to \$GOCACHE/fuzz/<import path>/<Target>"
	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	grep -Fq 'seed="${rel}/testdata/fuzz/${FUZZER}"' "${workflow}" ||
		fail "${workflow} must resolve the seed corpus to <pkg>/testdata/fuzz/<Target>"

	grep -Fxq "${cache_save_guard}" "${workflow}" ||
		fail "${workflow} must save the corpus on failure too — a crasher is the finding worth keeping"

	# harden-runner fails these transfers silently: actions/cache warns and the
	# job stays green while persisting nothing.
	for endpoint in '*.actions.githubusercontent.com:443' '*.blob.core.windows.net:443' 'results-receiver.actions.githubusercontent.com:443'; do
		grep -Fxq "            ${endpoint}" "${workflow}" ||
			fail "${workflow} must allow ${endpoint} or the corpus transfer is blocked without failing"
	done

	# Corpus persistence must not have cost the lane its least-privilege token.
	perms_declarations="$(grep -Ec '^[[:space:]]*permissions:' "${workflow}" || true)"
	[ "${perms_declarations}" -eq 1 ] ||
		fail "${workflow} must declare permissions exactly once (found ${perms_declarations})"
	perms_block="$(awk '/^permissions:$/ { inside = 1; next } inside && /^[^[:space:]]/ { exit } inside && NF { print }' "${workflow}")"
	[ "${perms_block}" = "  contents: read" ] ||
		fail "${workflow} permissions must stay exactly 'contents: read'"

	# Nightly and monthly restore from and save to the same per-fuzzer cache
	# prefix, so a manual workflow_dispatch of one can race the other's
	# scheduled run for the same fuzzer's cache entry outside the cron
	# spacing that keeps the two routine schedules apart. Both workflows'
	# corpus-touching jobs have to share one job-level concurrency group,
	# keyed on the fuzzer name the cache key itself uses (runner.os isn't a
	# valid context in a concurrency expression), with cancel-in-progress:
	# false so the second writer queues instead of either run being cancelled
	# mid-save.
	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	corpus_concurrency_group='      group: quality-fuzz-corpus-${{ matrix.fuzzer.name }}'
	grep -Fxq "${corpus_concurrency_group}" "${workflow}" ||
		fail "${workflow} corpus-touching job must share the concurrency group quality-fuzz-corpus-\${{ matrix.fuzzer.name }} with the other fuzz-corpus workflow"
	grep -Fxq "      cancel-in-progress: false" "${workflow}" ||
		fail "${workflow} corpus-touching job's concurrency group must set cancel-in-progress: false so a racing writer queues instead of being cancelled"

	# The if: guard has to sit ON the upload-artifact step itself, not just
	# appear somewhere in the file — a guard attached to the wrong step would
	# match a bare `grep -Fxq` just as well and silently stop protecting the
	# corpus artifact.
	upload_step_block="$(awk '
		/^      - name: Upload fuzz corpus on failure or cancel$/ { inside = 1; print; next }
		inside && /^      - name:/ { exit }
		inside { print }
	' "${workflow}")"
	[ -n "${upload_step_block}" ] ||
		fail "${workflow} must have an 'Upload fuzz corpus on failure or cancel' step"
	grep -Fxq "        if: failure() || cancelled()" <<<"${upload_step_block}" ||
		fail "${workflow} must guard the 'Upload fuzz corpus on failure or cancel' step with if: failure() || cancelled()"
	grep -Fq "uses: actions/upload-artifact@${upload_artifact_sha}" <<<"${upload_step_block}" ||
		fail "${workflow} must upload the corpus artifact with actions/upload-artifact pinned to ${upload_artifact_sha} in the same step"
done

# Monthly/nightly cron overlap. Both lanes restore from and save to the same
# corpus cache prefix, and restore-keys picks whichever entry is newest, so if
# the monthly 360m job is still running when the nightly starts, the lane that
# saves second silently discards the other's coverage. The monthly cron hour
# has to leave at least 6 hours (360m + margin) before the nightly cron hour.
cron_hour() {
	awk -F"'" '/^[[:space:]]*- cron: /{print $2; exit}' "$1" | awk '{print $2}'
}
nightly_cron_hour="$(cron_hour .github/workflows/quality-fuzz-nightly.yml)"
monthly_cron_hour="$(cron_hour .github/workflows/quality-fuzz-monthly.yml)"
[ -n "${nightly_cron_hour}" ] ||
	fail "quality-fuzz-nightly.yml must declare a schedule.cron"
[ -n "${monthly_cron_hour}" ] ||
	fail "quality-fuzz-monthly.yml must declare a schedule.cron"
if [ -n "${nightly_cron_hour}" ] && [ -n "${monthly_cron_hour}" ]; then
	[ "$((10#${monthly_cron_hour} + 6))" -le "$((10#${nightly_cron_hour}))" ] ||
		fail "quality-fuzz-monthly.yml cron hour (${monthly_cron_hour}) + 6 must be <= quality-fuzz-nightly.yml cron hour (${nightly_cron_hour}), or the 360m monthly job can still be running when the nightly starts and restore-keys races the corpus cache"
fi

if [ "$failures" -ne 0 ]; then
	echo "${failures} fuzz tier contract check(s) failed" >&2
	exit 1
fi

echo "Fuzz tier contract checks passed."
