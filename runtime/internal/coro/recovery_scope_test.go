/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0.
 */

package coro

import (
	"strconv"
	"testing"
	"unsafe"
)

func TestAwaitChildScopePublication(t *testing.T) {
	for _, status := range []CompletionStatus{
		CompletionNone, completionArmed, completionRecoverArmed,
		completionRecoverTaken, CompletionReturn, CompletionPanic,
	} {
		for _, inherited := range []bool{false, true} {
			parent := Frame{completion: CompletionRecord{status: status}}
			if inherited {
				parent.properties |= frameDeferredPanicScope
			}
			// Binding must preserve storage ownership, replace a stale inherited
			// fact, and not turn a legacy (empty-completion) edge into a scope.
			child := Frame{properties: frameBorrowedStorage | frameDeferredPanicScope}
			linkAwaitChild(&parent, &child)
			want := status == completionRecoverArmed || status == completionRecoverTaken ||
				status == completionArmed && inherited
			if got := child.properties&frameDeferredPanicScope != 0; got != want ||
				child.parent != &parent || !child.borrowedStorage() {
				t.Fatalf("status=%d inherited=%t: child=(parent=%p flags=%d), want scope=%t",
					status, inherited, child.parent, child.properties, want)
			}
		}
	}
}

func TestDeferredPanicScopeNegativeDoesNotInspectAncestry(t *testing.T) {
	fixture := newAwaitCompletionFixture(t)
	child := fixture.g.active
	parent := child.parent
	// The active frame is valid but the ordinary ancestry is deliberately
	// cyclic. A negative observation must return without even reading it. A
	// positive observation still requires the exact CompletionRecord proof.
	child.parent = child
	if DeferredPanicTraceActive(fixture.g) {
		t.Fatal("ordinary active frame acquired a deferred-panic scope")
	}
	child.properties |= frameDeferredPanicScope
	if DeferredPanicTraceActive(fixture.g) {
		t.Fatal("cached positive fact bypassed the completion identity proof")
	}
	child.properties &^= frameDeferredPanicScope
	child.parent = parent
	fixture.keepAlive()
}

func BenchmarkDeferredPanicTraceNegative(b *testing.B) {
	for _, depth := range []int{1, 1000, 10000} {
		b.Run(strconv.Itoa(depth), func(b *testing.B) {
			g := &G{magic: gMagic, state: GRunning}
			frames := make([]Frame, depth)
			headers := make([]HeaderV1, depth)
			handles := make([]byte, depth)
			for i := range frames {
				frames[i] = Frame{
					owner: g, handle: unsafe.Pointer(&handles[i]), header: &headers[i], state: FrameActive,
				}
				headers[i] = HeaderV1{G: unsafe.Pointer(g), Lifecycle: uint16(FrameActive)}
				if i != 0 {
					frames[i-1].completion = CompletionRecord{status: completionArmed, child: frames[i].handle}
					linkAwaitChild(&frames[i-1], &frames[i])
				}
			}
			g.active = &frames[depth-1]
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if DeferredPanicTraceActive(g) {
					b.Fatal("ordinary await chain acquired a panic scope")
				}
			}
		})
	}
}
