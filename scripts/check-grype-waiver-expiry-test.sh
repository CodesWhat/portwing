#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Tests for scripts/check-grype-waiver-expiry.sh, on both sides of the deadline.
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-waiver-expiry.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

config="${test_root}/grype.yaml"
cat >"${config}" <<'YAML'
ignore:
  - vulnerability: GO-2026-5932
    reason: "No expiry, permanent linkage waiver."
    package:
      name: golang.org/x/crypto
  - vulnerability: GHSA-fc5q-39xc-vjpf
    reason: "Build-time only. Approved 2026-11-18, expires: 2026-12-17 or when fixed."
    package:
      name: picomatch
YAML

failures=0
expect() {
	local want="$1" today="$2" file="$3" label="$4" remedy="${5:-Remove the ignore entry or get a renewed approval}" out
	local got=0
	out="$(GRYPE_WAIVER_TODAY="${today}" bash scripts/check-grype-waiver-expiry.sh "${file}" 2>&1)" || got=$?
	if [ "${want}" = pass ] && [ "${got}" -ne 0 ]; then
		echo "FAIL: ${label}: expected pass, got exit ${got}: ${out}" >&2
		failures=$((failures + 1))
	elif [ "${want}" = fail ] && [ "${got}" -ne 1 ]; then
		echo "FAIL: ${label}: expected exit 1, got ${got}: ${out}" >&2
		failures=$((failures + 1))
	elif [ "${want}" = fail ] && { ! grep -Fq 'GHSA-fc5q-39xc-vjpf' <<<"${out}" || ! grep -Fq "${remedy}" <<<"${out}"; }; then
		echo "FAIL: ${label}: message must name the vulnerability and the remedy: ${out}" >&2
		failures=$((failures + 1))
	fi
}

expect pass 2026-12-16 "${config}" "one day before the deadline"
expect fail 2026-12-17 "${config}" "on the deadline"
expect fail 2026-12-18 "${config}" "after the deadline"
expect fail 2027-06-01 "${config}" "long after the deadline"

# An entry without an expiry never expires, however late the date.
noexp="${test_root}/noexp.yaml"
sed '/GHSA-fc5q-39xc-vjpf/,$d' "${config}" >"${noexp}"
expect pass 2099-01-01 "${noexp}" "entry without an expiry"

# A malformed expiry fails instead of silently disabling the check.
bad="${test_root}/bad.yaml"
sed 's/expires: 2026-12-17/expires: soon/' "${config}" >"${bad}"
expect fail 2026-01-01 "${bad}" "malformed expiry" "expires: YYYY-MM-DD"

# The real config is valid today's-date-independent in shape: pre-deadline passes.
expect pass 2026-11-18 .grype.yaml "real .grype.yaml before its deadline"

if [ "${failures}" -ne 0 ]; then
	exit 1
fi
echo "Grype waiver expiry tests passed."
