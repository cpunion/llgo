#include <stddef.h>
#include <stdint.h>

extern unsigned char __data_end;
extern unsigned char __global_base;
extern unsigned char __heap_base;
extern unsigned char __stack_high;

#define LLGO_WASM_PAGE_SIZE 65536

uintptr_t llgo_gc_globals_start(void) {
	uintptr_t start = (uintptr_t)&__global_base;
	uintptr_t end = (uintptr_t)&__data_end;
	uintptr_t stack_high = (uintptr_t)&__stack_high;
	if (stack_high > start && stack_high < end) {
		return stack_high;
	}
	return start;
}

uintptr_t llgo_gc_globals_end(void) {
	return (uintptr_t)&__data_end;
}

uintptr_t llgo_gc_heap_base(void) {
	return (uintptr_t)&__heap_base;
}

uintptr_t llgo_gc_stack_top(void) {
	return (uintptr_t)&__stack_high;
}

uintptr_t llgo_gc_memory_size(void) {
	return (uintptr_t)__builtin_wasm_memory_size(0) * LLGO_WASM_PAGE_SIZE;
}

int llgo_gc_grow_memory(uintptr_t required) {
	uintptr_t current = llgo_gc_memory_size();
	if (required <= current) {
		return 1;
	}
	uintptr_t pages = (required - current + LLGO_WASM_PAGE_SIZE - 1) /
		LLGO_WASM_PAGE_SIZE;
	return __builtin_wasm_memory_grow(0, pages) != (size_t)-1;
}
