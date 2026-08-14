#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
validator="${script_dir}/validate-commit-range.sh"
test_root="$(mktemp -d)"
repo="${test_root}/repo"
output="${test_root}/output"
trap 'rm -rf "${test_root}"' EXIT

git init -q "${repo}"
git -C "${repo}" config user.name "Commit Range Test"
git -C "${repo}" config user.email "commit-range-test@example.com"

printf 'base\n' >"${repo}/fixture.txt"
git -C "${repo}" add fixture.txt
git -C "${repo}" commit -qm "chore: establish base"
base_commit="$(git -C "${repo}" rev-parse HEAD)"

printf 'valid\n' >>"${repo}/fixture.txt"
git -C "${repo}" commit -qam "ci: validate commit range"

if ! (cd "${repo}" && bash "${validator}" "${base_commit}") >"${output}" 2>&1; then
	echo "FAIL: valid commit range was rejected" >&2
	cat "${output}" >&2
	exit 1
fi

if (cd "${repo}" && bash "${validator}" refs/remotes/origin/missing) >"${output}" 2>&1; then
	echo "FAIL: unresolved base revision was accepted" >&2
	exit 1
fi
if ! grep -Fq "cannot resolve refs/remotes/origin/missing; commit validation did not run" "${output}"; then
	echo "FAIL: unresolved base revision did not report the fail-closed diagnostic" >&2
	cat "${output}" >&2
	exit 1
fi

if (cd "${repo}" && bash "${validator}" HEAD) >"${output}" 2>&1; then
	echo "FAIL: empty commit range was accepted" >&2
	exit 1
fi
if ! grep -Fq "no commits found in HEAD..HEAD; commit validation did not run" "${output}"; then
	echo "FAIL: empty commit range did not report the fail-closed diagnostic" >&2
	cat "${output}" >&2
	exit 1
fi

printf 'invalid\n' >>"${repo}/fixture.txt"
git -C "${repo}" commit -qam "not conventional"
if (cd "${repo}" && bash "${validator}" "${base_commit}") >"${output}" 2>&1; then
	echo "FAIL: non-conventional commit was accepted" >&2
	exit 1
fi
if ! grep -Fq "Non-conventional commit: not conventional" "${output}"; then
	echo "FAIL: non-conventional commit did not report its subject" >&2
	cat "${output}" >&2
	exit 1
fi

echo "Commit range validation checks passed."
