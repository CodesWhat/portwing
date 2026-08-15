#!/usr/bin/env bash
set -euo pipefail

module_directory="${MODULE_DIRECTORY:-.}"
case "${module_directory}" in
/* | *..*)
	echo "invalid MODULE_DIRECTORY: ${module_directory}" >&2
	exit 2
	;;
esac

GOLANGCI_LINT_CACHE="$(mktemp -d "${TMPDIR:-/tmp}/portwing-golangci-lint.XXXXXX")"
export GOLANGCI_LINT_CACHE
trap 'rm -rf "${GOLANGCI_LINT_CACHE}"' EXIT

cd "${module_directory}"
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run
