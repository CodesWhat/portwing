#!/usr/bin/env bash
set -euo pipefail

fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

# Rebuilt from scratch before every mutation, so one negative case can never
# leave a broken file behind and make the next one pass for the wrong reason.
seed_fixture() {
	rm -rf "${fixture:?}"/*
	mkdir -p "${fixture}/scripts" "${fixture}/.github/workflows"
	cp scripts/fuzz-tier-config-test.sh "${fixture}/scripts/"
	cp lefthook.yml "${fixture}/"
	cp .github/workflows/ci-verify.yml "${fixture}/.github/workflows/"
	cp .github/workflows/quality-fuzz-nightly.yml "${fixture}/.github/workflows/"
	cp .github/workflows/quality-fuzz-monthly.yml "${fixture}/.github/workflows/"
	while IFS= read -r corpus_dir; do
		mkdir -p "${fixture}/${corpus_dir}"
		cp "${corpus_dir}"/* "${fixture}/${corpus_dir}/"
	done < <(find internal -type d -path '*/testdata/fuzz/*')
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

# --- Crash classification (PW codex follow-up #1) ---------------------------

seed_fixture
# Only the seed-corpus-regression string goes missing; first-discovery stays,
# so a classifier that only greps the old string would still pass this.
sed 's|failure while testing seed corpus entry|failure while testing seed corpus ENTRY|' \
	"${nightly}" >"${nightly}.tmp"
mv "${nightly}.tmp" "${nightly}"
expect_fail "a workflow that stops classifying a seed-corpus regression as a crash must not satisfy the contract"

seed_fixture
sed 's|failure while testing seed corpus entry|failure while testing seed corpus ENTRY|' \
	"${monthly}" >"${monthly}.tmp"
mv "${monthly}.tmp" "${monthly}"
expect_fail "a workflow that stops classifying a seed-corpus regression as a crash must not satisfy the contract"

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

echo "Fuzz tier contract self-tests passed."
