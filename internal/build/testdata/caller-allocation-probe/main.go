package main

import "unsafe"

//go:linkname traceAlloc github.com/xgo-dev/llgo/runtime/internal/runtime/tinygogc.debugTraceAlloc
var traceAlloc bool

//go:linkname push github.com/xgo-dev/llgo/runtime/internal/runtime.PushCallerLocationFrame
func push(entry uintptr, name, file string, line int) int

//go:linkname pushWasm github.com/xgo-dev/llgo/runtime/internal/runtime.PushCallerLocationFrameWasm
func pushWasm(entry uintptr, name *byte, nameLen int, file *byte, fileLen, line int) int

//go:linkname record github.com/xgo-dev/llgo/runtime/internal/runtime.RecordCallerLocation
func record(entry uintptr, name, file string, line int)

//go:linkname recordWasm github.com/xgo-dev/llgo/runtime/internal/runtime.RecordCallerLocationWasm
func recordWasm(entry uintptr, name *byte, nameLen int, file *byte, fileLen, line int)

//go:linkname panicRecord github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocation
func panicRecord(entry uintptr, name, file string, line int)

//go:linkname panicRecordWasm github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm
func panicRecordWasm(entry uintptr, name *byte, nameLen int, file *byte, fileLen, line int)

//go:linkname pop github.com/xgo-dev/llgo/runtime/internal/runtime.PopCallerLocationFrame
func pop(mark int)

//go:noinline
func measure(name string, f func()) {
	f()
	println("TRACE_BEGIN", name)
	traceAlloc = true
	f()
	traceAlloc = false
	println("TRACE_END", name)
}

func main() {
	const entry, name, file = 42, "probe", "probe.go"
	nd, fd := unsafe.StringData(name), unsafe.StringData(file)
	measure("push-wasm", func() { mark := pushWasm(entry, nd, len(name), fd, len(file), 1); pop(mark) })
	measure("push-direct", func() { mark := push(entry, name, file, 1); pop(mark) })
	measure("record-wasm", func() { recordWasm(entry, nd, len(name), fd, len(file), 2) })
	measure("record-direct", func() { record(entry, name, file, 2) })
	measure("panic-wasm", func() { panicRecordWasm(entry, nd, len(name), fd, len(file), 3) })
	measure("panic-direct", func() { panicRecord(entry, name, file, 3) })
}
