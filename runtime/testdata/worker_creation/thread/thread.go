package thread

/*
#include <stdint.h>
int probe_join(uintptr_t thread);
*/
import "C"

import "unsafe"

type Thread = unsafe.Pointer

func Join(thread Thread, _ *unsafe.Pointer) int {
	return int(C.probe_join(C.uintptr_t(uintptr(thread))))
}
