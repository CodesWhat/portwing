#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Fails when a Grype ignore entry has passed its expiry.
#
# Grype has no expiry field, so a waiver would keep suppressing its finding
# forever. The convention: an ignore entry's `reason` carries `expires:
# YYYY-MM-DD`. The waiver is valid through the day before that date; on the
# day itself, or later (UTC), this script fails. Entries with no `expires:` are
# permanent (for example the GO-2026-5932 linkage waivers) and always pass. A
# malformed or impossible (for example 2026-11-31) `expires:` value fails too,
# so a typo cannot disable the check. `expires:` is read from the entry's reason,
# so block-scalar reasons count; two in one entry fail as ambiguous.
#
# The config is read with a real YAML parser, mikefarah yq v4 (preinstalled on
# GitHub's ubuntu runners, `brew install yq` locally), so list indentation, key
# order and `#`-leading lines inside block scalars cannot hide an entry. With no
# yq v4 on PATH the script fails closed (exit 2) rather than skipping the check.
#
# Usage: check-grype-waiver-expiry.sh [grype-config ...]   (default .grype.yaml)
# GRYPE_WAIVER_TODAY=YYYY-MM-DD overrides today's date, for tests.
set -euo pipefail

today="${GRYPE_WAIVER_TODAY:-$(date -u +%F)}"
if ! [[ ${today} =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
	echo "error: GRYPE_WAIVER_TODAY must be YYYY-MM-DD, got '${today}'" >&2
	exit 2
fi

if [ "$#" -eq 0 ]; then
	set -- .grype.yaml
fi

if ! command -v yq >/dev/null 2>&1 || ! yq --version 2>&1 | grep -Eq 'mikefarah/yq.* v?4\.'; then
	echo "error: mikefarah yq v4 is required to read the Grype config and was not found on PATH" >&2
	exit 2
fi

# One line per ignore entry: <count of expires: values>, TAB, <vulnerability>,
# TAB, <first word after expires:>.
# shellcheck disable=SC2016  # $v and $e are yq variables, not shell
entry_query='(.ignore // [])[] | (.vulnerability // "(no vulnerability id)") as $v
	| ([(.reason // "") | tostring | match("expires:[ \t]*(\\S*)"; "g") | .captures[0].string]) as $e
	| ($e | length | tostring) + "\t" + ($v | tostring) + "\t" + ($e[0] // "")'

is_leap() {
	local y=$((10#$1))
	[ $((y % 4)) -eq 0 ] && { [ $((y % 100)) -ne 0 ] || [ $((y % 400)) -eq 0 ]; }
}

status=0
for config in "$@"; do
	if [ ! -f "${config}" ]; then
		echo "error: ${config} not found" >&2
		exit 2
	fi
	if ! entries="$(yq eval -r "${entry_query}" "${config}" 2>&1)"; then
		echo "error: ${config} is not valid YAML: ${entries}" >&2
		exit 2
	fi
	[ -n "${entries}" ] || continue
	while IFS=$'\t' read -r n vuln val; do
		[ "${n}" -gt 0 ] || continue
		if [ "${n}" -gt 1 ]; then
			echo "${config}: waiver for ${vuln} has ${n} expires: values; ambiguous, keep exactly one" >&2
			status=1
			continue
		fi
		if ! [[ ${val} =~ ^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])([^0-9]|$) ]]; then
			echo "${config}: waiver for ${vuln} has a malformed expiry; use expires: YYYY-MM-DD" >&2
			status=1
			continue
		fi
		expiry="${val:0:10}"
		m=$((10#${expiry:5:2}))
		d=$((10#${expiry:8:2}))
		mx="$(printf '%s' '312831303130313130313031' | cut -c$(((m - 1) * 2 + 1))-$(((m - 1) * 2 + 2)))"
		mx=$((10#${mx}))
		if [ "${m}" -eq 2 ] && is_leap "${expiry:0:4}"; then
			mx=29
		fi
		if [ "${d}" -gt "${mx}" ]; then
			echo "${config}: waiver for ${vuln} has an invalid calendar date ${expiry} in expires:; use a real YYYY-MM-DD date" >&2
			status=1
			continue
		fi
		if ! [[ ${expiry} > ${today} ]]; then
			echo "${config}: waiver for ${vuln} expired on ${expiry} (today is ${today}). Remove the ignore entry or get a renewed approval and a new expiry date." >&2
			status=1
		fi
	done <<<"${entries}"
done
exit "${status}"
