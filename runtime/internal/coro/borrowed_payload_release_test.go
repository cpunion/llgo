package coro

import (
	"runtime"
	"testing"
	"unsafe"
)

type borrowedPayloadTestStorage struct {
	Prefix   [64]byte
	Header   HeaderV1
	Payload  [32]uintptr
	Metadata BorrowedFrameStorageV2
	Tail     [64]byte
}

func borrowedPayloadTestFrame(t *testing.T, g *G) (*borrowedPayloadTestStorage, *Frame, *FrameDescriptorV1) {
	t.Helper()
	storage := new(borrowedPayloadTestStorage)
	for i := range storage.Payload {
		storage.Payload[i] = ^uintptr(0)
	}
	for i := range storage.Prefix {
		storage.Prefix[i] = 0xa5
		storage.Tail[i] = 0x5a
	}
	descriptor := &FrameDescriptorV1{Version: 1, ResultAlign: 1, Function: "test.borrowedPayload", File: "payload.go"}
	handle := unsafe.Pointer(storage)
	if !PublishFrameV3Compiler(g, handle, &storage.Header, nil, unsafe.Pointer(&storage.Metadata), unsafe.Pointer(descriptor), nil) {
		t.Fatal("publish")
	}
	frame := (*Frame)(unsafe.Pointer(&storage.Metadata))
	frame.state = FrameDestroyPending
	storage.Header.Lifecycle = uint16(FrameDestroyPending)
	g.destroyTarget = frame
	return storage, frame, descriptor
}

func TestCompilerBorrowedPayloadRelease(t *testing.T) {
	g := new(G)
	if !InitG(g) {
		t.Fatal("init")
	}
	storage, frame, descriptor := borrowedPayloadTestFrame(t, g)
	before, header := *frame, storage.Header
	for _, tc := range []struct {
		name        string
		size, align uintptr
		descriptor  unsafe.Pointer
	}{
		{"short-span", unsafe.Offsetof(storage.Metadata), unsafe.Alignof(*storage), unsafe.Pointer(descriptor)},
		{"invalid-alignment", unsafe.Sizeof(*storage), 3, unsafe.Pointer(descriptor)},
		{"wrong-descriptor", unsafe.Sizeof(*storage), unsafe.Alignof(*storage), unsafe.Pointer(new(byte))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := PrepareBorrowedFrameReleaseCompiler(g, unsafe.Pointer(storage), tc.size, tc.align, tc.descriptor); ok {
				t.Fatal("invalid release accepted")
			}
			if *frame != before || storage.Header != header || g.frames != frame || g.destroyTarget != frame {
				t.Fatal("invalid release mutated ownership")
			}
		})
	}
	metadata, retained, ok := PrepareBorrowedFrameReleaseCompiler(g, unsafe.Pointer(storage), unsafe.Sizeof(*storage), unsafe.Alignof(*storage), unsafe.Pointer(descriptor))
	if !ok || retained || metadata != unsafe.Pointer(frame) || g.frames != nil || g.destroyTarget != nil {
		t.Fatal("release")
	}
	Zero(unsafe.Pointer(storage), unsafe.Sizeof(*storage))
	if *storage != (borrowedPayloadTestStorage{}) {
		t.Fatal("payload still contains references")
	}
	runtime.KeepAlive(descriptor)
}

func TestCompilerBorrowedPanicTraceRelocation(t *testing.T) {
	g := new(G)
	if !InitG(g) {
		t.Fatal("init")
	}
	storage, frame, descriptor := borrowedPayloadTestFrame(t, g)
	handle := unsafe.Pointer(storage)
	parent := &Frame{owner: g, completion: CompletionRecord{status: CompletionPanic, child: handle, typeWord: unsafe.Pointer(new(byte)), dataWord: unsafe.Pointer(new(byte))}}
	frame.parent = parent
	frame.retainPanicTrace = true
	metadata, retained, ok := PrepareBorrowedFrameReleaseCompiler(g, handle, unsafe.Sizeof(*storage), unsafe.Alignof(*storage), unsafe.Pointer(descriptor))
	if !ok || !retained || g.panicTraceTail != frame {
		t.Fatal("retain borrowed trace")
	}
	owned := new(Frame)
	raw := unsafe.Pointer(owned)
	total := BorrowedPanicTraceAllocationSize()
	if RelocateBorrowedPanicTraceFrameCompiler(g, metadata, metadata, total) {
		t.Fatal("overlapping relocation accepted")
	}
	if !RelocateBorrowedPanicTraceFrameCompiler(g, metadata, raw, total) {
		t.Fatal("relocate")
	}
	Zero(handle, unsafe.Sizeof(*storage))
	if g.panicTraceHead != owned || g.panicTraceTail != owned || owned.borrowedStorage() || panicTraceCarrier(g) != parent {
		t.Fatal("trace retained borrowed storage")
	}
	snapshot, next, ok := retainedPanicTraceFrame(g, owned)
	if !ok || next != nil || snapshot.Function != "test.borrowedPayload" {
		t.Fatal("trace lost after clearing LLVM storage")
	}
	if !stagePanicTraceDiscard(g) {
		t.Fatal("stage")
	}
	got, size, ok := TakeDiscardedPanicTraceFrame(g)
	if !ok || got != raw || size != total {
		t.Fatal("owned trace release")
	}
	if got, _, ok := TakeDiscardedPanicTraceFrame(g); !ok || got != nil {
		t.Fatal("trace did not drain")
	}
	runtime.KeepAlive(storage)
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(owned)
}

