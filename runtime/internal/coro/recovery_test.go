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
	"runtime"
	"testing"
	"unsafe"
)

func newRecoverAwaitFixture(t *testing.T, typeWord, dataWord unsafe.Pointer) *awaitCompletionFixture {
	t.Helper()
	return newAwaitCompletionFixtureConfigured(t, nil, typeWord, dataWord)
}

func completeRecoverChild(t *testing.T, fixture *awaitCompletionFixture) {
	t.Helper()
	fixture.child.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.g, fixture.child.handle, fixture.child.header) {
		t.Fatal("publish recovery child return")
	}
	fixture.destroyChildAndResumeParent(t)
}

func TestRecoverDirectDeferredChildTakesPayloadOnce(t *testing.T) {
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture := newRecoverAwaitFixture(t, typeWord, dataWord)

	snapshot, recovered, valid := TakeRecover(fixture.g, fixture.child.handle)
	want := RecoverSnapshot{TypeWord: typeWord, DataWord: dataWord}
	if !valid || !recovered || snapshot != want {
		t.Fatalf("take direct recover = (%+v, %t, %t), want (%+v, true, true)", snapshot, recovered, valid, want)
	}
	if duplicate, recovered, valid := TakeRecover(fixture.g, fixture.child.handle); !valid || recovered || duplicate != (RecoverSnapshot{}) {
		t.Fatalf("duplicate recover = (%+v, %t, %t)", duplicate, recovered, valid)
	}

	completeRecoverChild(t, fixture)
	completion, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle)
	if !ok || completion != (CompletionSnapshot{Status: CompletionReturnRecovered}) {
		t.Fatalf("consume recovered child return = (%+v, %t)", completion, ok)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	fixture.keepAlive()
}

func TestRecoverUntakenScopePublishesOrdinaryReturn(t *testing.T) {
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture := newRecoverAwaitFixture(t, typeWord, dataWord)
	completeRecoverChild(t, fixture)

	snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle)
	if !ok || snapshot != (CompletionSnapshot{Status: CompletionReturn}) {
		t.Fatalf("consume unrecovered child return = (%+v, %t)", snapshot, ok)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	fixture.keepAlive()
}

func TestRecoverOutsideArmedScopeReturnsNil(t *testing.T) {
	fixture := newAwaitCompletionFixture(t)
	if snapshot, recovered, valid := TakeRecover(fixture.g, fixture.child.handle); !valid || recovered || snapshot != (RecoverSnapshot{}) {
		t.Fatalf("ordinary child recover = (%+v, %t, %t)", snapshot, recovered, valid)
	}
	completeRecoverChild(t, fixture)
	if snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); !ok || snapshot.Status != CompletionReturn {
		t.Fatalf("consume ordinary child return = (%+v, %t)", snapshot, ok)
	}
	fixture.keepAlive()
}

func TestRecoverInRootFrameReturnsNil(t *testing.T) {
	checked := false
	fixture := newAwaitCompletionFixtureBeforeAwait(t, func(g *G, parent, _ *testFrame) {
		snapshot, recovered, valid := TakeRecover(g, parent.handle)
		if !valid || recovered || snapshot != (RecoverSnapshot{}) {
			t.Fatalf("root recover = (%+v, %t, %t)", snapshot, recovered, valid)
		}
		checked = true
	})
	if !checked {
		t.Fatal("root recover callback was not exercised")
	}
	completeRecoverChild(t, fixture)
	if snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); !ok || snapshot.Status != CompletionReturn {
		t.Fatalf("consume root-recover fixture child = (%+v, %t)", snapshot, ok)
	}
	fixture.keepAlive()
}

func TestRecoverCompletionTransactionRejectsDuplicatePrepare(t *testing.T) {
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture := newRecoverAwaitFixture(t, typeWord, dataWord)
	before := fixture.parentFrame().completion
	if PrepareAwaitCompletionRecover(fixture.g, fixture.parent.handle, fixture.child.handle, typeWord, dataWord) {
		t.Fatal("duplicate recoverable await accepted while child is active")
	}
	if fixture.parentFrame().completion != before {
		t.Fatal("duplicate recoverable await mutated the winning completion transaction")
	}
	if snapshot, recovered, valid := TakeRecover(fixture.g, fixture.parent.handle); valid || recovered || snapshot != (RecoverSnapshot{}) {
		t.Fatalf("non-active child identity accepted = (%+v, %t, %t)", snapshot, recovered, valid)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	fixture.keepAlive()
}
