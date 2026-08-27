#!/usr/bin/env bash
set -euo pipefail

failures=0
safe_publication="127.0.0.1:3000:3000"

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

yaml_publications() {
	awk '
		/^[[:space:]]*ports:[[:space:]]*$/ {
			in_ports = 1
			ports_indent = match($0, /[^[:space:]]/) - 1
			next
		}
		in_ports {
			if ($0 ~ /^[[:space:]]*$/) {
				next
			}

			indent = match($0, /[^[:space:]]/) - 1
			if (indent <= ports_indent) {
				in_ports = 0
				next
			}

			if ($0 ~ /^[[:space:]]*-[[:space:]]+/) {
				value = $0
				sub(/^[[:space:]]*-[[:space:]]*/, "", value)
				sub(/[[:space:]]+#.*$/, "", value)
				sub(/[[:space:]]*$/, "", value)
				first = substr(value, 1, 1)
				last = substr(value, length(value), 1)
				if ((first == "\"" && last == "\"") ||
				    (first == "\047" && last == "\047")) {
					value = substr(value, 2, length(value) - 2)
				}
				print NR "|" value
			}
		}
	' "$1"
}

docker_run_publications() {
	awk '
		/^[[:space:]]+-p[[:space:]]+/ {
			value = $0
			sub(/^[[:space:]]+-p[[:space:]]+/, "", value)
			sub(/[[:space:]]*\\[[:space:]]*$/, "", value)
			print NR "|" value
		}
		/^[[:space:]]+--publish[=[:space:]]/ {
			value = $0
			sub(/^[[:space:]]+--publish[=[:space:]]+/, "", value)
			sub(/[[:space:]]*\\[[:space:]]*$/, "", value)
			print NR "|" value
		}
	' "$1"
}

require_publications() {
	local file="$1"
	local kind="$2"
	local expected_count="$3"
	local description="$4"
	local actual_count=0
	local line
	local publication
	local publications

	if [ "$kind" = "yaml" ]; then
		publications="$(yaml_publications "$file")"
	else
		publications="$(docker_run_publications "$file")"
	fi

	while IFS='|' read -r line publication; do
		[ -n "$line" ] || continue
		actual_count=$((actual_count + 1))
		if [ "$publication" != "$safe_publication" ]; then
			fail "${description} must publish ${safe_publication}, got ${publication} at ${file}:${line}"
		fi
	done <<<"$publications"

	if [ "$actual_count" -ne "$expected_count" ]; then
		fail "${description} must contain exactly ${expected_count} ${kind} publication(s), found ${actual_count} in ${file}"
	fi
}

require_publications "examples/docker-compose.standard.yml" "yaml" 1 \
	"the canonical standard-mode Compose example"
require_publications "examples/docker-compose.with-sockguard.yml" "yaml" 1 \
	"the canonical standard-mode sockguard Compose example"
require_publications "README.md" "yaml" 1 \
	"the README copy of the standard-mode sockguard example"
require_publications "README.md" "docker run" 4 \
	"README standard-mode docker run instructions"
require_publications "docs/content/docs/getting-started.mdx" "yaml" 2 \
	"getting-started copies of the standard-mode Compose examples"
require_publications "docs/content/docs/getting-started.mdx" "docker run" 1 \
	"getting-started standard-mode docker run instructions"

if ! grep -Fqx 'BIND_ADDRESS=127.0.0.1' scripts/install.sh; then
	fail "the generated service config must bind plaintext standard mode to loopback"
fi
if grep -Fqx 'BIND_ADDRESS=0.0.0.0' scripts/install.sh; then
	fail "the generated service config must not bind plaintext standard mode to every interface"
fi

if [ "$failures" -ne 0 ]; then
	echo "${failures} standard-mode bind contract check(s) failed" >&2
	exit 1
fi

echo "Standard-mode bind contract checks passed."
