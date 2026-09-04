#!/usr/bin/env bash
set -euo pipefail

# Self-test for scripts/starchart-config-test.sh. Each case breaks the real
# workflow in one specific way and asserts the contract rejects it with the
# message that names that specific breakage. A contract nobody has watched
# fail is a contract nobody knows works.

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-starchart.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${test_root}/scripts"
cp scripts/starchart-config-test.sh "${test_root}/scripts/"
fixture="${test_root}/workflow.yml"

reset_fixture() {
	cp .github/workflows/starchart.yml "${fixture}"
}

run_contract() {
	(cd "${test_root}" && bash scripts/starchart-config-test.sh workflow.yml 2>&1)
}

assert_passes() {
	local failure_message="$1"
	if ! run_contract >/dev/null 2>&1; then
		echo "FAIL: ${failure_message}" >&2
		echo "--- actual output ---" >&2
		run_contract >&2 || true
		exit 1
	fi
}

assert_rejected() {
	local expected="$1"
	local failure_message="$2"
	local output
	local status

	set +e
	output="$(run_contract)"
	status=$?
	set -e

	if [ "${status}" -eq 0 ] || ! grep -Fq "${expected}" <<<"${output}"; then
		echo "FAIL: ${failure_message}" >&2
		echo "--- actual output ---" >&2
		echo "${output}" >&2
		exit 1
	fi
}

# The real workflow must pass its own contract.
reset_fixture
assert_passes "the real starchart.yml must pass its own contract"

# --- top-level permissions --------------------------------------------------

reset_fixture
sed -i.bak 's/^permissions: {}$/permissions:\n  contents: write/' "${fixture}"
assert_rejected \
	"workflow-level permissions must be exactly permissions: {}" \
	"contract must reject a workflow-level permissions block that isn't {}"

# --- contents: write scoped to exactly one job ------------------------------

# Dropped entirely: the reusable workflow's commit-back step would fail on
# every run with no permission to push.
reset_fixture
sed -i.bak '/^      contents: write$/d' "${fixture}"
assert_rejected \
	"exactly one job in the file must grant contents: write" \
	"contract must reject a starchart job missing contents: write"

# Granted a second time by another job: this simulates a future job in the
# same file quietly picking up write access it doesn't need.
reset_fixture
cat >>"${fixture}" <<'YAML'
  extra:
    permissions:
      contents: write
    runs-on: ubuntu-24.04
    steps:
      - run: echo hi
YAML
assert_rejected \
	"exactly one job in the file must grant contents: write" \
	"contract must reject a second job also granting contents: write"

# --- no unconditional escape hatch ------------------------------------------

reset_fixture
sed -i.bak 's/^    permissions:$/    continue-on-error: true\n    permissions:/' "${fixture}"
assert_rejected \
	"must not carry continue-on-error" \
	"contract must reject a starchart job with continue-on-error"

reset_fixture
sed -i.bak 's/^    permissions:$/    if: always()\n    permissions:/' "${fixture}"
assert_rejected \
	"must not run unconditionally via if: always()" \
	"contract must reject a starchart job gated on if: always()"

# --- the reusable workflow pin ----------------------------------------------

# Pinned to a branch instead of a commit SHA: exactly the drift the pin
# exists to prevent, since the write path this delegates to could then
# change behaviour with no commit landing in this repo.
reset_fixture
sed -i.bak \
	's|starchart-refresh\.yml@[0-9a-f]\{40\}|starchart-refresh.yml@main|' \
	"${fixture}"
assert_rejected \
	"must be a full 40-hex commit SHA followed by a comment" \
	"contract must reject the reusable workflow pinned to a branch"

# SHA present but the trailing comment recording why/when is gone.
reset_fixture
sed -i.bak \
	's|\(starchart-refresh\.yml@[0-9a-f]\{40\}\)  # main, 2026-08-21|\1|' \
	"${fixture}"
assert_rejected \
	"must be a full 40-hex commit SHA followed by a comment" \
	"contract must reject a SHA pin with no trailing comment"

# --- the target branch -------------------------------------------------------

reset_fixture
sed -i.bak 's/^      branch: dev\/v0\.9$/      branch: main/' "${fixture}"
assert_rejected \
	"branch must not be main/master/HEAD/empty" \
	"contract must reject branch: main, which the reusable workflow also refuses at run time"

reset_fixture
sed -i.bak 's/^      branch: dev\/v0\.9$/      branch: some-feature-branch/' "${fixture}"
assert_rejected \
	"branch must match this repo's dev/vX.Y convention" \
	"contract must reject a branch that isn't this repo's dev/vX.Y"

echo "starchart contract self-tests passed."
