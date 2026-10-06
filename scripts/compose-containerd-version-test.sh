#!/usr/bin/env bash
set -euo pipefail

# Exercises scripts/ci/verify-compose-containerd.sh against a fake `go` on
# PATH that replays canned `go version -m` output, so the fail-closed contract
# (match passes; mismatch, missing module line, missing pin and the other
# unverifiable shapes fail) is pinned without building a compose binary.

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
script="${repository_root}/scripts/ci/verify-compose-containerd.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

failures=0
fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

bin="${fixture}/bin"
mkdir -p "${bin}"
# The fake go replays $MOCK_BUILD_INFO for `go version -m <file>` and fails
# when $MOCK_GO_FAIL is set, mimicking a binary with no build info.
cat >"${bin}/go" <<'MOCK'
#!/usr/bin/env bash
if [ "${1:-}" != "version" ] || [ "${2:-}" != "-m" ]; then
	echo "unexpected go command: $*" >&2
	exit 90
fi
if [ -n "${MOCK_GO_FAIL:-}" ]; then
	echo "$3: could not read Go build info" >&2
	exit 1
fi
cat "${MOCK_BUILD_INFO:?MOCK_BUILD_INFO must be set}"
MOCK
chmod +x "${bin}/go"

binary="${fixture}/docker-compose"
printf 'not a real binary\n' >"${binary}"

write_dockerfile() {
	cat >"${fixture}/Dockerfile" <<DOCKERFILE
FROM golang:1.27.1-alpine AS compose-builder
# was: go get github.com/containerd/containerd/v2@v2.3.5
RUN tar -xzf /tmp/compose.tar.gz --strip-components=1 \\
    && go get ${1} \\
    && go build -o /docker-compose ./cmd
DOCKERFILE
}

write_build_info() {
	printf '%s\n' \
		"${binary}: go1.27.1" \
		$'\tpath\tgithub.com/docker/compose/v5/cmd' \
		$'\tmod\tgithub.com/docker/compose/v5\t(devel)\t' \
		$'\tdep\tgithub.com/containerd/errdefs\tv1.0.0\th1:abc=' \
		"$@" \
		$'\tdep\tgithub.com/docker/docker\tv28.0.0+incompatible\th1:def=' \
		>"${fixture}/buildinfo.txt"
}

run_script() {
	PATH="${bin}:${PATH}" MOCK_BUILD_INFO="${fixture}/buildinfo.txt" \
		bash "${script}" "${binary}" "${fixture}/Dockerfile" 2>&1
}

expect_pass() {
	local description="$1"
	local output
	if ! output="$(run_script)"; then
		fail "${description}: ${output}"
	fi
}

expect_fail() {
	local expected="$1"
	local description="$2"
	local output
	local status
	set +e
	output="$(run_script)"
	status=$?
	set -e
	if [ "${status}" -eq 0 ] || ! grep -Fq -- "${expected}" <<<"${output}"; then
		fail "${description} (status ${status}, wanted: ${expected}): ${output}"
	fi
}

module_line=$'\tdep\tgithub.com/containerd/containerd/v2\t'

write_dockerfile "github.com/containerd/containerd/v2@v2.3.6"
write_build_info "${module_line}v2.3.6"$'\th1:xyz='
expect_pass "a binary on the pinned containerd must pass"

write_build_info "${module_line}v2.3.5"$'\th1:xyz='
expect_fail "embeds github.com/containerd/containerd/v2 v2.3.5, but" \
	"an older containerd than the pin must fail"

write_build_info "${module_line}v2.3.60"$'\th1:xyz='
expect_fail "embeds github.com/containerd/containerd/v2 v2.3.60, but" \
	"a version that only has the pin as a prefix must fail"

write_build_info
expect_fail "no github.com/containerd/containerd/v2 module line" \
	"a binary without the containerd module line must fail"

# The v1 module path is a different module and must not satisfy the check.
write_build_info $'\tdep\tgithub.com/containerd/containerd\tv1.7.30\th1:xyz='
expect_fail "no github.com/containerd/containerd/v2 module line" \
	"the unversioned containerd path must not count as the v2 module"

write_build_info "${module_line}v2.3.6"$'\th1:xyz=' \
	$'\t=>\tgithub.com/example/containerd\tv2.3.6\th1:rep='
