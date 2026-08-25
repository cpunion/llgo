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

package coro

import (
	"testing"
	"unsafe"
)

func TestResumePanicBoundaryDistinguishesLeafLandingFromOpenAncestorReturn(t *testing.T) {
	fixture := newInlineAwaitFixtureForCompiler(t, true)
	parent := FrameFromStorage(fixture.parent.storage)
	child := FrameFromStorage(fixture.child.storage)

	if !ResumePanicBoundaryActive(fixture.g, child.handle) ||
		!ResumePanicBoundaryMayLand(fixture.g, child.handle) {
		t.Fatal("active inline child did not admit its exact signal landing")
	}
	if ResumePanicBoundaryActive(fixture.g, parent.handle) ||
		ResumePanicBoundaryMayLand(fixture.g, parent.handle) {
		t.Fatal("suspended parent incorrectly admitted a leaf signal landing")
	}
	if !ResumePanicBoundaryReturning(fixture.g, child.handle) ||
		!ResumePanicBoundaryReturning(fixture.g, parent.handle) {
		t.Fatal("live inline resume ancestry did not admit normal boundary pop")
	}
	if ResumePanicBoundaryReturning(fixture.g, unsafe.Pointer(new(byte))) {
		t.Fatal("unrelated handle admitted a normal boundary pop")
	}

	parent.completion.status = CompletionReturn
	fixture.g.pending = pendingTransition{kind: pendingComplete, from: child}
	if !ResumePanicBoundaryReturning(fixture.g, child.handle) ||
		!ResumePanicBoundaryReturning(fixture.g, parent.handle) {
		t.Fatal("terminal inline receipt prevented native boundary unwind before finish")
	}

	// A child may publish a slow transition before nested machine resumes
	// return. Landing must fail closed after publication, while every open
	// ancestor boundary must still be removable on the normal unwind.
	parent.completion.status = completionArmed
	fixture.g.pending = pendingTransition{kind: pendingYield, from: child}
	if ResumePanicBoundaryMayLand(fixture.g, child.handle) {
		t.Fatal("published transition admitted a nonlocal signal landing")
	}
	if !ResumePanicBoundaryReturning(fixture.g, child.handle) ||
		!ResumePanicBoundaryReturning(fixture.g, parent.handle) {
		t.Fatal("published transition prevented normal inline boundary unwind")
	}
	if got := FinishInlineAwait(
		fixture.g, parent.handle, child.handle, false,
	); got != InlineAwaitSuspend {
		t.Fatalf("finish slow inline unwind = %d, want suspend", got)
	}
	if fixture.p.inlineAwaitDepth != 0 || fixture.g.active != child {
		t.Fatalf("slow inline unwind = (depth %d, active %p), want zero-depth child %p",
			fixture.p.inlineAwaitDepth, fixture.g.active, child)
	}
	if !ResumePanicBoundaryReturning(fixture.g, parent.handle) {
		t.Fatal("zero-depth logical descendant prevented outer action boundary pop")
	}
	if ResumePanicBoundaryReturning(fixture.g, child.handle) {
		t.Fatal("zero-depth logical descendant retained a closed inline boundary")
	}
}
