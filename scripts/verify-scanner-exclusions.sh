#!/usr/bin/env bash
set -euo pipefail

readonly -a excluded_packages=(
	"golang.org/x/crypto/openpgp"
	"github.com/klauspost/compress/s2"
)

if [[ $# -eq 0 ]]; then
	dependency_graph="$(go list -deps -test ./...)"
	for package_path in "${excluded_packages[@]}"; do
		if grep -Fxq "${package_path}" <<<"${dependency_graph}"; then
			echo "error: ${package_path} is present in the Portwing dependency graph" >&2
			exit 1
		fi
	done
	echo "verified: scoped scanner exclusions are absent from the Portwing dependency graph"
	exit 0
fi

for binary_path in "$@"; do
	if [[ ! -r ${binary_path} ]]; then
		echo "error: cannot inspect unreadable binary: ${binary_path}" >&2
		exit 1
	fi
	for package_path in "${excluded_packages[@]}"; do
		if LC_ALL=C grep -aFq "${package_path}" "${binary_path}"; then
			echo "error: ${package_path} is linked into ${binary_path}" >&2
			exit 1
		fi
	done
done

echo "verified: scoped scanner exclusions are absent from ${#} shipped binaries"
