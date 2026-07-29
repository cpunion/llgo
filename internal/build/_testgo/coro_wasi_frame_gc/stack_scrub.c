#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#define LLGO_CORO_WASM_GC_SCRUB_WORDS 4096
#define LLGO_CORO_WASM_NON_POINTER ((uintptr_t)1 << 31)

__attribute__((noinline))
uintptr_t llgo_coro_wasi_frame_gc_scrub_stack(uintptr_t seed) {
	volatile uintptr_t words[LLGO_CORO_WASM_GC_SCRUB_WORDS];
	uintptr_t checksum = LLGO_CORO_WASM_NON_POINTER;

	for (size_t index = 0; index < LLGO_CORO_WASM_GC_SCRUB_WORDS; ++index) {
		words[index] = LLGO_CORO_WASM_NON_POINTER |
			((seed + (uintptr_t)index * 0x1e3779b1u) &
			 ~LLGO_CORO_WASM_NON_POINTER);
	}
	for (size_t index = 0; index < LLGO_CORO_WASM_GC_SCRUB_WORDS; ++index) {
		checksum = (checksum << 7) ^ (checksum >> 3) ^ words[index];
	}
	return checksum | LLGO_CORO_WASM_NON_POINTER;
}

__attribute__((noreturn))
void llgo_coro_wasi_frame_gc_exit(int32_t status) {
	_Exit(status);
}
