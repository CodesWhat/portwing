#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

extract_step() {
	awk -v requested="$1" '
    $0 == "      - name: " requested { in_step = 1; next }
    in_step && /^      - name:/ { exit }
    in_step && $0 == "        run: |" { in_run = 1; next }
    in_run { sub(/^          /, ""); print }
' "${repo_root}/.github/workflows/release-cut.yml"
}

extract_step 'Compute next version' >"${fixture}/compute.sh"
extract_step 'Require the release tag to be the newest CHANGELOG entry' >"${fixture}/newest.sh"
if [ ! -s "${fixture}/compute.sh" ] || [ ! -s "${fixture}/newest.sh" ]; then
	echo "FAIL: could not extract the release version steps" >&2
	exit 1
fi

git -C "${fixture}" init -q
git -C "${fixture}" -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false commit -q --allow-empty -m 'chore: fixture release'
git -C "${fixture}" tag v0.9.26
git -C "${fixture}" -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false commit -q --allow-empty -m 'fix: fixture change'

run_version_step() {
	local supplied="$1"
	(cd "${fixture}" &&
		VERSION_INPUT="${supplied}" \
			LATEST_TAG=v0.9.26 \
			CURRENT_VERSION=0.9.26 \
			TARGET_SHA=HEAD \
			GITHUB_OUTPUT="${fixture}/output" \
			bash "${fixture}/compute.sh")
}

expect_version() {
	local supplied="$1"
	local expected="$2"
	local output
	: >"${fixture}/output"
	if ! output="$(run_version_step "${supplied}" 2>&1)"; then
		echo "FAIL: release cut must accept '${supplied}' as ${expected}" >&2
		echo "${output}" >&2
		exit 1
	fi
	if ! grep -Fxq "release_tag=v${expected}" "${fixture}/output"; then
		echo "FAIL: release cut must compute v${expected} for '${supplied}'" >&2
		cat "${fixture}/output" >&2
		exit 1
	fi
}

expect_rejection() {
	local supplied="$1"
	if run_version_step "${supplied}" >"${fixture}/failure" 2>&1; then
		echo "FAIL: release cut must reject '${supplied}'" >&2
		exit 1
	fi
}

expect_version 1.0.0-rc.1 1.0.0-rc.1
expect_version v1.0.0-rc.2 1.0.0-rc.2
expect_version 1.0.0-rc.0 1.0.0-rc.0
expect_version 1.0.0 1.0.0
expect_version '' 0.9.27
expect_rejection 1.0.0-rc.x
expect_rejection 1.0.0-rc.01
expect_rejection 01.0.0
expect_rejection 1.0.0-rc.1.extra
expect_rejection 0.9.26

cat >"${fixture}/CHANGELOG.md" <<'CHANGELOG'
# Changelog

## [Unreleased]

## [v1.0.0-rc.1] - 2026-10-15

### Changed

- Release candidate.

## [v0.9.26] - 2026-10-15

- Previous stable release.
CHANGELOG

if ! output="$(cd "${fixture}" && RELEASE_TAG=v1.0.0-rc.1 bash newest.sh 2>&1)"; then
	echo 'FAIL: the newest RC changelog entry must be accepted' >&2
	echo "${output}" >&2
	exit 1
fi
for older in v0.9.26 v1.0.0-rc.0 v1.0.0-rc.2; do
	if (cd "${fixture}" && RELEASE_TAG="${older}" bash newest.sh >failure 2>&1); then
		echo "FAIL: ${older} must not match the newest RC changelog entry" >&2
		exit 1
	fi
done

echo 'Release version step tests passed.'
