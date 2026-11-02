package coro

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestTaskStorageRejectsCopiedTerminalOwner(t *testing.T) {
	p := new(P)
	parent := newYieldingTestG(t, "storage-identity-parent")
	if !Enqueue(p, parent.g) {
		t.Fatal("enqueue storage-identity parent")
	}
	if got, ok := NextRunnable(p); !ok || got != parent.g {
		t.Fatal("dequeue storage-identity parent")
	}
	parentAction := beginSpawnTestResume(t, p, parent)
	child := new(G)
	if !BeginSpawn(parent.g, child, unsafe.Pointer(child), TaskStorageSize()) {
		t.Fatal("begin storage-identity child")
	}
	frame, _ := newSpawnTestFrame(t, child, unsafe.Pointer(new(byte)), 0, 1)
	if !CommitSpawn(parent.g, child, frame.handle) || !RequestPreempt(parent.g) {
		t.Fatal("commit storage-identity child")
	}
	yieldSpawnTestG(t, p, parent.g, parent.frame, parentAction)
	if got, ok := NextRunnable(p); !ok || got != child {
		t.Fatal("dequeue storage-identity child")
	}
	childAction := beginSpawnTestChildResume(t, p, child, frame)
	completeSpawnTestG(t, p, child, frame, childAction)
	if !ReclaimableG(child) {
		t.Fatal("original terminal child is not reclaimable")
	}
	// A Go value copy can copy private fields without naming them. It must
	// never mint another allocation-release capability at the copy's address.
	copied := *child
	if ReclaimableG(&copied) {
		t.Fatal("copied terminal G retained allocation ownership")
	}
	if _, ok := TaskStorageOwned(&copied); ok {
		t.Fatal("copied terminal G reported valid allocation ownership")
	}
	if raw, size, ok := ReleaseTaskStorage(&copied); ok || raw != nil || size != 0 {
		t.Fatal("copied terminal G transferred storage")
	}
	if raw, size, ok := ReleaseTaskStorage(child); !ok || raw != unsafe.Pointer(child) || size != TaskStorageSize() {
		t.Fatalf("original storage release=(%p,%d,%t)", raw, size, ok)
	}
	if _, _, ok := ReleaseTaskStorage(child); ok {
		t.Fatal("original storage transferred twice")
	}
	if got, ok := NextRunnable(p); !ok || got != parent.g {
		t.Fatal("dequeue storage-identity parent after child retirement")
	}
	parentAction = beginSpawnTestResume(t, p, parent)
	completeSpawnTestG(t, p, parent.g, parent.frame, parentAction)
	if !TerminalG(p, parent.g) {
		t.Fatal("storage-identity parent did not retire")
	}
	runtime.KeepAlive(parent.frame.memory)
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(child)
}
