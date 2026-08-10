//go:build coro_nil_fault_adapter_test

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// This test is a named production-source island. These definitions preserve
// only the interface prefix used by coro_nil_fault.go and replace the runtime's
// non-returning abort with an observable test trap.
type errorString string

func (s errorString) Error() string { return "runtime error: " + string(s) }
func (s errorString) RuntimeError() {}

type plainError string

func (s plainError) Error() string { return string(s) }
func (s plainError) RuntimeError() {}

type boundsErrorCode uint8

const (
	boundsIndex boundsErrorCode = iota
	boundsSliceAlen
	boundsSliceAcap
	boundsSliceB
	boundsSlice3Alen
	boundsSlice3Acap
	boundsSlice3B
	boundsSlice3C
	boundsConvert
)

type boundsError struct {
	x      int64
	y      int
	signed bool
	code   boundsErrorCode
}

func (e boundsError) Error() string {
	formats := [...]string{
		"index out of range [%x] with length %y",
		"slice bounds out of range [:%x] with length %y",
		"slice bounds out of range [:%x] with capacity %y",
		"slice bounds out of range [%x:%y]",
		"slice bounds out of range [::%x] with length %y",
		"slice bounds out of range [::%x] with capacity %y",
		"slice bounds out of range [:%x:%y]",
		"slice bounds out of range [%x:%y:]",
		"cannot convert slice with length %y to array or pointer to array with length %x",
	}
	negativeFormats := [...]string{
		"index out of range [%x]",
		"slice bounds out of range [:%x]",
		"slice bounds out of range [:%x]",
		"slice bounds out of range [%x:]",
		"slice bounds out of range [::%x]",
		"slice bounds out of range [::%x]",
		"slice bounds out of range [:%x:]",
		"slice bounds out of range [%x::]",
	}
	format := formats[e.code]
	if e.signed && e.x < 0 {
		format = negativeFormats[e.code]
	}
	out := []byte("runtime error: ")
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			out = append(out, format[i])
			continue
		}
		i++
		switch format[i] {
		case 'x':
			out = appendTestBoundsInt(out, e.x, e.signed)
		case 'y':
			out = appendTestBoundsInt(out, int64(e.y), true)
		}
	}
	return string(out)
}

func (boundsError) RuntimeError() {}

func appendTestBoundsInt(out []byte, value int64, signed bool) []byte {
	if signed && value < 0 {
		out = append(out, '-')
		value = -value
	}
	var buf [20]byte
	i := len(buf) - 1
	unsigned := uint64(value)
	for unsigned >= 10 {
		buf[i] = byte(unsigned%10 + '0')
		i--
		unsigned /= 10
	}
	buf[i] = byte(unsigned + '0')
	return append(out, buf[i:]...)
}

func AllocZ(size uintptr) unsafe.Pointer {
	switch size {
	case unsafe.Sizeof(boundsError{}):
		return unsafe.Pointer(new(boundsError))
	case unsafe.Sizeof(plainError("")):
		return unsafe.Pointer(new(plainError))
	default:
		panic("unexpected parameterized fault allocation size")
	}
}

type _type struct{}
type interfacetype struct{}
type itab struct {
	inter *interfacetype
	_type *_type
}
type iface struct {
	tab  *itab
	data unsafe.Pointer
}

func coroRuntimeAbort(message string) { panic(message) }

type coroNilFaultTestFrameV1 struct {
	handle     unsafe.Pointer
	header     *coro.HeaderV1
	storage    unsafe.Pointer
	descriptor unsafe.Pointer
	raw        unsafe.Pointer
	total      uintptr
	size       uintptr
	align      uintptr
	memory     []uintptr
}

