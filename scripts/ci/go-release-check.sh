#!/usr/bin/env bash
set -euo pipefail

module_directory="${MODULE_DIRECTORY:-.}"
if [ "${module_directory}" != "." ]; then
	echo "Portwing release checks require MODULE_DIRECTORY=." >&2
	exit 2
fi

go run github.com/goreleaser/goreleaser/v2@v2.17.1 check
bash scripts/package-release-config-self-test-test.sh
bash scripts/package-release-config-self-test-cleanup-test.sh
bash scripts/package-release-config-test-test.sh
bash scripts/package-release-config-test.sh
bash scripts/install-config-permissions-test.sh
bash scripts/standard-mode-bind-config-test.sh
bash scripts/setup-go-egress-config-test-test.sh
bash scripts/setup-go-egress-config-test.sh
bash scripts/pre-push-config-test-test.sh
bash scripts/pre-push-config-test.sh
bash scripts/fuzz-tier-config-test-test.sh
bash scripts/fuzz-tier-config-test.sh
bash scripts/codeql-trigger-config-test-test.sh
bash scripts/codeql-trigger-config-test.sh
bash scripts/drydock-compat-script-test.sh
