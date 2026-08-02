//go:build llgo && js && wasm && llgo.wasm_resume && !llgo.wasm_workers

package wasmevent

import _ "unsafe"

var runtimeDispatcher dispatcher

// ScheduleDispatch returns to JavaScript and asks it to reenter the scheduler
// after delay nanoseconds.
func ScheduleDispatch(delay uint64) {
	scheduleHostDispatch(runtimeDispatcher.schedule(), delay)
}

// CancelDispatch invalidates any outstanding host callback.
func CancelDispatch() {
	runtimeDispatcher.cancel()
}

// ConsumeDispatch reports whether generation is the current host wakeup.
func ConsumeDispatch(generation uintptr) bool {
	return runtimeDispatcher.consume(generation)
}

//go:linkname scheduleHostDispatch C.llgo_wasm_event_schedule_dispatch
func scheduleHostDispatch(generation uintptr, delay uint64)