func newCoroNilFaultTestFrameV1(t *testing.T, g *coro.G, parent unsafe.Pointer) *coroNilFaultTestFrameV1 {
	t.Helper()
	const (
		size  = uintptr(37)
		align = uintptr(16)
	)
	total, ok := coro.FrameAllocationSize(size, align)
	if !ok {
		t.Fatal("compute nil-fault frame allocation")
	}
	wordSize := unsafe.Sizeof(uintptr(0))
	memory := make([]uintptr, (total+wordSize-1)/wordSize)
	raw := unsafe.Pointer(&memory[0])
	descriptor := unsafe.Pointer(&coro.FrameDescriptorV1{Version: 1, ResultAlign: 1})
	storage, ok := coro.RegisterFrame(g, raw, total, size, align, descriptor)
	if !ok {
		t.Fatal("register nil-fault frame")
	}
	handle := unsafe.Pointer(new(byte))
	header := &coro.HeaderV1{
		G:             unsafe.Pointer(g),
		Parent:        parent,
		Descriptor:    descriptor,
		SuspendReason: uint16(coro.SuspendNone),
		Lifecycle:     uint16(coro.FrameInitialSuspended),
	}
	if !coro.PublishFrame(g, handle, header, storage) {
		t.Fatal("publish nil-fault frame")
	}
	return &coroNilFaultTestFrameV1{
		handle:     handle,
		header:     header,
		storage:    storage,
		descriptor: descriptor,
		raw:        raw,
		total:      total,
		size:       size,
		align:      align,
		memory:     memory,
	}
}

func releaseCoroNilFaultTestFrameV1(t *testing.T, g *coro.G, frame *coroNilFaultTestFrameV1) {
	t.Helper()
	raw, total, ok := coro.ReleaseFrame(g, frame.storage, frame.size, frame.align, frame.descriptor)
	if !ok || raw != frame.raw || total != frame.total {
		t.Fatalf("release nil-fault frame = (%p, %d, %t), want (%p, %d, true)",
			raw, total, ok, frame.raw, frame.total)
	}
}

func activateCoroNilFaultActionV1(
	t *testing.T,
	p *coro.P,
	g *coro.G,
	action coro.Action,
	handle unsafe.Pointer,
) coro.Action {
	t.Helper()
	var ok bool
	action, ok = coro.Checked(p, g, action, false)
	if !ok || action.Kind != coro.ActionResume || action.Handle != handle {
		t.Fatalf("activate nil-fault frame = (%+v, %t)", action, ok)
	}
	if outcome, caseID, task, sourceSlot, generation, ok := coro.TakeRunDecisionWords(g, 0, 0); !ok || outcome != 0 || caseID != 0 || task != 0 || sourceSlot != 0 || generation != 0 {
		t.Fatalf("take nil-fault resume gate = (%d, %d, %d, %d, %d, %t)",
			outcome, caseID, task, sourceSlot, generation, ok)
	}
	return action
}

func beginCoroNilFaultRootV1(
	t *testing.T,
	p *coro.P,
	g *coro.G,
	frame *coroNilFaultTestFrameV1,
) coro.Action {
	t.Helper()
	if !coro.AdoptRoot(g, frame.handle) || !coro.Enqueue(p, g) {
		t.Fatal("adopt and enqueue nil-fault root")
	}
	if next, ok := coro.NextRunnable(p); !ok || next != g {
		t.Fatalf("dequeue nil-fault root = (%p, %t)", next, ok)
	}
	action, ok := coro.BeginRunG(p, g)
	if !ok || action.Kind != coro.ActionCheckResume || action.Handle != frame.handle {
		t.Fatalf("begin nil-fault root = (%+v, %t)", action, ok)
	}
	return activateCoroNilFaultActionV1(t, p, g, action, frame.handle)
}

func TestCoroNilFaultPayloadV1IsAllocationFreeAndStable(t *testing.T) {
	want := *(*iface)(unsafe.Pointer(&memoryError))
	if want.tab == nil || want.tab._type == nil {
		t.Fatal("memoryError has no concrete interface type")
	}
	if allocations := testing.AllocsPerRun(1000, func() {
		typeWord, dataWord := coroNilFaultPayloadV1()
		if typeWord != unsafe.Pointer(want.tab._type) || dataWord != want.data {
			panic("unstable memoryError interface words")
		}
	}); allocations != 0 {
		t.Fatalf("nil-fault payload allocations = %v, want 0", allocations)
	}
	runtime.KeepAlive(memoryError)
}

