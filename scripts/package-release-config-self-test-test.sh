#!/usr/bin/env bash
set -euo pipefail

failures=0

new_fixture() {
	local fixture

	fixture="$(mktemp -d "${fixture_root}/fixture.XXXXXX")" || return
	git archive HEAD | tar -x -C "${fixture}" || return
	cp scripts/package-release-config-test-test.sh "${fixture}/scripts/" || return
	cp scripts/package-release-config-test.sh "${fixture}/scripts/" || return
	mkdir -p "${fixture}/scripts/ci" || return
	cp scripts/ci/go-release-check.sh "${fixture}/scripts/ci/" || return
	git -C "${fixture}" init -q || return
	git -C "${fixture}" config user.email "contract-test@codeswhat.com" || return
	git -C "${fixture}" config user.name "Contract Test" || return
	git -C "${fixture}" add . || return
	git -C "${fixture}" commit -qm "test fixture" || return
	printf '%s\n' "${fixture}"
}

fixture_root="$(mktemp -d)"
missing_heading_fixture=''
unrelated_failure_fixture=''
missing_builder_fixture=''
trap 'rm -rf "${fixture_root}" "${missing_heading_fixture}" "${unrelated_failure_fixture}" "${missing_builder_fixture}"' EXIT

missing_heading_fixture="$(new_fixture)"
unrelated_failure_fixture="$(new_fixture)"
missing_builder_fixture="$(new_fixture)"

sed -E 's/^## \[v([0-9]+\.[0-9]+\.[0-9]+)\] - ([0-9]{4}-[0-9]{2}-[0-9]{2})$/## [v\1] (\2)/' \
	"${missing_heading_fixture}/CHANGELOG.md" >"${missing_heading_fixture}/CHANGELOG.md.tmp"
mv "${missing_heading_fixture}/CHANGELOG.md.tmp" "${missing_heading_fixture}/CHANGELOG.md"

set +e
missing_heading_output="$(
	cd "${missing_heading_fixture}" && bash scripts/package-release-config-test-test.sh 2>&1
)"
missing_heading_status=$?
set -e
if [ "${missing_heading_status}" -eq 0 ] ||
	! grep -Fq "FAIL: could not derive release version from CHANGELOG.md" <<<"${missing_heading_output}"; then
	echo "FAIL: the self-test must explicitly reject a missing release heading" >&2
	failures=$((failures + 1))
fi

printf '%s\n' '#!/usr/bin/env bash' 'exit 42' >"${unrelated_failure_fixture}/scripts/package-release-config-test.sh"

if (cd "${unrelated_failure_fixture}" && bash scripts/package-release-config-test-test.sh >/dev/null 2>&1); then
	echo "FAIL: an unrelated validator error must not satisfy the prefix-version self-test" >&2
	failures=$((failures + 1))
fi

sed '/^FROM golang:/d' "${missing_builder_fixture}/Dockerfile" >"${missing_builder_fixture}/Dockerfile.tmp"
mv "${missing_builder_fixture}/Dockerfile.tmp" "${missing_builder_fixture}/Dockerfile"

set +e
missing_builder_output="$(
	cd "${missing_builder_fixture}" && bash scripts/package-release-config-test.sh 2>&1
)"
missing_builder_status=$?
set -e
if [ "${missing_builder_status}" -eq 0 ] ||
	! grep -Fq "FAIL: Dockerfile must use the exact go.mod toolchain in a digest-pinned Alpine builder" <<<"${missing_builder_output}"; then
	echo "FAIL: the validator must report a missing Go builder" >&2
	failures=$((failures + 1))
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} package release contract self-test check(s) failed" >&2
	exit 1
fi

echo "Package release contract self-test checks passed."
