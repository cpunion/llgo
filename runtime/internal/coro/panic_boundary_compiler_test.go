// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package coro

import (
	"testing"
	"unsafe"
)

func pendingBoundaryCompilerFixture(t *testing.T) (*inlineAwaitFixture, *testFrame) {
	t.Helper()
	f := newInlineAwaitFixtureForCompiler(t, true)
	leaf := newTestFrame(t, f.g, unsafe.Pointer(new(byte)), f.child.handle)
	f.child.header.SuspendReason = uint16(SuspendCall)
	f.child.header.Lifecycle = uint16(FrameSuspended)
	if got := PrepareInlineAwaitCompiler(f.g, f.child.handle, leaf.handle, nil, nil); got != InlineAwaitStarted {
		t.Fatalf("prepare nested child = %v", got)
	}
	return f, leaf
}

func TestCompilerPanicBoundaryUsesStartAndNormalReturnReceipts(t *testing.T) {
	f, leaf := pendingBoundaryCompilerFixture(t)
	if !ResumePanicBoundaryActiveCompiler(f.g, leaf.handle) || !ResumePanicBoundaryActive(f.g, leaf.handle) {
		t.Fatal("fresh nested child rejected its native push")
	}
	if ResumePanicBoundaryActiveCompiler(f.g, f.child.handle) {
		t.Fatal("suspended caller admitted a leaf boundary")
	}
	if _, _, _, _, _, ok := TakeRunDecisionWordsCompiler(f.g, 0, 0); !ok {
		t.Fatal("take child decision")
	}
	leaf.header.SuspendReason = uint16(SuspendFrameComplete)
	leaf.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareCompleteStatusCompiler(f.g, leaf.handle, leaf.header, CompletionReturn) {
		t.Fatal("publish normal return")
	}
	if !ResumePanicBoundaryReturningCompiler(f.g, leaf.handle) || !ResumePanicBoundaryReturning(f.g, leaf.handle) {
		t.Fatal("normal return rejected its native pop")
	}
	if !ResumePanicBoundaryReturningCompiler(f.g, f.child.handle) {
		t.Fatal("ancestor unwind lost its complete validator")
	}
	if ResumePanicBoundaryMayLand(f.g, leaf.handle) {
		t.Fatal("published completion admitted a nonlocal landing")
	}
}

func TestCompilerPanicBoundaryRejectsInvalidImmediateOwnership(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*inlineAwaitFixture, *testFrame)
	}{
		{"current", func(f *inlineAwaitFixture, _ *testFrame) { f.p.current = nil }},
		{"action flags", func(f *inlineAwaitFixture, _ *testFrame) { f.p.action.Flags = 1 }},
		{"not resuming", func(f *inlineAwaitFixture, _ *testFrame) { f.p.inResume = false }},
		{"parent handle", func(_ *inlineAwaitFixture, leaf *testFrame) { leaf.header.Parent = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, leaf := pendingBoundaryCompilerFixture(t)
			tc.mutate(f, leaf)
			if ResumePanicBoundaryActiveCompiler(f.g, leaf.handle) || ResumePanicBoundaryActive(f.g, leaf.handle) {
				t.Fatal("malformed immediate push ownership accepted")
			}
			if ResumePanicBoundaryReturningCompiler(f.g, leaf.handle) || ResumePanicBoundaryReturning(f.g, leaf.handle) {
				t.Fatal("malformed immediate pop ownership accepted")
			}
		})
	}
}
