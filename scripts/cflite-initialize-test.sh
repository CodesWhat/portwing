#!/usr/bin/env bash
# Check the C/Go startup ordering without needing an OSS-Fuzz image.
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-cflite-init.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

cat >"${test_root}/driver.c" <<'EOF'
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static int phase;

uintptr_t _cgo_wait_runtime_init_done(void) {
    if (phase != 0) abort();
    phase = 1;
    return 42;
}

void _cgo_release_context(uintptr_t context) {
    if (phase != 1 || context != 42) abort();
    phase = 2;
}

int __real_LLVMFuzzerInitialize(int *argc, char ***argv) {
    if (phase != 2) {
        fputs("coverage read before Go runtime initialization\n", stderr);
        return 1;
    }
    if (*argc != 1 || **argv == NULL) abort();
    *argc = 2;
    return 19;
}

extern int INITIALIZE(int *, char ***);

int main(int argc, char **argv) {
    int result = INITIALIZE(&argc, &argv);
    if (phase != 2) {
        fputs("initializer returned before Go runtime initialization\n", stderr);
        return 1;
    }
    return result == 19 && argc == 2 ? 0 : 1;
}
EOF

# The unsynchronized entry point must fail the same ordering check.
"${CC:-cc}" -Wall -Wextra -Werror -DINITIALIZE=__real_LLVMFuzzerInitialize \
	"${test_root}/driver.c" -o "${test_root}/baseline"
if "${test_root}/baseline" >"${test_root}/baseline.log" 2>&1; then
	echo "FAIL: startup fixture accepted uninitialized coverage tables" >&2
	exit 1
fi
grep -Fq 'coverage read before Go runtime initialization' "${test_root}/baseline.log"

"${CC:-cc}" -Wall -Wextra -Werror -DINITIALIZE=__wrap_LLVMFuzzerInitialize \
	"${test_root}/driver.c" "${1:-.clusterfuzzlite/initialize.c}" \
	-o "${test_root}/initialized"
"${test_root}/initialized"

echo "ClusterFuzzLite startup ordering checks passed."
