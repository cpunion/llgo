//go:build coro_runtime_adapter_test || coro_panic_payload_adapter_test

package runtime

import "unsafe"

// The named-source host island omits coro_nil_fault.go and its many runtime
// dependencies. Supply only its stable interface identity; production payload
// and recovered stack semantics are separately tested by LLGo-built programs.
type coroTestNilFaultError string

func (e *coroTestNilFaultError) Error() string { return string(*e) }
func (*coroTestNilFaultError) RuntimeError()   {}

var coroTestNilFaultValue = coroTestNilFaultError("runtime error: invalid memory address or nil pointer dereference")
var coroTestNilFaultInterface any = &coroTestNilFaultValue

func coroNilFaultPayloadV1() (unsafe.Pointer, unsafe.Pointer) {
	words := *(*eface)(unsafe.Pointer(&coroTestNilFaultInterface))
	return unsafe.Pointer(words._type), words.data
}
