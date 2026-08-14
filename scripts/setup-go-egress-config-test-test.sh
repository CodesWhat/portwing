#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d)"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${test_root}/.github/workflows" "${test_root}/scripts"
cp scripts/setup-go-egress-config-test.sh "${test_root}/scripts/"

write_same_job_fixture() {
	local policy="$1"
	shift

	{
		cat <<EOF
name: Contract fixture
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - name: Harden Runner
        uses: step-security/harden-runner@example
        with:
          egress-policy: ${policy}
          allowed-endpoints: |
EOF
		for endpoint in "$@"; do
			printf '            %s\n' "${endpoint}"
		done
		cat <<'EOF'
      - name: Setup Go
        uses: actions/setup-go@example
EOF
	} >"${test_root}/.github/workflows/contract.yml"
}

write_cross_job_fixture() {
	cat >"${test_root}/.github/workflows/contract.yml" <<'EOF'
name: Contract fixture
jobs:
  guarded:
    runs-on: ubuntu-latest
    steps:
      - name: Harden Runner
        uses: step-security/harden-runner@example
        with:
          egress-policy: block
          allowed-endpoints: |
            go.dev:443
            dl.google.com:443
  unguarded:
    runs-on: ubuntu-latest
    steps:
      - name: Setup Go
        uses: actions/setup-go@example
EOF
}

assert_rejected() {
	local expected="$1"
	local failure_message="$2"
	local output
	local status

	set +e
	output="$(
		cd "${test_root}" && PATH=/usr/bin:/bin bash scripts/setup-go-egress-config-test.sh 2>&1
	)"
	status=$?
	set -e

	if [ "${status}" -eq 0 ] || ! grep -Fq "${expected}" <<<"${output}"; then
		echo "FAIL: ${failure_message}" >&2
		exit 1
	fi
}

write_same_job_fixture block go.dev:443 dl.google.com:443
(cd "${test_root}" && PATH=/usr/bin:/bin bash scripts/setup-go-egress-config-test.sh >/dev/null)

write_same_job_fixture block dl.google.com:443
assert_rejected \
	"Setup Go job must allow go.dev:443" \
	"Setup Go must independently require the go.dev endpoint"

write_same_job_fixture block go.dev:443
assert_rejected \
	"Setup Go job must allow dl.google.com:443" \
	"Setup Go must independently require the dl.google.com endpoint"

write_cross_job_fixture
assert_rejected \
	"Setup Go job must use egress-policy: block" \
	"endpoint text from an earlier job must not protect a later Setup Go job"

write_same_job_fixture audit go.dev:443 dl.google.com:443
assert_rejected \
	"Setup Go job must use egress-policy: block" \
	"Setup Go must require a blocking Harden Runner policy in the same job"

echo "Setup Go egress contract self-tests passed."