func TestCoroFaultPayloadV1KindsAreStableDistinctAndAllocationFree(t *testing.T) {
	tests := []struct {
		name  string
		kind  uint32
		value *error
	}{
		{name: "nil", kind: coroFaultNilV1, value: &memoryError},
		{name: "index-bounds", kind: coroFaultIndexBoundsV1, value: &coroIndexBoundsErrorV1},
		{name: "channel-send-closed", kind: coroFaultChannelSendClosedV1, value: &coroChannelSendClosedErrorV1},
		{name: "unsafe-slice-len", kind: coroFaultUnsafeSliceLenV1, value: &coroUnsafeSliceLenErrorV1},
		{name: "unsafe-slice-nil", kind: coroFaultUnsafeSliceNilV1, value: &coroUnsafeSliceNilErrorV1},
		{name: "channel-close-nil", kind: coroFaultChannelCloseNilV1, value: &coroChannelCloseNilErrorV1},
		{name: "channel-close-closed", kind: coroFaultChannelCloseClosedV1, value: &coroChannelCloseClosedErrorV1},
		{name: "unsafe-string-len", kind: coroFaultUnsafeStringLenV1, value: &coroUnsafeStringLenErrorV1},
		{name: "unsafe-string-nil", kind: coroFaultUnsafeStringNilV1, value: &coroUnsafeStringNilErrorV1},
		{name: "slice-convert", kind: coroFaultSliceConvertV1, value: &coroSliceConvertErrorV1},
		{name: "integer-divide-by-zero", kind: coroFaultIntegerDivideByZeroV1, value: &coroIntegerDivideByZeroErrorV1},
	}
	words := make(map[uint32][2]unsafe.Pointer, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wantType, wantData := coroErrorFaultPayloadV1(test.value)
			if wantType == nil || wantData == nil {
				t.Fatalf("kind %d has empty payload = (%p, %p)", test.kind, wantType, wantData)
			}
			if allocations := testing.AllocsPerRun(1000, func() {
				var typeWord, dataWord unsafe.Pointer
				__llgo_coro_fault_payload_v1(
					test.kind,
					unsafe.Pointer(&typeWord),
					unsafe.Pointer(&dataWord),
				)
				if typeWord != wantType || dataWord != wantData {
					panic("unstable implicit-fault interface words")
				}
			}); allocations != 0 {
				t.Fatalf("kind %d payload allocations = %v, want 0", test.kind, allocations)
			}
			words[test.kind] = [2]unsafe.Pointer{wantType, wantData}
			runtime.KeepAlive(*test.value)
		})
	}
	for left := uint32(coroFaultNilV1); left <= coroFaultIntegerDivideByZeroV1; left++ {
		for right := left + 1; right <= coroFaultIntegerDivideByZeroV1; right++ {
			if words[left] == words[right] {
				t.Fatalf("fault kinds %d and %d share payload words: (%p, %p)",
					left, right, words[left][0], words[left][1])
			}
		}
	}
	if typeWord, dataWord := coroFaultPayloadV1(0); typeWord != nil || dataWord != nil {
		t.Fatalf("unknown kind 0 payload = (%p, %p), want empty", typeWord, dataWord)
	}
	if typeWord, dataWord := coroFaultPayloadV1(^uint32(0)); typeWord != nil || dataWord != nil {
		t.Fatalf("unknown maximum kind payload = (%p, %p), want empty", typeWord, dataWord)
	}
	if _, ok := coroSliceConvertErrorV1.(interface{ RuntimeError() }); !ok {
		t.Fatal("slice-conversion fault payload does not implement runtime.Error")
	}
	if got, want := coroSliceConvertErrorV1.Error(),
		"runtime error: cannot convert slice to array or pointer to array: length too short"; got != want {
		t.Fatalf("static slice-conversion message = %q, want %q", got, want)
	}
	if got, want := coroIntegerDivideByZeroErrorV1.Error(), "runtime error: integer divide by zero"; got != want {
		t.Fatalf("integer divide-by-zero message = %q, want %q", got, want)
	}
}

