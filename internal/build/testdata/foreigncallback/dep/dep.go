package dep

/*
#cgo !windows LDFLAGS: -pthread
typedef int (*callback_t)(int);
extern int GoDepCallback(int);
int invoke_threads(int main_callback);
int invoke_callback_threads(callback_t callback);
*/
import "C"

import (
	"runtime"
	"sync/atomic"
)

var payload = []int32{22}
var calls int32

func Result(value int32) int32 {
	copy := append([]int32(nil), payload...)
	runtime.GC()
	atomic.AddInt32(&calls, 1)
	return value + copy[0]
}

//export GoDepCallback
func GoDepCallback(value C.int) C.int { return C.int(Result(int32(value))) }

func Run(mainCallback int) int { return int(C.invoke_threads(C.int(mainCallback))) }
func Count() int32             { return atomic.LoadInt32(&calls) }
func RunPointer() int          { return int(C.invoke_callback_threads((C.callback_t)(C.GoDepCallback))) }
