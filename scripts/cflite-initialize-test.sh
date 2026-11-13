#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
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

#ifndef REAL_INITIALIZE
#define REAL_INITIALIZE __real_LLVMFuzzerInitialize
#endif

int REAL_INITIALIZE(int *argc, char ***argv) {
    if (phase != 2) {
        fputs("coverage read before Go runtime initialization\n", stderr);
        return 1;
    }
    if (*argc != 1 || **argv == NULL) abort();
    *argc = 2;
    return 19;
}

int runtime_ready(void) { return phase == 2; }
EOF

cat >"${test_root}/main.c" <<'EOF'
#include <stdio.h>
extern int runtime_ready(void);
extern int INITIALIZE(int *, char ***);

int main(int argc, char **argv) {
    int result = INITIALIZE(&argc, &argv);
    if (!runtime_ready()) {
        fputs("initializer returned before Go runtime initialization\n", stderr);
        return 1;
    }
    return result == 19 && argc == 2 ? 0 : 1;
}
EOF

# The unsynchronized entry point must fail the same ordering check.
"${CC:-cc}" -Wall -Wextra -Werror -DINITIALIZE=__real_LLVMFuzzerInitialize \
	"${test_root}/driver.c" "${test_root}/main.c" -o "${test_root}/baseline"
if "${test_root}/baseline" >"${test_root}/baseline.log" 2>&1; then
	echo "FAIL: startup fixture accepted uninitialized coverage tables" >&2
	exit 1
fi
grep -Fq 'coverage read before Go runtime initialization' "${test_root}/baseline.log"

"${CC:-cc}" -Wall -Wextra -Werror -DINITIALIZE=__wrap_LLVMFuzzerInitialize \
	"${test_root}/driver.c" "${test_root}/main.c" "${1:-.clusterfuzzlite/initialize.c}" \
	-o "${test_root}/initialized"
"${test_root}/initialized"

if [ "$(uname -s)" = "Linux" ]; then
	# Separate objects leave the normal entry point unresolved until the linker
	# applies the same --wrap flag used by the production fuzz build.
	"${CC:-cc}" -Wall -Wextra -Werror \
		-DREAL_INITIALIZE=LLVMFuzzerInitialize -DINITIALIZE=LLVMFuzzerInitialize \
		"${test_root}/driver.c" "${test_root}/main.c" "${1:-.clusterfuzzlite/initialize.c}" \
		-Wl,--wrap=LLVMFuzzerInitialize -o "${test_root}/linked"
	"${test_root}/linked"
	echo "ClusterFuzzLite production linker wrapping check passed."
else
	echo "SKIP: GNU linker wrapping check requires the Linux CI toolchain."
fi

echo "ClusterFuzzLite startup ordering checks passed."