func TestCoroPanicWrapPayloadV1PreservesExactMessage(t *testing.T) {
	recvType := "example.org/pair.Box[int, example.org/item.Value]"
	methodName := "Read"
	var typeWord, dataWord unsafe.Pointer
	__llgo_coro_wrap_nil_payload_v1(
		unsafe.Pointer(unsafe.StringData(recvType)),
		uintptr(len(recvType)),
		unsafe.Pointer(unsafe.StringData(methodName)),
		uintptr(len(methodName)),
		unsafe.Pointer(&typeWord),
		unsafe.Pointer(&dataWord),
	)
	if typeWord == nil || dataWord == nil {
		t.Fatalf("value-method nil payload = (%p, %p)", typeWord, dataWord)
	}
	got := *(*plainError)(dataWord)
	want := plainError(
		"value method example.org/pair.Box[int, example.org/item.Value].Read " +
			"called using nil *Box[int, example.org/item.Value] pointer",
	)
	if got != want {
		t.Fatalf("value-method nil payload = %q, want %q", got, want)
	}
	wantType, _ := coroErrorFaultPayloadV1(&coroPanicWrapErrorTypeV1)
	if typeWord != wantType {
		t.Fatalf("value-method nil type word = %p, want %p", typeWord, wantType)
	}
}

func TestCoroFaultPayloadV2CarriesSliceConversionOperands(t *testing.T) {
	typeWord, dataWord := coroFaultPayloadV2(coroFaultSliceConvertV1, 9, 8)
	wantType, _ := coroErrorFaultPayloadV1(&coroBoundsErrorTypeV2)
	if typeWord == nil || typeWord != wantType || dataWord == nil {
		t.Fatalf("parameterized payload = (%p, %p), want type %p and data", typeWord, dataWord, wantType)
	}
	payload := *(*boundsError)(dataWord)
	if payload.x != 9 || payload.y != 8 || !payload.signed || payload.code != boundsConvert {
		t.Fatalf("parameterized bounds payload = %+v", payload)
	}
	if got, want := payload.Error(),
		"runtime error: cannot convert slice with length 8 to array or pointer to array with length 9"; got != want {
		t.Fatalf("parameterized slice-conversion message = %q, want %q", got, want)
	}
	if _, ok := any(payload).(interface{ RuntimeError() }); !ok {
		t.Fatal("parameterized slice-conversion payload does not implement runtime.Error")
	}

	var hookType, hookData unsafe.Pointer
	__llgo_coro_fault_payload_v2(
		coroFaultSliceConvertV1,
		9,
		8,
		unsafe.Pointer(&hookType),
		unsafe.Pointer(&hookData),
	)
	if hookType != typeWord || hookData == nil {
		t.Fatalf("parameterized payload hook = (%p, %p), want type %p and data", hookType, hookData, typeWord)
	}
	if got := (*boundsError)(hookData).Error(); got != payload.Error() {
		t.Fatalf("parameterized hook message = %q, want %q", got, payload.Error())
	}

	for _, test := range []struct {
		name string
		kind uint32
		arg0 uint64
		arg1 uintptr
	}{
		{name: "unknown kind", kind: ^uint32(0)},
		{name: "operand on static kind", kind: coroFaultNilV1, arg0: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if typ, data := coroFaultPayloadV2(test.kind, test.arg0, test.arg1); typ != nil || data != nil {
				t.Fatalf("invalid parameterized payload = (%p, %p)", typ, data)
			}
		})
	}
	runtime.KeepAlive(coroBoundsErrorTypeV2)
}

