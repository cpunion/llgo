//go:build llgo && js && wasm && llgo.wasm_resume && !llgo.wasm_workers

package runtime

import "github.com/goplus/llgo/runtime/internal/wasmevent"

func initWasmResumeHost() {
}

func stopWasmResumeHost() {
	wasmevent.CancelDispatch()
}

//export llgo_wasm_event_dispatch
func llgo_wasm_event_dispatch(generation uintptr) {
	if wasmevent.ConsumeDispatch(generation) {
		RunWasmMain()
	}
}

func waitWasmResumeRunq() (*g, bool) {
	if gp := popWasmRunq(); gp != nil {
		return gp, false
	}
	deadline, ok := wasmevent.NextDeadline()
	if !ok {
		return nil, false
	}
	now := wasmevent.Now()
	var delay uint64
	if deadline > now {
		delay = uint64(deadline - now)
	}
	wasmevent.ScheduleDispatch(delay)
	return nil, true
}
