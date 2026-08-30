package main

/*
#include <pthread.h>
#include <stdint.h>

extern int32_t llgo_gc_foreign_export_v1(int32_t value);

struct llgo_gc_foreign_call_v1 {
	int32_t value;
	int32_t result;
};

static void *llgo_gc_foreign_thread_v1(void *opaque) {
	struct llgo_gc_foreign_call_v1 *call =
		(struct llgo_gc_foreign_call_v1 *)opaque;
	call->result = llgo_gc_foreign_export_v1(
		llgo_gc_foreign_export_v1(call->value));
	return 0;
}

int32_t llgo_gc_foreign_call_v1(int32_t value) {
	struct llgo_gc_foreign_call_v1 call = {value, INT32_MIN};
	pthread_t thread;
	if (pthread_create(&thread, 0, llgo_gc_foreign_thread_v1, &call) != 0 ||
		pthread_join(thread, 0) != 0) {
		return INT32_MIN;
	}
	return call.result;
}
*/
import "C"

import (
	"runtime"
	_ "unsafe"
)

var ready chan int32

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=caller-thread reentry=managed-ingress memory=by-value
//go:linkname callForeign C.llgo_gc_foreign_call_v1
func callForeign(int32) int32

//export llgo_gc_foreign_export_v1
func llgo_gc_foreign_export_v1(value int32) (result int32) {
	defer func() { result++ }()
	payload := make([]byte, 1<<20)
	payload[0] = 0x2a
	value += <-ready
	runtime.GC()
	runtime.KeepAlive(payload)
	return value + int32(payload[0])
}

func main() {
	ready = make(chan int32)
	go func() {
		ready <- 10
		ready <- 20
	}()
	if result := callForeign(7); result != 123 {
		panic("foreign GC ingress")
	}
}