func TestCoroFaultPayloadV2CarriesExactBoundsErrors(t *testing.T) {
	tests := []struct {
		name   string
		code   boundsErrorCode
		signed bool
		arg0   uint64
		arg1   uintptr
		want   string
	}{
		{
			name: "negative index", code: boundsIndex, signed: true,
			arg0: ^uint64(0), arg1: 3,
			want: "runtime error: index out of range [-1]",
		},
		{
			name: "unsigned index", code: boundsIndex, signed: false,
			arg0: ^uint64(0), arg1: 3,
			want: "runtime error: index out of range [18446744073709551615] with length 3",
		},
		{
			name: "slice length", code: boundsSliceAlen, signed: true,
			arg0: 4, arg1: 3,
			want: "runtime error: slice bounds out of range [:4] with length 3",
		},
		{
			name: "slice capacity", code: boundsSliceAcap, signed: true,
			arg0: 4, arg1: 3,
			want: "runtime error: slice bounds out of range [:4] with capacity 3",
		},
		{
			name: "slice low", code: boundsSliceB, signed: false,
			arg0: ^uint64(0), arg1: 0,
			want: "runtime error: slice bounds out of range [18446744073709551615:0]",
		},
		{
			name: "full slice length", code: boundsSlice3Alen, signed: true,
			arg0: 4, arg1: 3,
			want: "runtime error: slice bounds out of range [::4] with length 3",
		},
		{
			name: "full slice capacity", code: boundsSlice3Acap, signed: true,
			arg0: 4, arg1: 3,
			want: "runtime error: slice bounds out of range [::4] with capacity 3",
		},
		{
			name: "full slice high", code: boundsSlice3B, signed: true,
			arg0: 2, arg1: 1,
			want: "runtime error: slice bounds out of range [:2:1]",
		},
		{
			name: "full slice low", code: boundsSlice3C, signed: true,
			arg0: 1, arg1: 0,
			want: "runtime error: slice bounds out of range [1:0:]",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind := coroFaultBoundsBaseV2 + uint32(test.code)*2
			if !test.signed {
				kind++
			}
			code, signed, ok := coroFaultBoundsMetadataV2(kind)
			if !ok || code != test.code || signed != test.signed {
				t.Fatalf("bounds metadata = (%d, %t, %t), want (%d, %t, true)",
					code, signed, ok, test.code, test.signed)
			}
			typeWord, dataWord := coroFaultPayloadV2(kind, test.arg0, test.arg1)
			if typeWord == nil || dataWord == nil {
				t.Fatal("exact bounds payload is empty")
			}
			payload := *(*boundsError)(dataWord)
			if payload.x != int64(test.arg0) || payload.y != int(test.arg1) ||
				payload.signed != test.signed || payload.code != test.code {
				t.Fatalf("exact bounds payload = %+v", payload)
			}
			if got := payload.Error(); got != test.want {
				t.Fatalf("bounds message = %q, want %q", got, test.want)
			}
		})
	}
	for _, kind := range []uint32{coroFaultBoundsBaseV2 - 1, coroFaultBoundsLimitV2} {
		if _, _, ok := coroFaultBoundsMetadataV2(kind); ok {
			t.Fatalf("out-of-range bounds kind %d was accepted", kind)
		}
	}
	runtime.KeepAlive(coroBoundsErrorTypeV2)
}

