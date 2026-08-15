#!/usr/bin/env bash
set -euo pipefail

fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

git archive HEAD | tar -x -C "${fixture}"
cp scripts/package-release-config-test.sh "${fixture}/scripts/"
mkdir -p "${fixture}/scripts/ci"
cp scripts/ci/go-release-check.sh "${fixture}/scripts/ci/"
git -C "${fixture}" init -q
git -C "${fixture}" add .

if ! (cd "${fixture}" && bash scripts/package-release-config-test.sh >/dev/null); then
	echo "FAIL: complete package release fixture must pass" >&2
	exit 1
fi

sed '/bash scripts\/package-release-config-test.sh/d' \
	"${fixture}/scripts/ci/go-release-check.sh" >"${fixture}/scripts/ci/go-release-check.sh.tmp"
mv "${fixture}/scripts/ci/go-release-check.sh.tmp" "${fixture}/scripts/ci/go-release-check.sh"
set +e
adapter_output="$(cd "${fixture}" && bash scripts/package-release-config-test.sh 2>&1)"
adapter_status=$?
set -e
if [ "${adapter_status}" -eq 0 ] || ! grep -Fq \
	"FAIL: CI release adapter must enforce the package release contract" <<<"${adapter_output}"; then
	echo "FAIL: package release contract must reject a disconnected CI adapter" >&2
	exit 1
fi
cp scripts/ci/go-release-check.sh "${fixture}/scripts/ci/"

release_version="$(
	grep -E '^## \[v[0-9]+\.[0-9]+\.[0-9]+\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$' CHANGELOG.md |
		sed -n '1{s/^## \[v\([0-9.]*\)\].*/\1/p;}' || true
)"
if [ -z "${release_version}" ]; then
	echo "FAIL: could not derive release version from CHANGELOG.md" >&2
	exit 1
fi
prefix_version="${release_version}0"

awk -v from="VERSION=${release_version}" -v to="VERSION=${prefix_version}" '
	!changed && index($0, from) {
		sub(from, to)
		changed = 1
	}
	{ print }
	END { if (!changed) exit 1 }
' "${fixture}/README.md" >"${fixture}/README.md.tmp"
mv "${fixture}/README.md.tmp" "${fixture}/README.md"

set +e
validator_output="$(cd "${fixture}" && bash scripts/package-release-config-test.sh 2>&1)"
validator_status=$?
set -e
expected_diagnostic="FAIL: README release commands and asset names must use ${release_version}:"

if [ "${validator_status}" -eq 0 ]; then
	echo "FAIL: prefix version ${prefix_version} must not match current version ${release_version}" >&2
	exit 1
fi
if ! grep -Fq "${expected_diagnostic}" <<<"${validator_output}"; then
	echo "FAIL: prefix-version fixture failed without the expected stale README diagnostic" >&2
	echo "${validator_output}" >&2
	exit 1
fi

echo "Package release contract self-tests passed."
