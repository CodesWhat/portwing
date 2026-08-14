#!/usr/bin/env bash
set -euo pipefail

failures=0
setup_count=0

while IFS= read -r workflow; do
	while IFS='|' read -r line has_go_dev has_dl_google; do
		[ -n "${line}" ] || continue
		setup_count=$((setup_count + 1))
		if [ "${has_go_dev}" -ne 1 ]; then
			echo "FAIL: ${workflow}:${line} Setup Go job must allow go.dev:443" >&2
			failures=$((failures + 1))
		fi
		if [ "${has_dl_google}" -ne 1 ]; then
			echo "FAIL: ${workflow}:${line} Setup Go job must allow dl.google.com:443" >&2
			failures=$((failures + 1))
		fi
	done < <(
		awk '
			/- name: Harden Runner/ {
				harden = "\n"
				found_harden = 1
			}
			found_harden { harden = harden $0 "\n" }
			/uses: actions\/setup-go@/ {
				has_go_dev = harden ~ /\n[[:space:]]+go[.]dev:443[[:space:]]*\n/
				has_dl_google = harden ~ /\n[[:space:]]+dl[.]google[.]com:443[[:space:]]*\n/
				printf "%d|%d|%d\n", NR, has_go_dev, has_dl_google
				found_harden = 0
				harden = ""
			}
		' "${workflow}"
	)
done < <(rg -l 'uses: actions/setup-go@' .github/workflows | sort)

if [ "${setup_count}" -eq 0 ]; then
	echo "FAIL: no actions/setup-go steps found" >&2
	exit 1
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} Setup Go egress contract check(s) failed" >&2
	exit 1
fi

echo "Setup Go egress contract checks passed for ${setup_count} jobs."