func TestCoroFaultPayloadHookFeedsDirectRecover(t *testing.T) {
	var typeWord, dataWord unsafe.Pointer
	__llgo_coro_fault_payload_v1(
		coroFaultNilV1,
		unsafe.Pointer(&typeWord),
		unsafe.Pointer(&dataWord),
	)
	wantType, wantData := coroNilFaultPayloadV1()
	if typeWord != wantType || dataWord != wantData {
		t.Fatalf("materialized recover payload = (%p, %p), want (%p, %p)", typeWord, dataWord, wantType, wantData)
	}

	g := new(coro.G)
	if !coro.InitG(g) {
		t.Fatal("initialize fault-recover G")
	}
	parent := newCoroNilFaultTestFrameV1(t, g, nil)
	child := newCoroNilFaultTestFrameV1(t, g, parent.handle)
	p := new(coro.P)
	parentAction := beginCoroNilFaultRootV1(t, p, g, parent)
	parent.header.SuspendReason = uint16(coro.SuspendCall)
	parent.header.Lifecycle = uint16(coro.FrameSuspended)
	if !coro.PrepareAwaitCompletionRecover(g, parent.handle, child.handle, typeWord, dataWord) {
		t.Fatal("prepare recoverable fault child await")
	}
	action, ok := coro.Resumed(p, g, parentAction)
	if !ok || action.Kind != coro.ActionCheckResume || action.Handle != child.handle {
		t.Fatalf("dispatch fault-recover child = (%+v, %t)", action, ok)
	}
	action = activateCoroNilFaultActionV1(t, p, g, action, child.handle)
	child.header.SuspendReason = uint16(coro.SuspendNone)
	child.header.Lifecycle = uint16(coro.FrameActive)
	snapshot, recovered, valid := coro.TakeRecover(g, child.handle)
	if !valid || !recovered || snapshot.TypeWord != typeWord || snapshot.DataWord != dataWord {
		t.Fatalf("take materialized fault recover = (%+v, %t, %t), want (%p, %p)",
			snapshot, recovered, valid, typeWord, dataWord)
	}

	child.header.SuspendReason = uint16(coro.SuspendFrameComplete)
	child.header.Lifecycle = uint16(coro.FrameFinalSuspended)
	if !coro.PrepareComplete(g, child.handle, child.header) {
		t.Fatal("publish recovered fault child return")
	}
	action, ok = coro.Resumed(p, g, action)
	if !ok || action.Kind != coro.ActionCheckDestroy || action.Handle != child.handle {
		t.Fatalf("dispatch recovered fault child destroy = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(p, g, action, true)
	if !ok || action.Kind != coro.ActionDestroy || action.Handle != child.handle {
		t.Fatalf("activate recovered fault child destroy = (%+v, %t)", action, ok)
	}
	releaseCoroNilFaultTestFrameV1(t, g, child)
	action, ok = coro.Destroyed(p, g, action)
	if !ok || action.Kind != coro.ActionCheckResume || action.Handle != parent.handle {
		t.Fatalf("resume recovered fault parent = (%+v, %t)", action, ok)
	}
	action = activateCoroNilFaultActionV1(t, p, g, action, parent.handle)
	parent.header.SuspendReason = uint16(coro.SuspendNone)
	parent.header.Lifecycle = uint16(coro.FrameActive)
	completion, consumed := coro.ConsumeAwaitCompletion(g, parent.handle)
	if !consumed || completion != (coro.CompletionSnapshot{Status: coro.CompletionReturnRecovered}) {
		t.Fatalf("consume recovered fault child = (%+v, %t)", completion, consumed)
	}
	runtime.KeepAlive(parent.memory)
	runtime.KeepAlive(child.memory)
	runtime.KeepAlive(memoryError)
}

func TestCoroFaultPayloadHookRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		kind    uint32
		typeOut unsafe.Pointer
		dataOut unsafe.Pointer
		want    string
	}{
		{name: "unknown-kind", kind: ^uint32(0), typeOut: unsafe.Pointer(new(unsafe.Pointer)), dataOut: unsafe.Pointer(new(unsafe.Pointer)), want: "invalid coroutine fault payload kind"},
		{name: "nil-type-output", kind: coroFaultNilV1, dataOut: unsafe.Pointer(new(unsafe.Pointer)), want: "invalid coroutine fault payload output"},
		{name: "nil-data-output", kind: coroFaultNilV1, typeOut: unsafe.Pointer(new(unsafe.Pointer)), want: "invalid coroutine fault payload output"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != test.want {
					t.Fatalf("fault payload abort = %#v, want %q", recovered, test.want)
				}
			}()
			__llgo_coro_fault_payload_v1(test.kind, test.typeOut, test.dataOut)
			t.Fatal("invalid fault payload hook returned")
		})
	}
}

