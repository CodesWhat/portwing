#!/usr/bin/env bash
set -euo pipefail

fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

# Rebuilt from scratch before every mutation, so one negative case can never
# leave a broken file behind and make the next one pass for the wrong reason.
seed_fixture() {
	rm -rf "${fixture:?}"/*
	mkdir -p "${fixture}/scripts/ci" "${fixture}/.github/workflows" "${fixture}/.clusterfuzzlite"
	cp scripts/fuzz-tier-config-test.sh "${fixture}/scripts/"
	cp scripts/ci/go-fuzz.sh scripts/ci/fuzz-run.sh "${fixture}/scripts/ci/"
	cp lefthook.yml "${fixture}/"
	cp .github/workflows/ci-verify.yml "${fixture}/.github/workflows/"
	cp .github/workflows/quality-fuzz-nightly.yml "${fixture}/.github/workflows/"
	cp .github/workflows/quality-fuzz-monthly.yml "${fixture}/.github/workflows/"
	cp .clusterfuzzlite/build.sh "${fixture}/.clusterfuzzlite/"
	while IFS= read -r corpus_dir; do
		mkdir -p "${fixture}/${corpus_dir}"
		cp "${corpus_dir}"/* "${fixture}/${corpus_dir}/"
	done < <(find internal -type d -path '*/testdata/fuzz/*')
	# The files that declare the targets, so the inventory-vs-tree check has a
	# tree to read. Only the declaring files; nothing else compiles here.
	while IFS= read -r fuzz_file; do
		mkdir -p "${fixture}/$(dirname "${fuzz_file}")"
		cp "${fuzz_file}" "${fixture}/${fuzz_file}"
	done < <(grep -rlE '^func Fuzz[A-Za-z0-9_]*\(' --include='*_test.go' internal cmd || true)
}

expect_pass() {
	if ! (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null); then
		echo "FAIL: $1" >&2
		exit 1
	fi
}

expect_fail() {
	if (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null 2>&1); then
		echo "FAIL: $1" >&2
		exit 1
	fi
}

seed_fixture
expect_pass "valid fuzz tier fixture must pass"

seed_fixture
awk '
	/FuzzParseKeyLine \.\/internal\/auth\/"; do/ {
		print "        # \"FuzzParseKeyLine ./internal/auth/\""
		print "          \"FuzzUnrelated ./internal/auth/\"; do"
		next
	}
	{ print }
' "${fixture}/lefthook.yml" >"${fixture}/lefthook.yml.tmp"
mv "${fixture}/lefthook.yml.tmp" "${fixture}/lefthook.yml"
expect_fail "a comment outside the Lefthook entry list must not satisfy the contract"

seed_fixture
sed 's|"pkg":"./internal/auth/"|"pkg":"X/internal/auth/"|' \
	.github/workflows/ci-verify.yml >"${fixture}/.github/workflows/ci-verify.yml"
expect_fail "a malformed workflow mapping must not satisfy the contract"

# --- Seed corpus (PW-2.1) ---------------------------------------------------

seed_fixture
rm -rf "${fixture}/internal/protocol/testdata/fuzz/FuzzEnvelope"
expect_fail "a fuzzer with no committed seed corpus must not satisfy the contract"

seed_fixture
find "${fixture}/internal/protocol/testdata/fuzz/FuzzEnvelope" -type f -delete
expect_fail "an empty seed corpus directory must not satisfy the contract"

seed_fixture
printf 'string("nope")\n' >"${fixture}/internal/protocol/testdata/fuzz/FuzzEnvelope/headerless"
expect_fail "a corpus file without the 'go test fuzz v1' header must not satisfy the contract"

seed_fixture
{
	printf 'go test fuzz v1\nstring("'
	head -c 8192 /dev/zero | tr '\0' 'a'
	printf '")\n'
} >"${fixture}/internal/protocol/testdata/fuzz/FuzzEnvelope/oversized"
expect_fail "an oversized corpus file must not satisfy the contract"

# --- Corpus persistence (PW-2.1) --------------------------------------------

nightly="${fixture}/.github/workflows/quality-fuzz-nightly.yml"
monthly="${fixture}/.github/workflows/quality-fuzz-monthly.yml"

seed_fixture
sed 's|uses: actions/cache/restore@|uses: actions/cache/restore@v6 # |' "${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "an unpinned corpus cache restore must not satisfy the contract"

seed_fixture
# Only the second key line — the save step's — so the two genuinely disagree.
awk '
	/^          key: fuzz-corpus-v1-/ {
		seen++
		if (seen == 2) { print $0 "-save"; next }
	}
	{ print }
' "${monthly}" >"${monthly}.tmp"
mv "${monthly}.tmp" "${monthly}"
expect_fail "restore and save disagreeing on the cache key must not satisfy the contract"

seed_fixture
sed 's|^            fuzz-corpus-v1-\${{ runner.os }}-\${{ matrix.fuzzer.name }}-$|            fuzz-corpus-v1-monthly-\${{ matrix.fuzzer.name }}-|' \
	"${monthly}" >"${monthly}.tmp"
mv "${monthly}.tmp" "${monthly}"
expect_fail "a monthly-private restore-keys prefix must not satisfy the contract"

seed_fixture
sed '/^            \*\.blob\.core\.windows\.net:443$/d' "${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "dropping a cache egress endpoint must not satisfy the contract"

seed_fixture
sed "s|^        if: always() && steps.corpus.outputs.generated != ''$|        if: success()|" \
	"${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "a corpus save that skips failed runs must not satisfy the contract"

seed_fixture
sed 's|^    timeout-minutes: 75$|    timeout-minutes: 75\n    permissions:\n      actions: read|' \
	"${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "a job-level permissions grant must not satisfy the contract"

seed_fixture
sed '/^        if: failure() || cancelled()$/d' "${monthly}" >"${monthly}.tmp"
mv "${monthly}.tmp" "${monthly}"
expect_fail "dropping the on-failure corpus artifact upload must not satisfy the contract"

seed_fixture
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
sed 's|^            ${{ steps.corpus.outputs.generated }}$|&\n            ${{ steps.corpus.outputs.seed }}|' \
	"${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "caching the git-tracked seed corpus must not satisfy the contract"

# --- Crash classification lives in scripts/ci/fuzz-run.sh (PW-5.10) --------

fuzz_run="${fixture}/scripts/ci/fuzz-run.sh"
lefthook="${fixture}/lefthook.yml"
go_fuzz="${fixture}/scripts/ci/go-fuzz.sh"

seed_fixture
# The shared script losing the seed-corpus-regression phrase from its own
# classifier condition. first-discovery stays, so a classifier that only
# greps the old string would still pass this.
sed 's|failure while testing seed corpus entry|failure while testing seed corpus ENTRY|' \
	"${fuzz_run}" >"${fuzz_run}.tmp"
mv "${fuzz_run}.tmp" "${fuzz_run}"
expect_fail "scripts/ci/fuzz-run.sh losing the seed-corpus-regression classifier phrase must not satisfy the contract"

seed_fixture
# Same, for the first-discovery phrase.
sed 's|Failing input written to testdata|Failing input written to TESTDATA|' \
	"${fuzz_run}" >"${fuzz_run}.tmp"
mv "${fuzz_run}.tmp" "${fuzz_run}"
expect_fail "scripts/ci/fuzz-run.sh losing the first-discovery classifier phrase must not satisfy the contract"

seed_fixture
# A caller reimplementing the grep inline instead of calling the shared
# script — exactly the duplication PW-5.10 removed. lefthook.yml still calls
# fuzz-run.sh here too, so a bare "does this caller invoke the shared script"
# check would miss the reintroduced duplicate classifier entirely.
awk '
	/FUZZ_RETRIES=2 FUZZ_TIMEOUT=1m/ {
		print "          grep -q \"Failing input written to testdata\" /dev/null && true"
	}
	{ print }
' "${lefthook}" >"${lefthook}.tmp"
mv "${lefthook}.tmp" "${lefthook}"
expect_fail "a caller reimplementing the crash-phrase grep inline must not satisfy the contract"

seed_fixture
# A caller dropping the retry env — the explicit FUZZ_RETRIES=2 that makes
# the retry budget visible at the call site instead of relying silently on
# fuzz-run.sh's own default.
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
sed 's|FUZZ_RETRIES=2 FUZZ_OUTPUT_FILE="\$GITHUB_OUTPUT" FUZZ_LOG_FILE="fuzz-run-\${FUZZER}.log" bash scripts/ci/fuzz-run.sh|FUZZ_OUTPUT_FILE="$GITHUB_OUTPUT" FUZZ_LOG_FILE="fuzz-run-${FUZZER}.log" bash scripts/ci/fuzz-run.sh|' \
	"${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "a caller dropping the FUZZ_RETRIES retry env must not satisfy the contract"

seed_fixture
# go-fuzz.sh dropping the retry env too — a separate caller, separate line.
sed '/^FUZZ_RETRIES=2 \\$/d' "${go_fuzz}" >"${go_fuzz}.tmp"
mv "${go_fuzz}.tmp" "${go_fuzz}"
expect_fail "scripts/ci/go-fuzz.sh dropping the FUZZ_RETRIES retry env must not satisfy the contract"

seed_fixture
# A caller commenting out its real call while leaving a comment that
# mentions fuzz-run.sh's path — a bare "does the path appear in the file"
# check (or "does FUZZ_RETRIES=2 appear in the file") is satisfiable by the
# leftover comment alone once the actual invocation is gone.
awk '
	/FUZZ_RETRIES=2 FUZZ_TIMEOUT=1m/ {
		print "        # was: calls scripts/ci/fuzz-run.sh with FUZZ_RETRIES=2"
		print "        # " $0
		next
	}
	{ print }
' "${lefthook}" >"${lefthook}.tmp"
mv "${lefthook}.tmp" "${lefthook}"
expect_fail "a caller with its real call commented out, and a comment mentioning the script path in its place, must not satisfy the contract"

seed_fixture
# A caller reimplementing the crash-phrase grep inline with single quotes —
# the same duplication as above, but in a quoting style the old classifier
# check (anchored to a double-quoted grep only) would have missed.
inline_grep_line="grep -q 'failure while testing seed corpus entry' /dev/null && true"
awk -v ins="${inline_grep_line}" '
	/FUZZ_RETRIES=2 \\$/ { print; print ins; next }
	{ print }
' "${go_fuzz}" >"${go_fuzz}.tmp"
mv "${go_fuzz}.tmp" "${go_fuzz}"
expect_fail "a caller reimplementing the crash-phrase grep inline with single quotes must not satisfy the contract"

seed_fixture
# A caller keeping the anchored FUZZ_RETRIES=2 prefix and the
# bash .../fuzz-run.sh text on the same line, but as a trailing comment
# after a real command that never runs it — strip_comments only dropped
# full-line comments, so this line's non-comment prefix
# (`FUZZ_RETRIES=2 true`) satisfied the anchored FUZZ_RETRIES check and the
# whole line's text satisfied the invocation check, while `true` (not
# fuzz-run.sh) is what actually runs.
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
sed 's|FUZZ_RETRIES=2 FUZZ_TIMEOUT=1m FUZZ_PARALLEL="\$workers" bash scripts/ci/fuzz-run.sh "\$2" "\$1" 5s|FUZZ_RETRIES=2 true # bash scripts/ci/fuzz-run.sh "\$2" "\$1" 5s|' \
	"${lefthook}" >"${lefthook}.tmp"
mv "${lefthook}.tmp" "${lefthook}"
expect_fail "a caller with the real call reduced to a trailing comment after an unrelated command must not satisfy the contract"

# --- Monthly/nightly cron overlap (PW codex follow-up #2) -------------------

seed_fixture
# 5 + 6 = 11 > 9 (nightly), so the 360m monthly job can still be running when
# the nightly starts and the two lanes race the shared corpus cache prefix.
sed "s|30 2 1 \* \*|30 5 1 * *|" "${monthly}" >"${monthly}.tmp"
mv "${monthly}.tmp" "${monthly}"
expect_fail "a monthly cron less than 6 hours ahead of the nightly cron must not satisfy the contract"

# --- Upload-artifact if: scoped to its own step (PW codex follow-up #3) -----

seed_fixture
# Move the if: guard off the upload-artifact step and onto a neighboring step
# with a different if:, so a bare "does this string exist anywhere" check
# would still pass while the artifact upload itself runs unconditionally.
awk '
	/^      - name: Upload fuzz corpus on failure or cancel$/ { print; getline; next }
	/^      - name: Save fuzz corpus$/ { print; print "        if: failure() || cancelled()"; next }
	{ print }
' "${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "an if: guard attached to a different step must not satisfy the contract"

# --- Failure-upload before scoring (codex follow-up #3) ---------------------

seed_fixture
# Duplicate the 'Score corpus coverage' step immediately ahead of 'Upload
# fuzz corpus on failure or cancel', leaving the original step untouched in
# its rightful place further down. step_line() takes the FIRST match, so this
# makes the contract see 'Score corpus coverage' as running before the
# failure-upload step even though every other check (step exists, runs
# if: always(), invokes fuzz-score.sh, comes after Save fuzz corpus) still
# passes — only the ordering check between these two specific steps notices.
score_block_file="${fixture}/score-block.txt"
awk '
	$0 == "      - name: Score corpus coverage" { inside = 1; print; next }
	inside && /^      - name:/ { exit }
	inside { print }
' "${nightly}" >"${score_block_file}"
# Read the captured block back with getline rather than passing it through
# -v: awk -v does not accept an embedded newline in the assigned value on
# every awk this repo has to run under.
awk -v blockfile="${score_block_file}" '
	$0 == "      - name: Upload fuzz corpus on failure or cancel" {
		while ((getline line < blockfile) > 0) {
			print line
		}
		close(blockfile)
	}
	{ print }
' "${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "'Score corpus coverage' running before 'Upload fuzz corpus on failure or cancel' must not satisfy the contract"

# --- Corpus writer concurrency (review follow-up) ---------------------------

seed_fixture
# Nightly keeps the correct shared group; monthly's diverges, so the two
# workflows' corpus-touching jobs no longer serialise against each other.
sed 's|      group: quality-fuzz-corpus-\${{ matrix.fuzzer.name }}|      group: quality-fuzz-corpus-monthly-${{ matrix.fuzzer.name }}|' \
	"${monthly}" >"${monthly}.tmp"
mv "${monthly}.tmp" "${monthly}"
expect_fail "a corpus-touching job whose concurrency group doesn't match the other fuzz-corpus workflow's must not satisfy the contract"

seed_fixture
# 6-space indent is the job-level concurrency block; the workflow-level one
# above it is indented 2 spaces and must stay untouched by this mutation.
sed 's|^      cancel-in-progress: false$|      cancel-in-progress: true|' \
	"${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "a corpus-touching job's concurrency group with cancel-in-progress: true must not satisfy the contract"

seed_fixture
# A target dropped from the ClusterFuzzLite build while the four Go-engine tiers
# still run it. This is the drift the tier is most likely to acquire, because
# build.sh is the only inventory that is not a workflow file.
grep -v '^build_fuzzer internal/auth FuzzParseKeyLine$' \
	.clusterfuzzlite/build.sh >"${fixture}/.clusterfuzzlite/build.sh"
expect_fail "a fuzzer missing from .clusterfuzzlite/build.sh must not satisfy the contract"

seed_fixture
# An eleventh target built for libFuzzer that no other tier runs. Every
# per-fuzzer check still passes; only the count notices.
{
	cat .clusterfuzzlite/build.sh
	echo 'build_fuzzer internal/auth FuzzUnrelated'
} >"${fixture}/.clusterfuzzlite/build.sh"
expect_fail "an extra, unaccounted-for ClusterFuzzLite target must not satisfy the contract"

seed_fixture
# The right target built from the wrong package.
sed 's|^build_fuzzer internal/server FuzzParsePHC$|build_fuzzer internal/servers FuzzParsePHC|' \
	.clusterfuzzlite/build.sh >"${fixture}/.clusterfuzzlite/build.sh"
expect_fail "a ClusterFuzzLite target built from the wrong package must not satisfy the contract"

seed_fixture
# An eleventh target declared in the tree and listed in no tier at all. Every
# per-fuzzer check still passes, because they all iterate the inventory.
printf '\nfunc FuzzUnlisted(f *testing.F) {}\n' >>"${fixture}/internal/auth/keys_fuzz_test.go"
expect_fail "a Fuzz target declared in the tree but listed in no tier must not satisfy the contract"

seed_fixture
# The other direction: an inventory entry whose target no longer exists. The
# workflow greps still find its name in every tier, so only this check notices.
rm -f "${fixture}/internal/auth/keys_fuzz_test.go"
expect_fail "an inventory entry with no declaration left in the tree must not satisfy the contract"

echo "Fuzz tier contract self-tests passed."
