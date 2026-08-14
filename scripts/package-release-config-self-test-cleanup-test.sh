#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d)"
trap 'rm -rf "${test_root}"' EXIT

repo_fixture="${test_root}/repo"
generated_fixtures="${test_root}/generated"
wrapper_dir="${test_root}/bin"
mkdir -p "${repo_fixture}" "${generated_fixtures}" "${wrapper_dir}"
git archive HEAD | tar -x -C "${repo_fixture}"
cp scripts/package-release-config-self-test-test.sh "${repo_fixture}/scripts/"

real_mktemp="$(command -v mktemp)"
# The wrapper expands these variables when it runs in the fixture process.
# shellcheck disable=SC2016
printf '%s\n' \
	'#!/usr/bin/env bash' \
	'set -euo pipefail' \
	'count=0' \
	'if [ -f "${MKTEMP_COUNT_FILE}" ]; then read -r count <"${MKTEMP_COUNT_FILE}"; fi' \
	'count=$((count + 1))' \
	'printf "%s\n" "${count}" >"${MKTEMP_COUNT_FILE}"' \
	'if [ "${count}" -eq 2 ]; then exit 97; fi' \
	'exec "${REAL_MKTEMP}" -d "${GENERATED_FIXTURES}/fixture.XXXXXX"' >"${wrapper_dir}/mktemp"
chmod +x "${wrapper_dir}/mktemp"

set +e
(
	cd "${repo_fixture}"
	MKTEMP_COUNT_FILE="${test_root}/mktemp-count" \
		REAL_MKTEMP="${real_mktemp}" \
		GENERATED_FIXTURES="${generated_fixtures}" \
		PATH="${wrapper_dir}:/usr/bin:/bin" \
		bash scripts/package-release-config-self-test-test.sh >/dev/null 2>&1
)
self_test_status=$?
set -e

if [ "${self_test_status}" -eq 0 ]; then
	echo "FAIL: fixture failure injection must stop the package release self-test" >&2
	exit 1
fi

mktemp_count="$(cat "${test_root}/mktemp-count")"
if [ "${mktemp_count}" -ne 2 ]; then
	echo "FAIL: fixture creation must stop at the injected second mktemp failure (got ${mktemp_count} calls)" >&2
	exit 1
fi

if find "${generated_fixtures}" -mindepth 1 -maxdepth 1 -type d -print -quit | grep -q .; then
	echo "FAIL: package release self-test must clean earlier fixtures after setup failure" >&2
	exit 1
fi

echo "Package release self-test cleanup checks passed."