func TestCoroNilFaultHookPublishesRootPanicRecord(t *testing.T) {
	g := new(coro.G)
	if !coro.InitG(g) {
		t.Fatal("initialize nil-fault root G")
	}
	frame := newCoroNilFaultTestFrameV1(t, g, nil)
	p := new(coro.P)
	action := beginCoroNilFaultRootV1(t, p, g, frame)
	frame.header.SuspendReason = uint16(coro.SuspendPanic)
	frame.header.Lifecycle = uint16(coro.FrameFinalSuspended)
	__llgo_coro_fault_prepare_v1(unsafe.Pointer(g), frame.handle, unsafe.Pointer(frame.header), coroFaultNilV1)

	wantType, wantData := coroNilFaultPayloadV1()
	record, published := coro.LoadPanicRecord(g)
	if !published || record.Status != coro.ExplicitStatusPanic ||
		record.TypeWord != wantType || record.DataWord != wantData {
		t.Fatalf("root nil-fault record = (%+v, %t), want (%p, %p)",
			record, published, wantType, wantData)
	}
	if next, ok := coro.Resumed(p, g, action); !ok ||
		next.Kind != coro.ActionCheckDestroy || next.Handle != frame.handle {
		t.Fatalf("dispatch nil-fault root destroy = (%+v, %t)", next, ok)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(memoryError)
}

func TestCoroNilFaultHookPublishesAwaitedChildCompletion(t *testing.T) {
	g := new(coro.G)
	if !coro.InitG(g) {
		t.Fatal("initialize nil-fault child G")
	}
	parent := newCoroNilFaultTestFrameV1(t, g, nil)
	child := newCoroNilFaultTestFrameV1(t, g, parent.handle)
	p := new(coro.P)
	parentAction := beginCoroNilFaultRootV1(t, p, g, parent)
	parent.header.SuspendReason = uint16(coro.SuspendCall)
	parent.header.Lifecycle = uint16(coro.FrameSuspended)
	if !coro.PrepareAwaitCompletion(g, parent.handle, child.handle) {
		t.Fatal("prepare nil-fault child await")
	}
	action, ok := coro.Resumed(p, g, parentAction)
	if !ok || action.Kind != coro.ActionCheckResume || action.Handle != child.handle {
		t.Fatalf("dispatch nil-fault child = (%+v, %t)", action, ok)
	}
	action = activateCoroNilFaultActionV1(t, p, g, action, child.handle)
	child.header.SuspendReason = uint16(coro.SuspendPanic)
	child.header.Lifecycle = uint16(coro.FrameFinalSuspended)
	__llgo_coro_fault_prepare_v1(unsafe.Pointer(g), child.handle, unsafe.Pointer(child.header), coroFaultNilV1)
	if record, published := coro.LoadPanicRecord(g); published || record != (coro.PanicRecordSnapshot{}) {
		t.Fatalf("awaited nil fault escaped to root = (%+v, %t)", record, published)
	}

	action, ok = coro.Resumed(p, g, action)
	if !ok || action.Kind != coro.ActionCheckDestroy || action.Handle != child.handle {
		t.Fatalf("dispatch nil-fault child destroy = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(p, g, action, true)
	if !ok || action.Kind != coro.ActionDestroy || action.Handle != child.handle {
		t.Fatalf("activate nil-fault child destroy = (%+v, %t)", action, ok)
	}
	releaseCoroNilFaultTestFrameV1(t, g, child)
	action, ok = coro.Destroyed(p, g, action)
	if !ok || action.Kind != coro.ActionCheckResume || action.Handle != parent.handle {
		t.Fatalf("commit nil-fault child destroy = (%+v, %t)", action, ok)
	}
	action = activateCoroNilFaultActionV1(t, p, g, action, parent.handle)
	parent.header.SuspendReason = uint16(coro.SuspendNone)
	parent.header.Lifecycle = uint16(coro.FrameActive)
	snapshot, consumed := coro.ConsumeAwaitCompletion(g, parent.handle)
	wantType, wantData := coroNilFaultPayloadV1()
	if !consumed || snapshot.Status != coro.CompletionPanic ||
		snapshot.TypeWord != wantType || snapshot.DataWord != wantData {
		t.Fatalf("consume nil-fault child = (%+v, %t), want (%p, %p)",
			snapshot, consumed, wantType, wantData)
	}
	if record, published := coro.LoadPanicRecord(g); published || record != (coro.PanicRecordSnapshot{}) {
		t.Fatalf("consumed nil fault poisoned root = (%+v, %t)", record, published)
	}
	runtime.KeepAlive(parent.memory)
	runtime.KeepAlive(child.memory)
	runtime.KeepAlive(memoryError)
}

func TestCoroNilFaultHookRejectsInvalidPhysicalG(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != "invalid coroutine fault panic handoff" {
			t.Fatalf("nil-fault runtime abort = %#v", recovered)
		}
	}()
	__llgo_coro_fault_prepare_v1(nil, nil, nil, coroFaultNilV1)
	t.Fatal("invalid nil-fault hook returned")
}

func TestCoroFaultHookRejectsUnknownKindBeforePublication(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != "invalid coroutine fault panic handoff" {
			t.Fatalf("unknown fault-kind runtime abort = %#v", recovered)
		}
	}()
	__llgo_coro_fault_prepare_v1(nil, nil, nil, ^uint32(0))
	t.Fatal("unknown fault-kind hook returned")
}
