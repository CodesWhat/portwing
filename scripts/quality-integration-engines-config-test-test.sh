#!/usr/bin/env bash
set -euo pipefail

# Self-test for scripts/quality-integration-engines-config-test.sh. Each case
# breaks the workflow (or the source the floor is derived from) in one
# specific way and asserts the contract rejects it with the message that names
# that specific breakage. A contract nobody has watched fail is a contract
# nobody knows works.

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-quality-integration-engines.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${test_root}/scripts"
cp scripts/quality-integration-engines-config-test.sh "${test_root}/scripts/"
fixture="${test_root}/workflow.yml"
base_fixture="${test_root}/base.yml"
client_fixture="${test_root}/client.go"

reset_fixture() {
	cp .github/workflows/quality-integration-engines.yml "${fixture}"
}

reset_base_fixture() {
	cp .github/workflows/quality-integration.yml "${base_fixture}"
}

reset_client_fixture() {
	cp internal/docker/client.go "${client_fixture}"
}

run_contract() {
	(cd "${test_root}" &&
		bash scripts/quality-integration-engines-config-test.sh \
			workflow.yml base.yml client.go 2>&1)
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

reset_all() {
	reset_fixture
	reset_base_fixture
	reset_client_fixture
}

# The real workflow must pass its own contract.
reset_all
assert_passes "the real quality-integration-engines.yml must pass its own contract"

# --- triggers ----------------------------------------------------------------

# No schedule: the lane stops being a recurring one, which is the whole ask.
reset_all
sed -i.bak "/^    - cron: /d" "${fixture}"
assert_rejected \
	"lane must run on a schedule" \
	"contract must reject a lane with no cron schedule"

# No manual dispatch: nobody can prove a fix without waiting a week.
reset_all
sed -i.bak "/^  workflow_dispatch:$/d" "${fixture}"
assert_rejected \
	"lane must be manually dispatchable" \
	"contract must reject a lane that can't be dispatched manually"

# pull_request widened to the source tree: a four-Engine matrix would run on
# every PR in the repo.
reset_all
sed -i.bak "s|^      - '.github/workflows/quality-integration-engines.yml'$|&\n      - 'internal/**'|" "${fixture}"
assert_rejected \
	"pull_request paths must be exactly" \
	"contract must reject a pull_request filter widened past this workflow file"

# pull_request scoped to some other file: the workflow could change without
# ever exercising itself.
reset_all
sed -i.bak "s|^      - '.github/workflows/quality-integration-engines.yml'$|      - '.github/workflows/quality-integration.yml'|" "${fixture}"
assert_rejected \
	"pull_request paths must be exactly" \
	"contract must reject a pull_request filter pointed at a different file"

# --- permissions -------------------------------------------------------------

# Top-level grant widened beyond the checkout's read.
reset_all
sed -i.bak "s|^  contents: read$|&\n  issues: write|" "${fixture}"
assert_rejected \
	"top-level permissions must be exactly 'contents: read'" \
	"contract must reject a widened top-level permissions block"

# Job-level block reintroducing a write scope under the top-level read.
reset_all
sed -i.bak "s|^    runs-on: ubuntu-24.04$|&\n    permissions:\n      packages: write|" "${fixture}"
assert_rejected \
	"the integration job must not declare its own permissions block" \
	"contract must reject a job-level permissions block"

# --- job shape ---------------------------------------------------------------

# No timeout: a wedged daemon holds a runner for six hours.
reset_all
sed -i.bak "/^    timeout-minutes: /d" "${fixture}"
assert_rejected \
	"the integration job must set timeout-minutes" \
	"contract must reject a job with no timeout"

# fail-fast back on: the first broken Engine cancels the rest and the matrix
# stops answering the only question it was built to answer.
reset_all
sed -i.bak "s|^      fail-fast: false$|      fail-fast: true|" "${fixture}"
assert_rejected \
	"strategy must set fail-fast: false" \
	"contract must reject fail-fast: true"

# continue-on-error at job level: every leg reports green regardless.
reset_all
sed -i.bak "s|^    runs-on: ubuntu-24.04$|&\n    continue-on-error: true|" "${fixture}"
assert_rejected \
	"continue-on-error must not appear anywhere" \
	"contract must reject job-level continue-on-error"

# continue-on-error at step level: the same masking, one indent deeper.
reset_all
sed -i.bak "s|^      - name: Run integration suite$|&\n        continue-on-error: true|" "${fixture}"
assert_rejected \
	"continue-on-error must not appear anywhere" \
	"contract must reject step-level continue-on-error"

# --- the matrix --------------------------------------------------------------

# Collapsed to a single Engine: no longer a matrix, and identical in coverage
# to the daily lane it is supposed to complement.
reset_all
sed -i.bak \
	-e "/^          - v27\.5\.1$/d" \
	-e "/^          - v28\.5\.2$/d" \
	-e "/^          - v29\.7\.2$/d" \
	"${fixture}"
assert_rejected \
	"matrix.engine must pin at least 2 Engine versions, found 1" \
	"contract must reject a single-entry matrix"

# A floating minor instead of a pinned patch: the lane stops being reproducible.
reset_all
sed -i.bak "s|^          - v29\.7\.2$|          - v29|" "${fixture}"
assert_rejected \
	"must be a fully pinned vMAJOR.MINOR.PATCH release" \
	"contract must reject an Engine entry that isn't a full release version"

# Oldest pin dropped: the matrix no longer reaches the API version the docs
# and the negotiation fallback both publish as the floor.
reset_all
sed -i.bak "/^          - v25\.0\.5$/d" "${fixture}"
assert_rejected \
	"must sit exactly on the documented floor" \
	"contract must reject a matrix that no longer covers the documented API floor"

# The floor is read from client.go, not restated in the contract: drop the
# negotiation fallback to an older API and the same matrix must now fail,
# because the code is claiming support for an Engine nothing tests.
reset_all
sed -i.bak 's|c.apiVersion = "v1.44"|c.apiVersion = "v1.43"|g' "${client_fixture}"
assert_rejected \
	"must sit exactly on the documented floor" \
	"contract must re-derive the floor from client.go, not hard-code it"

# Matrix reaching BELOW the floor. A one-sided "at least as old as" check
# passes this, which is why the assertion is equality: Engine 24.0 serves API
# v1.43, which portwing's docs don't claim, so a leg there tests a contract
# nobody made.
reset_all
sed -i.bak "s|^          - v25\.0\.5$|          - v24.0.9|" "${fixture}"
assert_rejected \
	"must sit exactly on the documented floor" \
	"contract must reject a matrix that reaches back past the documented floor"

# An API version the table doesn't know must fail loudly rather than skip the
# floor check and report a green contract.
reset_all
sed -i.bak 's|c.apiVersion = "v1.44"|c.apiVersion = "v1.60"|g' "${client_fixture}"
assert_rejected \
	"is not in this script's API-to-Engine table" \
	"contract must refuse to guess at an API version its table doesn't cover"

# --- action pins -------------------------------------------------------------

# A mutable tag ref in place of a SHA.
reset_all
sed -i.bak "s|uses: docker/setup-docker-action@[0-9a-f]*  # v5.4.0|uses: docker/setup-docker-action@v5.4.0|" "${fixture}"
assert_rejected \
	"must be pinned to a 40-hex commit SHA" \
	"contract must reject a tag-ref action"

# A SHA one character short: still looks pinned, isn't a valid full OID.
reset_all
sed -i.bak "s|uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1|uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b|" "${fixture}"
assert_rejected \
	"must be pinned to a 40-hex commit SHA" \
	"contract must reject a truncated SHA"

# Checkout persisting credentials into the workspace of a job that then runs
# arbitrary container workloads.
reset_all
sed -i.bak "/persist-credentials: false/d" "${fixture}"
assert_rejected \
	"checkout must not persist credentials" \
	"contract must reject a checkout that persists credentials"

# --- egress ------------------------------------------------------------------

# Downgraded from block to audit: harden-runner stops enforcing anything.
reset_all
sed -i.bak "s|^          egress-policy: block$|          egress-policy: audit|" "${fixture}"
assert_rejected \
	"harden-runner must run with egress-policy: block" \
	"contract must reject an audit-only harden-runner"

# The allow-list loses the host the pinned Engine tarball comes from, which
# would fail every leg at install time.
reset_all
sed -i.bak "/download.docker.com:443/d" "${fixture}"
assert_rejected \
	"must permit download.docker.com" \
	"contract must reject an allow-list missing download.docker.com"

# The allow-list loses the raw GitHub path setup-docker-action reads release
# metadata from. This one looks removable and isn't: run 33691346332 failed
# all four legs on exactly this.
reset_all
sed -i.bak "/raw.githubusercontent.com:443/d" "${fixture}"
assert_rejected \
	"must permit raw.githubusercontent.com" \
	"contract must reject an allow-list missing raw.githubusercontent.com"

# --- the pinned daemon actually being under test -----------------------------

# Engine version hard-coded instead of taken from the matrix: every leg
# installs the same daemon and the matrix is decoration.
reset_all
sed -i.bak 's|          version: ${{ matrix.engine }}|          version: v29.7.2|' "${fixture}"
assert_rejected \
	"must install the version the matrix names" \
	"contract must reject a hard-coded Engine version"

# The whole verification step removed: a silent fallback to the runner's stock
# dockerd would make every leg of the matrix a lie.
reset_all
sed -i.bak "s|^      - name: Resolve daemon socket and verify pinned version$|      - name: Resolve daemon socket|" "${fixture}"
assert_rejected \
	"lane must resolve the daemon socket and verify the pinned Engine took" \
	"contract must reject a lane with no daemon verification step"

# Socket hard-coded rather than resolved from the active context, so every leg
# talks to the runner's preinstalled daemon.
reset_all
sed -i.bak "s|host=\"\$(docker context inspect --format '{{ .Endpoints.docker.Host }}')\"|host=\"unix:///var/run/docker.sock\"|" "${fixture}"
assert_rejected \
	"must be resolved from the active docker context" \
	"contract must reject a hard-coded daemon socket"

# The version comparison dropped: the pinned install could fail over to stock
# dockerd and the leg would still pass.
reset_all
sed -i.bak "/pinned Engine did not take/d" "${fixture}"
assert_rejected \
	"must say what went wrong when the daemon reports a version other than the pin" \
	"contract must reject a lane that doesn't check the daemon version it got"

# The socket existence check dropped: the suite skips on a missing socket and
# exits 0, so the leg goes green having run no tests at all.
reset_all
sed -i.bak "/the suite would silently skip/d" "${fixture}"
assert_rejected \
	"must say why a missing socket is fatal" \
	"contract must reject a lane that lets the suite skip itself green"

# The version comparison inverted from != to =, so the guard passes only when
# the daemon is WRONG. The echo string survives, which is exactly why the
# contract asserts the predicate and not just the message.
reset_all
# shellcheck disable=SC2016 # ${got}/${WANT} are the workflow's shell variables, not ours.
sed -i.bak 's|if \[ "v${got}" != "${WANT}" \]; then|if [ "v${got}" = "${WANT}" ]; then|' "${fixture}"
assert_rejected \
	"must compare the daemon's reported version against the pin with !=" \
	"contract must reject an inverted version comparison"

# The version guard keeps its diagnostic but stops failing the leg. Printing
# is not guarding: the suite would then run against the wrong daemon and pass.
reset_all
sed -i.bak '/pinned Engine did not take/{n;s/exit 1/true/;}' "${fixture}"
assert_rejected \
	"the version guard must exit non-zero" \
	"contract must reject a version guard that prints without failing"

# Same for the missing-socket guard. This is the worse of the two, because a
# skipped suite exits 0 and the leg reports green having run nothing.
reset_all
sed -i.bak '/the suite would silently skip/{n;s/exit 1/true/;}' "${fixture}"
assert_rejected \
	"the missing-socket guard must exit non-zero" \
	"contract must reject a socket guard that prints without failing"

# -S relaxed to -e: a plain file or directory at the resolved path would now
# satisfy the check, and the suite would skip on it.
reset_all
# shellcheck disable=SC2016 # ${sock} is the workflow's shell variable, not ours.
sed -i.bak 's|if \[ ! -S "${sock}" \]; then|if [ ! -e "${sock}" ]; then|' "${fixture}"
assert_rejected \
	"must check the resolved path is actually a socket" \
	"contract must reject a socket check relaxed to mere existence"

# --- suite invocation --------------------------------------------------------

# The two lanes drift: this one now runs a shorter timeout than the daily lane,
# so a green matrix stops saying anything about the daily lane's coverage.
reset_all
sed -i.bak "s|-timeout=10m -race ./internal/edge/|-timeout=5m -race ./internal/edge/|" "${fixture}"
assert_rejected \
	"suite invocation must match" \
	"contract must reject a suite invocation that drifted from quality-integration.yml"

# The daily lane changes its package list and this one doesn't follow.
reset_all
sed -i.bak "s|-race ./internal/edge/ ./internal/integration/|-race ./internal/integration/|" "${base_fixture}"
assert_rejected \
	"suite invocation must match" \
	"contract must reject drift introduced from the base workflow's side"

# The suite pointed at the runner's stock socket rather than the pinned
# daemon's, which every leg would pass against the same Engine.
reset_all
sed -i.bak 's|          PORTWING_TEST_DOCKER_SOCKET: ${{ steps.daemon.outputs.socket }}|          PORTWING_TEST_DOCKER_SOCKET: /var/run/docker.sock|' "${fixture}"
assert_rejected \
	"must not be pointed at the runner's stock docker socket" \
	"contract must reject the suite being pointed at the stock docker socket"

# PORTWING_TEST_DOCKER_SOCKET dropped from the step that runs the suite. The
# suite then falls back to /var/run/docker.sock and every leg tests the
# runner's stock dockerd.
reset_all
sed -i.bak '/^          PORTWING_TEST_DOCKER_SOCKET: ${{ steps.daemon.outputs.socket }}$/d' "${fixture}"
assert_rejected \
	"the suite step must be pointed at the resolved daemon socket" \
	"contract must reject a suite step with no PORTWING_TEST_DOCKER_SOCKET"

# DOCKER_HOST dropped from the suite step only. The pre-pull step keeps its
# own, so a file-wide grep still finds the string. This is the case that makes
# the step scoping load-bearing rather than cosmetic, and it is the exact
# regression that would send startAlpineContainer's bare `docker run` to the
# runner's default context while the rest of the suite talks to the pinned
# daemon.
reset_all
sed -i.bak '/PORTWING_TEST_DOCKER_SOCKET/{n;d;}' "${fixture}"
assert_rejected \
	"the suite step must set DOCKER_HOST to the resolved daemon socket" \
	"contract must reject a suite step missing DOCKER_HOST even when a decoy DOCKER_HOST survives elsewhere"

echo "Quality integration engine-matrix contract self-tests passed."
