#!/usr/bin/env bash
set -euo pipefail

fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

mkdir -p "${fixture}/scripts" "${fixture}/.github/workflows"
cp scripts/fuzz-tier-config-test.sh "${fixture}/scripts/"
cp lefthook.yml "${fixture}/"
cp .github/workflows/ci-verify.yml "${fixture}/.github/workflows/"
cp .github/workflows/quality-fuzz-nightly.yml "${fixture}/.github/workflows/"
cp .github/workflows/quality-fuzz-monthly.yml "${fixture}/.github/workflows/"

if ! (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null); then
	echo "FAIL: valid fuzz tier fixture must pass" >&2
	exit 1
fi

awk '
	/FuzzVerifyRequest \.\/internal\/auth\/"; do/ {
		print "        # \"FuzzVerifyRequest ./internal/auth/\""
		print "          \"FuzzUnrelated ./internal/auth/\"; do"
		next
	}
	{ print }
' "${fixture}/lefthook.yml" >"${fixture}/lefthook.yml.tmp"
mv "${fixture}/lefthook.yml.tmp" "${fixture}/lefthook.yml"

if (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null 2>&1); then
	echo "FAIL: a comment outside the Lefthook entry list must not satisfy the contract" >&2
	exit 1
fi

cp lefthook.yml "${fixture}/lefthook.yml"
sed 's|pkg: ./internal/auth/ }|pkg: X/internal/auth/ } # malformed|' \
	.github/workflows/ci-verify.yml >"${fixture}/.github/workflows/ci-verify.yml"

if (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null 2>&1); then
	echo "FAIL: a malformed workflow mapping must not satisfy the contract" >&2
	exit 1
fi

echo "Fuzz tier contract self-tests passed."
