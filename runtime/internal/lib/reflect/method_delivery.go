package reflect

import (
	"unsafe"

	llruntime "github.com/xgo-dev/llgo/runtime/internal/runtime"
)

type boundMethodResult = struct{ R0 *callDeliveryState }

//go:linkname borrowBoundMethodResult llgo.coroBorrowResult
func borrowBoundMethodResult(func(*boundMethodResult), func(*boundMethodResult, unsafe.Pointer, *funcData, unsafe.Pointer, unsafe.Pointer)) *boundMethodResult

func initializeBoundMethodResult(out *boundMethodResult) {
	out.R0 = nil
	initializeBoundMethodNative(nil)
}

// The public result's private prefix is empty before invocation. Actual
// native result initialization belongs to callInto's separate result guard.
func initializeBoundMethodNative(_ *boundMethodResult) {}

func dropBoundMethodResult(out *boundMethodResult) {
	if out == nil {
		return
	}
	state := out.R0
	out.R0 = nil
	if state != nil && state.native != nil {
		state.native.Drop()
	}
}

func dropBoundMethodDelivery(out *boundMethodResult, _ unsafe.Pointer, _ *funcData, _ unsafe.Pointer, _ unsafe.Pointer) {
	dropBoundMethodResult(out)
}

// This fixed source producer owns the receiver actually read at invocation,
// the native resource result, and the second handoff into the public result.
// A nil caller output uses the ordinary producer fallback for go/discarded
// calls, including cancellation before the child reaches its entry body.
func boundMethodInvoke(fd *funcData, ret, values unsafe.Pointer) *callDeliveryState {
	out := borrowBoundMethodResult(initializeBoundMethodResult, dropBoundMethodDelivery)
	state := new(callDeliveryState)
	out.R0 = state
	var ins []Value
	if fd.nin != 0 {
		ins = unsafe.Slice((*Value)(values), fd.nin)
	}
	loan := new(callDeliveryResult)
	op := "Call"
	if fd.ftyp.Variadic() {
		op = "CallSlice"
	}
	rcvr, method := methodExpressionValue(op, fd.method, int(fd.method.flag)>>flagMethodShift)
	args := make([]Value, len(ins)+1)
	args[0] = rcvr
	copy(args[1:], ins)
	prev := llruntime.StartRecoverFrameAlias(fd.recoverFrom, method.UnsafePointer())
	method.callInto(op, args, state, loan)
	llruntime.EndRecoverFrameAlias(prev)
	if ret != nil {
		var offset uintptr
		for i, value := range loan.R0 {
			typ := fd.tout[i]
			size, alignment := llgoABITypeLayout(typ)
			offset = alignUp(offset, alignment)
			storeMakeFuncResult(add(ret, offset, ""), value, typ, size)
			offset += size
		}
	}
	return out.R0
}
