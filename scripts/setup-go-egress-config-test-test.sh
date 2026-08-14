#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d)"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${test_root}/.github/workflows" "${test_root}/scripts"
cp scripts/setup-go-egress-config-test.sh "${test_root}/scripts/"

write_guarded_job() {
	cat >"${test_root}/.github/workflows/contract.yml" <<'EOF'
name: Contract fixture
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - name: Harden Runner
        uses: step-security/harden-runner@example
        with:
          egress-policy: block
          allowed-endpoints: |
            go.dev:443
            dl.google.com:443
      - name: Setup Go
        uses: actions/setup-go@example
EOF
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

write_nonblocking_fixture() {
	cat >"${test_root}/.github/workflows/contract.yml" <<'EOF'
name: Contract fixture
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - name: Harden Runner
        uses: step-security/harden-runner@example
        with:
          egress-policy: audit
          allowed-endpoints: |
            go.dev:443
            dl.google.com:443
      - name: Setup Go
        uses: actions/setup-go@example
EOF
}

write_guarded_job
(cd "${test_root}" && PATH=/usr/bin:/bin bash scripts/setup-go-egress-config-test.sh >/dev/null)

write_cross_job_fixture
if (cd "${test_root}" && PATH=/usr/bin:/bin bash scripts/setup-go-egress-config-test.sh >/dev/null 2>&1); then
	echo "FAIL: endpoint text from an earlier job must not protect a later Setup Go job" >&2
	exit 1
fi

write_nonblocking_fixture
set +e
nonblocking_output="$(
	cd "${test_root}" && PATH=/usr/bin:/bin bash scripts/setup-go-egress-config-test.sh 2>&1
)"
nonblocking_status=$?
set -e
if [ "${nonblocking_status}" -eq 0 ] ||
	! grep -Fq "Setup Go job must use egress-policy: block" <<<"${nonblocking_output}"; then
	echo "FAIL: Setup Go must require a blocking Harden Runner policy in the same job" >&2
	exit 1
fi

echo "Setup Go egress contract self-tests passed."
