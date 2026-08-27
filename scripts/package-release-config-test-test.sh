#!/usr/bin/env bash
set -euo pipefail

fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

release_version="$(
	grep -E '^## \[v[0-9]+\.[0-9]+\.[0-9]+\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$' CHANGELOG.md |
		sed -n '1{s/^## \[v\([0-9.]*\)\].*/\1/p;}' || true
)"
if [ -z "${release_version}" ]; then
	echo "FAIL: could not derive release version from CHANGELOG.md" >&2
	exit 1
fi

restore_release_workflows() {
	cp .github/workflows/release.yml "${fixture}/.github/workflows/"
	cp .github/workflows/release-cut.yml "${fixture}/.github/workflows/"
}

git archive HEAD | tar -x -C "${fixture}"
cp scripts/package-release-config-test.sh "${fixture}/scripts/"
mkdir -p "${fixture}/scripts/ci"
cp scripts/ci/go-release-check.sh "${fixture}/scripts/ci/"
restore_release_workflows
git -C "${fixture}" init -q
git -C "${fixture}" add .

expect_release_contract_failure() {
	local expected="$1"
	local description="$2"
	local output
	local status

	set +e
	output="$(cd "${fixture}" && bash scripts/package-release-config-test.sh 2>&1)"
	status=$?
	set -e
	if [ "${status}" -eq 0 ] || ! grep -Fq "${expected}" <<<"${output}"; then
		echo "FAIL: ${description}" >&2
		echo "${output}" >&2
		exit 1
	fi
}

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

sed 's/workflow-file: ci-verify.yml/workflow-file: other.yml/' \
	"${fixture}/.github/workflows/release.yml" >"${fixture}/.github/workflows/release.yml.tmp"
mv "${fixture}/.github/workflows/release.yml.tmp" "${fixture}/.github/workflows/release.yml"
expect_release_contract_failure \
	"FAIL: the release prerequisite must query ci-verify.yml" \
	"the package release contract must reject a prerequisite that does not verify ci-verify.yml"
restore_release_workflows

awk '
	/target_sha=.*git rev-parse/ && !changed {
		sub(/\^\{commit\}/, "")
		changed = 1
	}
	{ print }
	END { if (!changed) exit 1 }
' "${fixture}/.github/workflows/release.yml" >"${fixture}/.github/workflows/release.yml.tmp"
mv "${fixture}/.github/workflows/release.yml.tmp" "${fixture}/.github/workflows/release.yml"
# shellcheck disable=SC2016 # Matching the validator's literal workflow diagnostic.
expect_release_contract_failure \
	'FAIL: the release prerequisite must resolve the pushed tag to a commit with git rev-parse "${GITHUB_REF}^{commit}"' \
	"the package release contract must reject a prerequisite that does not peel an annotated tag to its commit"
restore_release_workflows

sed '/git merge-base --is-ancestor.*origin\/main/s#origin/main#origin/not-main#' \
	"${fixture}/.github/workflows/release.yml" >"${fixture}/.github/workflows/release.yml.tmp"
mv "${fixture}/.github/workflows/release.yml.tmp" "${fixture}/.github/workflows/release.yml"
expect_release_contract_failure \
	"FAIL: release.yml must have a read-only prerequisite job that proves the tag commit is on origin/main and passed ci-verify.yml" \
	"the package release contract must reject a prerequisite that does not prove the tag commit is on origin/main"
restore_release_workflows

sed 's#target-sha: ${{ steps.target.outputs.sha }}#target-sha: ${{ github.sha }}#' \
	"${fixture}/.github/workflows/release.yml" >"${fixture}/.github/workflows/release.yml.tmp"
mv "${fixture}/.github/workflows/release.yml.tmp" "${fixture}/.github/workflows/release.yml"
expect_release_contract_failure \
	"FAIL: the release prerequisite must query CI for the exact commit resolved from the pushed tag" \
	"the package release contract must reject a CI query that bypasses the peeled tag output"
restore_release_workflows

sed 's/actions: read/actions: write/' \
	"${fixture}/.github/workflows/release.yml" >"${fixture}/.github/workflows/release.yml.tmp"
mv "${fixture}/.github/workflows/release.yml.tmp" "${fixture}/.github/workflows/release.yml"
expect_release_contract_failure \
	"FAIL: the release prerequisite must be unprivileged" \
	"the package release contract must reject write access in the release prerequisite"
restore_release_workflows

awk '
	$0 == "  release:" { in_release = 1 }
	in_release && /^    needs:/ && !removed { removed = 1; next }
	in_release && $0 != "  release:" && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { in_release = 0 }
	{ print }
	END { if (!removed) exit 1 }
' "${fixture}/.github/workflows/release.yml" >"${fixture}/.github/workflows/release.yml.tmp"
mv "${fixture}/.github/workflows/release.yml.tmp" "${fixture}/.github/workflows/release.yml"
expect_release_contract_failure \
	"FAIL: the privileged release job must depend on the read-only release prerequisite" \
	"the package release contract must reject a publish job disconnected from the prerequisite"
restore_release_workflows

sed 's/environment: Production/environment: Preview/' \
	"${fixture}/.github/workflows/release.yml" >"${fixture}/.github/workflows/release.yml.tmp"
mv "${fixture}/.github/workflows/release.yml.tmp" "${fixture}/.github/workflows/release.yml"
expect_release_contract_failure \
	"FAIL: the privileged release job must use the Production environment" \
	"the package release contract must reject a publish job outside the Production environment"
restore_release_workflows

sed '/git merge-base --is-ancestor.*origin\/main/s#origin/main#origin/not-main#' \
	"${fixture}/.github/workflows/release-cut.yml" >"${fixture}/.github/workflows/release-cut.yml.tmp"
mv "${fixture}/.github/workflows/release-cut.yml.tmp" "${fixture}/.github/workflows/release-cut.yml"
expect_release_contract_failure \
	"FAIL: release-cut.yml must prove TARGET_SHA is on origin/main before creating the release tag" \
	"the package release contract must reject a release cut that can tag a commit outside origin/main"
restore_release_workflows

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
