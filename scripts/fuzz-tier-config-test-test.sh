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

echo "Fuzz tier contract self-tests passed."
