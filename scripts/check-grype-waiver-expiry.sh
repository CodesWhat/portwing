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
# so a typo cannot disable the check. `expires:` is read anywhere in the entry,
# so block-scalar reasons count; two in one entry fail as ambiguous.
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
	# Dates are validated in awk (month lengths and leap years) rather than with
	# `date -d` or python3, so it behaves the same on GNU/Linux and macOS.
	if ! awk -v today="${today}" -v file="${config}" '
		function flush(    line, expiry, y, m, d, mx, rest) {
			if (!inentry) return
			inentry = 0
			if (n > 1) {
				printf "%s: waiver for %s has %d expires: values; ambiguous, keep exactly one\n", file, vuln, n > "/dev/stderr"
				bad = 1
				return
			}
			if (n == 0) return
			line = val
			if (line !~ /^[0-9][0-9][0-9][0-9]-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])([^0-9]|$)/) {
				printf "%s: waiver for %s has a malformed expiry; use expires: YYYY-MM-DD\n", file, vuln > "/dev/stderr"
				bad = 1
				return
			}
			expiry = substr(line, 1, 10)
			y = substr(expiry, 1, 4) + 0
			m = substr(expiry, 6, 2) + 0
			d = substr(expiry, 9, 2) + 0
			mx = substr("312831303130313130313031", (m - 1) * 2 + 1, 2) + 0
			if (m == 2 && (y % 4 == 0 && (y % 100 != 0 || y % 400 == 0))) mx = 29
			if (d > mx) {
				printf "%s: waiver for %s has an invalid calendar date %s in expires:; use a real YYYY-MM-DD date\n", file, vuln, expiry > "/dev/stderr"
				bad = 1
				return
			}
			if (expiry <= today) {
				printf "%s: waiver for %s expired on %s (today is %s). Remove the ignore entry or get a renewed approval and a new expiry date.\n", file, vuln, expiry, today > "/dev/stderr"
				bad = 1
			}
		}
		/^[[:space:]]*#/ { next }
		/^[^[:space:]]/ { flush(); next }
		/^[[:space:]]*-[[:space:]]+vulnerability:/ {
			flush()
			inentry = 1
			n = 0
			val = ""
			vuln = $0
			sub(/^[[:space:]]*-[[:space:]]+vulnerability:[[:space:]]*/, "", vuln)
			gsub(/["\047[:space:]]/, "", vuln)
			next
		}
		inentry {
			rest = $0
			while ((i = index(rest, "expires:")) > 0) {
				rest = substr(rest, i + 8)
				sub(/^[[:space:]]*/, "", rest)
				if (++n == 1) val = rest
			}
		}
		END { flush(); exit bad }
	' "${config}"; then
		status=1
	fi
done
exit "${status}"
