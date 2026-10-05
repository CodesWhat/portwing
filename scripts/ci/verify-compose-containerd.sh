#!/usr/bin/env bash
set -euo pipefail

# Proves the docker-compose binary shipped in an image really embeds the
# containerd version the Dockerfile that built it pins.
#
# The Dockerfile pin (`go get github.com/containerd/containerd/v2@vX.Y.Z` in
# the compose-builder stage) is only text. A build that silently stayed on an
# older containerd still produces a working docker-compose, passes the smoke
# test, and sits under the image scan's high cutoff when the advisory is
# medium. The module version stamped into the binary is the one fact that
# says what was actually linked.
#
# Usage: verify-compose-containerd.sh <docker-compose-binary> <dockerfile>
#
# Expected version: read from the Dockerfile at run time, never hardcoded here.
# Actual version: the `dep` line for the module in `go version -m <binary>`.
#
# Fails closed. An unreadable binary or Dockerfile, a Dockerfile with no pin
# (or several different ones), a `go version -m` failure, a binary with no
# containerd module line (or a replaced one), and a mismatch all exit 1.

readonly module="github.com/containerd/containerd/v2"

if [[ $# -ne 2 ]]; then
	echo "usage: $0 <docker-compose-binary> <dockerfile>" >&2
	exit 2
fi
binary_path="$1"
dockerfile="$2"

if [[ ! -r ${binary_path} ]]; then
	echo "error: cannot inspect unreadable binary: ${binary_path}" >&2
	exit 1
fi
if [[ ! -r ${dockerfile} ]]; then
	echo "error: cannot read Dockerfile: ${dockerfile}" >&2
	exit 1
fi
if ! command -v go >/dev/null 2>&1; then
	echo "error: go is not on PATH, cannot read build info from ${binary_path}" >&2
	exit 1
fi

# Active lines only, so a stale `# was ...@v2.3.5` comment is not a pin.
pins="$(
	grep -vE '^[[:space:]]*#' "${dockerfile}" |
		grep -oE "${module//./\\.}@v[0-9]+\\.[0-9]+\\.[0-9]+([-+][0-9A-Za-z.+-]*)?" |
		sed "s|^${module}@||" | sort -u || true
)"
if [[ -z ${pins} ]]; then
	echo "error: ${dockerfile} has no ${module}@vX.Y.Z pin to compare against" >&2
	exit 1
fi
if [[ $(wc -l <<<"${pins}" | tr -d '[:space:]') -ne 1 ]]; then
	echo "error: ${dockerfile} pins ${module} to more than one version: $(tr '\n' ' ' <<<"${pins}")" >&2
	exit 1
fi
expected="${pins}"

if ! build_info="$(go version -m "${binary_path}" 2>&1)"; then
	echo "error: go version -m ${binary_path} failed: ${build_info}" >&2
	exit 1
fi

# Tab- or space-separated `dep <module> <version> <hash>`. A `=>` line right
# after it means the module was replaced, and then the dep version is not what
# got linked.
actual="$(
	awk -v mod="${module}" '
		$1 == "dep" && $2 == mod { print $3; found = 1; next }
		found && $1 == "=>" { print "replaced"; exit }
		{ found = 0 }
	' <<<"${build_info}"
)"
if [[ -z ${actual} ]]; then
	echo "error: no ${module} module line in go version -m output for ${binary_path}" >&2
	exit 1
fi
if [[ $(wc -l <<<"${actual}" | tr -d '[:space:]') -ne 1 || ${actual} == *replaced* ]]; then
	echo "error: ${module} is listed more than once or replaced in ${binary_path}, cannot verify the linked version" >&2
	exit 1
fi

if [[ ${actual} != "${expected}" ]]; then
	echo "error: ${binary_path} embeds ${module} ${actual}, but ${dockerfile} pins ${expected}; the build did not pick up the pinned containerd" >&2
	exit 1
fi

echo "verified: ${binary_path} embeds ${module} ${actual}, matching the ${dockerfile} pin"
