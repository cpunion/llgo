package reflect

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/ffi"
)

type callDeliveryResult = struct{ R0 []Value }

type callDeliveryState struct{ native *ffi.ResultDelivery }

//go:linkname borrowCallDelivery llgo.coroBorrowResult
func borrowCallDelivery(func(*callDeliveryResult), func(*callDeliveryResult, unsafe.Pointer, Value, []Value)) *callDeliveryResult

//go:linkname borrowInternalCallDelivery llgo.coroBorrowResult
func borrowInternalCallDelivery(func(*callDeliveryResult), func(*callDeliveryResult, unsafe.Pointer, Value, string, []Value)) *callDeliveryResult

//go:linkname bindCallDelivery llgo.coroBindResultState
func bindCallDelivery(*callDeliveryResult, unsafe.Pointer)

//go:linkname loadCallDelivery llgo.coroLoadResultState
func loadCallDelivery(*callDeliveryResult) unsafe.Pointer

func initializeCallDelivery(out *callDeliveryResult) { out.R0 = nil }

func dropCallDelivery(out *callDeliveryResult, _ unsafe.Pointer, _ Value, _ []Value) {
	state := (*callDeliveryState)(loadCallDelivery(out))
	out.R0 = nil
	if state != nil && state.native != nil {
		state.native.Drop()
	}
}

func dropInternalCallDelivery(out *callDeliveryResult, _ unsafe.Pointer, _ Value, _ string, _ []Value) {
	state := (*callDeliveryState)(loadCallDelivery(out))
	out.R0 = nil
	if state != nil && state.native != nil {
		state.native.Drop()
	}
}
