#!/usr/bin/env bash
set -euo pipefail

module_directory="${MODULE_DIRECTORY:-.}"
if [ "${module_directory}" != "." ]; then
	echo "Portwing Qlty checks require MODULE_DIRECTORY=." >&2
	exit 2
fi

./scripts/qlty-check-gate.sh all
