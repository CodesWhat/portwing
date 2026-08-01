#!/usr/bin/env bash
set -euo pipefail

TEST_TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/portwing-compat-test.XXXXXX")
cleanup() {
	rm -rf "$TEST_TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$TEST_TMP_DIR/bin"
cat >"$TEST_TMP_DIR/bin/curl" <<'STUB'
#!/usr/bin/env bash
printf 'not-json'
STUB
chmod +x "$TEST_TMP_DIR/bin/curl"

set +e
OUTPUT=$(PATH="$TEST_TMP_DIR/bin:$PATH" bash scripts/drydock-compat-check.sh 2>&1)
STATUS=$?
set -e

if [[ $STATUS -ne 1 ]]; then
	printf 'FAIL: malformed responses exited %s, want 1\n%s\n' "$STATUS" "$OUTPUT" >&2
	exit 1
fi
if [[ $OUTPUT != *"Results:"* ]]; then
	printf 'FAIL: malformed responses aborted before the compatibility summary\n%s\n' "$OUTPUT" >&2
	exit 1
fi
if [[ $OUTPUT != *"/api/watchers body is JSON array"* ]]; then
	printf 'FAIL: malformed watcher JSON did not reach the watcher assertion\n%s\n' "$OUTPUT" >&2
	exit 1
fi
if [[ $OUTPUT != *"/api/triggers body is JSON array"* ]]; then
	printf 'FAIL: malformed trigger JSON did not reach the trigger assertion\n%s\n' "$OUTPUT" >&2
	exit 1
fi

echo "Drydock compatibility script failure-path checks passed."
