#include <limits.h>
#include <stdint.h>

#include <emscripten.h>

#define LLGO_NANOSECONDS_PER_MILLISECOND UINT64_C(1000000)

extern void llgo_wasm_event_dispatch(uintptr_t generation);

static void llgo_wasm_event_dispatch_callback(void *arg) {
	llgo_wasm_event_dispatch((uintptr_t)arg);
}

void llgo_wasm_event_schedule_dispatch(uintptr_t generation, uint64_t nanoseconds) {
	uint64_t milliseconds = nanoseconds / LLGO_NANOSECONDS_PER_MILLISECOND;
	if (nanoseconds % LLGO_NANOSECONDS_PER_MILLISECOND != 0) {
		milliseconds++;
	}
	if (milliseconds > INT_MAX) {
		milliseconds = INT_MAX;
	}
	emscripten_async_call(
		llgo_wasm_event_dispatch_callback,
		(void *)generation,
		(int)milliseconds);
}
