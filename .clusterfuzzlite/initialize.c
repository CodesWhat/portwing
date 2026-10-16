// SPDX-License-Identifier: MIT

#include <stdint.h>

extern uintptr_t _cgo_wait_runtime_init_done(void);
extern void _cgo_release_context(uintptr_t context);
extern int __real_LLVMFuzzerInitialize(int *argc, char ***argv);

/* Go's c-archive constructor starts the runtime on another thread. Wait for
 * its coverage registration before libFuzzer reads the tables, then retain
 * OSS-Fuzz's signal-handler setup. See golang/go#82127. */
int __wrap_LLVMFuzzerInitialize(int *argc, char ***argv) {
    uintptr_t context = _cgo_wait_runtime_init_done();
    _cgo_release_context(context);
    return __real_LLVMFuzzerInitialize(argc, argv);
}
