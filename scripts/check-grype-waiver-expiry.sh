#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Fails when a Grype ignore entry has passed its expiry.
#
# Grype has no expiry field, so a waiver would keep suppressing its finding
# forever. The convention: an ignore entry's `reason` carries `expires:
# YYYY-MM-DD`. The waiver is valid through the day before that date; on the
# day itself, or later (UTC), this script fails. Entries with no `expires:` are
# permanent (for example the GO-2026-5932 linkage waivers) and always pass. A
# malformed `expires:` value fails too, so a typo cannot disable the check.
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

status=0
for config in "$@"; do
	if [ ! -f "${config}" ]; then
		echo "error: ${config} not found" >&2
		exit 2
	fi
	if ! awk -v today="${today}" -v file="${config}" '
		/^[[:space:]]*#/ { next }
		/^[[:space:]]*-[[:space:]]+vulnerability:/ {
			vuln = $0
			sub(/^[[:space:]]*-[[:space:]]+vulnerability:[[:space:]]*/, "", vuln)
			gsub(/["\047[:space:]]/, "", vuln)
			next
		}
		/^[[:space:]]+reason:/ && index($0, "expires:") {
			line = $0
			sub(/^.*expires:[[:space:]]*/, "", line)
			if (line !~ /^[0-9][0-9][0-9][0-9]-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])([^0-9]|$)/) {
				printf "%s: waiver for %s has a malformed expiry; use expires: YYYY-MM-DD\n", file, vuln > "/dev/stderr"
				bad = 1
				next
			}
			expiry = substr(line, 1, 10)
			if (expiry <= today) {
				printf "%s: waiver for %s expired on %s (today is %s). Remove the ignore entry or get a renewed approval and a new expiry date.\n", file, vuln, expiry, today > "/dev/stderr"
				bad = 1
			}
		}
		END { exit bad }
	' "${config}"; then
		status=1
	fi
done
exit "${status}"
