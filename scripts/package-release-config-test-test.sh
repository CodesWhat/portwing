#!/usr/bin/env bash
set -euo pipefail

fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

git archive HEAD | tar -x -C "${fixture}"
cp scripts/package-release-config-test.sh "${fixture}/scripts/"
git -C "${fixture}" init -q
git -C "${fixture}" add .

release_version="$(
	grep -E '^## \[v[0-9]+\.[0-9]+\.[0-9]+\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$' CHANGELOG.md |
		sed -n '1{s/^## \[v\([0-9.]*\)\].*/\1/p;}'
)"
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

if (cd "${fixture}" && bash scripts/package-release-config-test.sh >/dev/null 2>&1); then
	echo "FAIL: prefix version ${prefix_version} must not match current version ${release_version}" >&2
	exit 1
fi

echo "Package release contract self-tests passed."