func TestCompilerNestedBorrowedTraceOutlivesLLVMStorage(t *testing.T) {
	g := new(G)
	if !InitG(g) {
		t.Fatal("init")
	}
	// The child's complete frame lies inside its parent's LLVM allocation.
	// Both ranges will be overwritten before any retained trace is inspected.
	outer := new(struct{ Parent, Child borrowedPayloadTestStorage })
	publish := func(storage *borrowedPayloadTestStorage, name string) *Frame {
		descriptor := &FrameDescriptorV1{Version: 1, ResultAlign: 1, Function: name}
		if !PublishFrameV3Compiler(g, unsafe.Pointer(storage), &storage.Header, nil, unsafe.Pointer(&storage.Metadata), unsafe.Pointer(descriptor), nil) {
			t.Fatal("publish")
		}
		frame := (*Frame)(unsafe.Pointer(&storage.Metadata))
		frame.state = FrameDestroyPending
		storage.Header.Lifecycle = uint16(FrameDestroyPending)
		return frame
	}
	parent := publish(&outer.Parent, "test.parent")
	child := publish(&outer.Child, "test.child")
	child.parent = parent
	child.retainPanicTrace = true
	typeWord, dataWord := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	parent.completion = CompletionRecord{status: CompletionPanic, child: child.handle, typeWord: typeWord, dataWord: dataWord}
	g.destroyTarget = child
	metadata, retained, ok := PrepareBorrowedFrameReleaseCompiler(g, child.handle, unsafe.Sizeof(outer.Child), unsafe.Alignof(outer.Child), outer.Child.Header.Descriptor)
	if !ok || !retained {
		t.Fatal("child prepare")
	}
	ownedChild := new(Frame)
	total := BorrowedPanicTraceAllocationSize()
	if !RelocateBorrowedPanicTraceFrameCompiler(g, metadata, unsafe.Pointer(ownedChild), total) {
		t.Fatal("child relocate")
	}
	Zero(unsafe.Pointer(&outer.Child), unsafe.Sizeof(outer.Child))
	carrier := &Frame{owner: g, completion: CompletionRecord{status: CompletionPanic, child: parent.handle, typeWord: typeWord, dataWord: dataWord}}
	parent.parent = carrier
	parent.completion = CompletionRecord{}
	parent.retainPanicTrace = true
	g.destroyTarget = parent
	metadata, retained, ok = PrepareBorrowedFrameReleaseCompiler(g, parent.handle, unsafe.Sizeof(*outer), unsafe.Alignof(*outer), outer.Parent.Header.Descriptor)
	if !ok || !retained {
		t.Fatal("parent prepare")
	}
	ownedParent := new(Frame)
	before := *ownedChild
	if RelocateBorrowedPanicTraceFrameCompiler(g, metadata, unsafe.Pointer(ownedParent), total-1) {
		t.Fatal("short allocation accepted")
	}
	if *ownedChild != before || g.panicTraceTail != parent {
		t.Fatal("failed relocation mutated chain")
	}
	if !RelocateBorrowedPanicTraceFrameCompiler(g, metadata, unsafe.Pointer(ownedParent), total) {
		t.Fatal("parent relocate")
	}
	Zero(unsafe.Pointer(outer), unsafe.Sizeof(*outer))
	snapshot, next, ok := retainedPanicTraceFrame(g, ownedChild)
	if !ok || snapshot.Function != "test.child" || next != ownedParent || ownedChild.parent != ownedParent {
		t.Fatal("child ancestry lost after parent reuse")
	}
	snapshot, next, ok = retainedPanicTraceFrame(g, ownedParent)
	if !ok || snapshot.Function != "test.parent" || next != nil || panicTraceCarrier(g) != carrier {
		t.Fatal("parent trace lost after storage reuse")
	}
	if !stagePanicTraceDiscard(g) {
		t.Fatal("stage")
	}
	for _, wanted := range []unsafe.Pointer{unsafe.Pointer(ownedChild), unsafe.Pointer(ownedParent)} {
		raw, size, ok := TakeDiscardedPanicTraceFrame(g)
		if !ok || raw != wanted || size != total {
			t.Fatal("owned metadata release")
		}
	}
	if raw, _, ok := TakeDiscardedPanicTraceFrame(g); !ok || raw != nil {
		t.Fatal("trace not empty")
	}
	runtime.KeepAlive(outer)
	runtime.KeepAlive(ownedChild)
	runtime.KeepAlive(ownedParent)
}
