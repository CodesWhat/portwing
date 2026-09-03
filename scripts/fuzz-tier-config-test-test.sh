#!/usr/bin/env bash
set -euo pipefail

fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

mkdir -p "${fixture}/scripts" "${fixture}/.github/workflows" "${fixture}/.clusterfuzzlite"
cp scripts/fuzz-tier-config-test.sh "${fixture}/scripts/"
cp lefthook.yml "${fixture}/"
cp .github/workflows/ci-verify.yml "${fixture}/.github/workflows/"
cp .github/workflows/quality-fuzz-nightly.yml "${fixture}/.github/workflows/"
cp .github/workflows/quality-fuzz-monthly.yml "${fixture}/.github/workflows/"
cp .clusterfuzzlite/build.sh "${fixture}/.clusterfuzzlite/"

if ! (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null); then
	echo "FAIL: valid fuzz tier fixture must pass" >&2
	exit 1
fi

awk '
	/FuzzParseKeyLine \.\/internal\/auth\/"; do/ {
		print "        # \"FuzzParseKeyLine ./internal/auth/\""
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
sed 's|"pkg":"./internal/auth/"|"pkg":"X/internal/auth/"|' \
	.github/workflows/ci-verify.yml >"${fixture}/.github/workflows/ci-verify.yml"

if (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null 2>&1); then
	echo "FAIL: a malformed workflow mapping must not satisfy the contract" >&2
	exit 1
fi

cp .github/workflows/ci-verify.yml "${fixture}/.github/workflows/ci-verify.yml"

# A target dropped from the ClusterFuzzLite build while the four Go-engine tiers
# still run it. This is the drift the tier is most likely to acquire, because
# build.sh is the only inventory that is not a workflow file.
grep -v '^build_fuzzer internal/auth FuzzParseKeyLine$' \
	.clusterfuzzlite/build.sh >"${fixture}/.clusterfuzzlite/build.sh"

if (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null 2>&1); then
	echo "FAIL: a fuzzer missing from .clusterfuzzlite/build.sh must not satisfy the contract" >&2
	exit 1
fi

# An eleventh target built for libFuzzer that no other tier runs. Every
# per-fuzzer check still passes; only the count notices.
{
	cat .clusterfuzzlite/build.sh
	echo 'build_fuzzer internal/auth FuzzUnrelated'
} >"${fixture}/.clusterfuzzlite/build.sh"

if (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null 2>&1); then
	echo "FAIL: an extra, unaccounted-for ClusterFuzzLite target must not satisfy the contract" >&2
	exit 1
fi

# The right target built from the wrong package.
sed 's|^build_fuzzer internal/server FuzzParsePHC$|build_fuzzer internal/servers FuzzParsePHC|' \
	.clusterfuzzlite/build.sh >"${fixture}/.clusterfuzzlite/build.sh"

if (cd "${fixture}" && bash scripts/fuzz-tier-config-test.sh >/dev/null 2>&1); then
	echo "FAIL: a ClusterFuzzLite target built from the wrong package must not satisfy the contract" >&2
	exit 1
fi

echo "Fuzz tier contract self-tests passed."
