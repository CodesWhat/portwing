#!/usr/bin/env bash
set -euo pipefail

failures=0
setup_count=0

while IFS= read -r workflow; do
	while IFS='|' read -r line has_block_policy has_go_dev has_dl_google; do
		[ -n "${line}" ] || continue
		setup_count=$((setup_count + 1))
		if [ "${has_block_policy}" -ne 1 ]; then
			echo "FAIL: ${workflow}:${line} Setup Go job must use egress-policy: block" >&2
			failures=$((failures + 1))
		fi
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
			/^  [[:alnum:]_.-]+:[[:space:]]*$/ {
				capture_harden = 0
				harden = ""
			}
			/^[[:space:]]+- name: Harden Runner[[:space:]]*$/ {
				capture_harden = 1
				harden = "\n" $0 "\n"
				next
			}
			capture_harden && /^[[:space:]]+- (name|uses):/ {
				capture_harden = 0
			}
			capture_harden { harden = harden $0 "\n" }
			/uses: actions\/setup-go@/ {
				has_block_policy = harden ~ /\n[[:space:]]+egress-policy:[[:space:]]+block[[:space:]]*\n/
				has_go_dev = harden ~ /\n[[:space:]]+go[.]dev:443[[:space:]]*\n/
				has_dl_google = harden ~ /\n[[:space:]]+dl[.]google[.]com:443[[:space:]]*\n/
				printf "%d|%d|%d|%d\n", NR, has_block_policy, has_go_dev, has_dl_google
				capture_harden = 0
				harden = ""
			}
		' "${workflow}"
	)
done < <(
	grep -rl \
		--include='*.yml' \
		--include='*.yaml' \
		'uses: actions/setup-go@' \
		.github/workflows | sort
)

if [ "${setup_count}" -eq 0 ]; then
	echo "FAIL: no actions/setup-go steps found" >&2
	exit 1
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} Setup Go egress contract check(s) failed" >&2
	exit 1
fi

echo "Setup Go egress contract checks passed for ${setup_count} jobs."
