#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Re-scan the Go lockfile with osv-scanner on a tree-receipt run. The receipt
# reuses the dev run's Qlty result, but advisories move without a commit, so
# this is the one Qlty plugin worth repeating. Scope matches .qlty/qlty.toml:
# go.mod only (package-lock.json is excluded there on purpose), and the same
# triage ignores live in .github/osv-scanner.toml.
set -euo pipefail

module_directory="${MODULE_DIRECTORY:-.}"
case "${module_directory}" in
/* | *..*)
	echo "invalid MODULE_DIRECTORY: ${module_directory}" >&2
	exit 2
	;;
esac

cd "${module_directory}"
go run github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.7.1 scan source \
	--config .github/osv-scanner.toml \
	--lockfile go.mod