expect_fail "listed more than once or replaced" \
	"a replaced containerd module must fail"

write_build_info "${module_line}v2.3.6"$'\th1:xyz=' "${module_line}v2.3.5"$'\th1:xyz='
expect_fail "listed more than once or replaced" \
	"a duplicated module line must fail"

write_build_info "${module_line}v2.3.6"$'\th1:xyz='
write_dockerfile "github.com/other/module@v1.0.0"
expect_fail "has no github.com/containerd/containerd/v2@vX.Y.Z pin" \
	"a Dockerfile without the pin must fail"

# A commented-out pin is not a pin.
cat >"${fixture}/Dockerfile" <<'DOCKERFILE'
FROM golang:1.27.1-alpine AS compose-builder
# RUN go get github.com/containerd/containerd/v2@v2.3.6
RUN go build ./cmd
DOCKERFILE
expect_fail "has no github.com/containerd/containerd/v2@vX.Y.Z pin" \
	"a commented-out pin must not count"

# Nor is one that only appears in a trailing comment on a real instruction.
cat >"${fixture}/Dockerfile" <<'DOCKERFILE'
FROM golang:1.27.1-alpine AS compose-builder
RUN go build ./cmd # go get github.com/containerd/containerd/v2@v2.3.6
DOCKERFILE
expect_fail "has no github.com/containerd/containerd/v2@vX.Y.Z pin" \
	"a pin that only appears in a trailing comment must not count"

# A real pin followed by a trailing comment still counts, and the comment's
# own version is ignored.
cat >"${fixture}/Dockerfile" <<'DOCKERFILE'
RUN go get github.com/containerd/containerd/v2@v2.3.6 # was v2.3.5
DOCKERFILE
expect_pass "a real pin with a trailing comment must still pass"

cat >"${fixture}/Dockerfile" <<'DOCKERFILE'
RUN go get github.com/containerd/containerd/v2@v2.3.6
RUN go get github.com/containerd/containerd/v2@v2.3.5
DOCKERFILE
expect_fail "pins github.com/containerd/containerd/v2 to more than one version" \
	"conflicting Dockerfile pins must fail"

write_dockerfile "github.com/containerd/containerd/v2@v2.3.6"
set +e
output="$(PATH="${bin}:${PATH}" MOCK_GO_FAIL=1 bash "${script}" "${binary}" "${fixture}/Dockerfile" 2>&1)"
status=$?
set -e
if [ "${status}" -eq 0 ] || ! grep -Fq "go version -m" <<<"${output}"; then
	fail "a go version -m failure must fail the check: ${output}"
fi

set +e
output="$(PATH="${bin}:${PATH}" MOCK_BUILD_INFO="${fixture}/buildinfo.txt" bash "${script}" "${fixture}/missing" "${fixture}/Dockerfile" 2>&1)"
status=$?
set -e
if [ "${status}" -eq 0 ] || ! grep -Fq "unreadable binary" <<<"${output}"; then
	fail "a missing binary must fail the check: ${output}"
fi

set +e
output="$(PATH="${bin}:${PATH}" MOCK_BUILD_INFO="${fixture}/buildinfo.txt" bash "${script}" "${binary}" "${fixture}/missing" 2>&1)"
status=$?
set -e
if [ "${status}" -eq 0 ] || ! grep -Fq "cannot read Dockerfile" <<<"${output}"; then
	fail "a missing Dockerfile must fail the check: ${output}"
fi

# No go at all must fail closed, not skip. A PATH holding only the symlinked
# coreutils the script needs, and no go, stands in for a runner without one.
nogo="${fixture}/nogo"
mkdir -p "${nogo}"
for tool in bash grep sed sort awk wc tr; do
	ln -s "$(command -v "${tool}")" "${nogo}/${tool}"
done
set +e
output="$(PATH="${nogo}" "${nogo}/bash" "${script}" "${binary}" "${fixture}/Dockerfile" 2>&1)"
status=$?
set -e
if [ "${status}" -eq 0 ] || ! grep -Fq "go is not on PATH" <<<"${output}"; then
	fail "a missing go must fail the check: ${output}"
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} verify-compose-containerd check(s) failed" >&2
	exit 1
fi

echo "verify-compose-containerd.sh checks passed."
