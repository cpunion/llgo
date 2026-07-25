/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package coro

import (
	"testing"
	"unsafe"
)

func hostOperationIDForTest(t *testing.T, slot, generation uint32) OperationID {
	t.Helper()
	id, ok := MakeOperationID(OperationSourceWorker, slot, generation)
	if !ok {
		t.Fatal("make host operation worker ID")
	}
	return id
}

func TestHostOperationActionV1LayoutAndCompletion(t *testing.T) {
	if unsafe.Sizeof(HostOperationActionV1{}) != 96 ||
		unsafe.Alignof(HostOperationActionV1{}) != 4 ||
		unsafe.Offsetof(HostOperationActionV1{}.Args) != 24 {
		t.Fatalf("host operation action layout = size %d align %d args %d",
			unsafe.Sizeof(HostOperationActionV1{}),
			unsafe.Alignof(HostOperationActionV1{}),
			unsafe.Offsetof(HostOperationActionV1{}.Args))
	}
	adapter := new(HostOperationAdapter)
	id := hostOperationIDForTest(t, 3, 7)
	highWord := uintptr(^uint32(0))
	if unsafe.Sizeof(uintptr(0)) == 8 {
		highWord = uintptr(uint64(0x01234567)<<32 | 0x89abcdef)
	}
	if !adapter.Submit(id, 41, []uintptr{5, highWord}) {
		t.Fatal("submit host operation")
	}
	var action HostOperationActionV1
	if !adapter.NextAction(&action) ||
		action.Kind != uint32(HostOperationActionSubmitV1) ||
		action.SourceSlot != id.SourceSlot ||
		action.SourceGeneration != id.Generation ||
		action.Opcode != 41 || action.ArgCount != 2 ||
		action.Args[0] != 5 || action.Args[1] != 0 ||
		action.Args[2] != uint32(highWord) ||
		uint64(action.Args[3]) != uint64(highWord)>>32 {
		t.Fatalf("host operation action = %+v", action)
	}
	if adapter.NextAction(&action) || action != (HostOperationActionV1{}) {
		t.Fatalf("duplicate host operation action = %+v", action)
	}
	lease, ok := adapter.BeginComplete(id)
	if !ok || !lease.valid() || !adapter.CommitComplete(lease) ||
		!adapter.Retire(id) || !adapter.CanRelease() {
		t.Fatalf("host operation completion lifecycle = lease %+v ok %t active %t release %t",
			lease, ok, adapter.Active(), adapter.CanRelease())
	}
}

func TestHostOperationCancellationPreservesSubmitBeforeCancel(t *testing.T) {
	adapter := new(HostOperationAdapter)
	id := hostOperationIDForTest(t, 1, 9)
	if !adapter.Submit(id, 17, []uintptr{11}) ||
		adapter.RequestCancel(id) != HostOperationCancelRequestedV1 ||
		adapter.RequestCancel(id) != HostOperationCancelAlreadyRequestedV1 {
		t.Fatal("request host operation cancellation")
	}
	var action HostOperationActionV1
	if !adapter.NextAction(&action) ||
		action.Kind != uint32(HostOperationActionSubmitV1) ||
		action.SourceSlot != id.SourceSlot ||
		action.SourceGeneration != id.Generation ||
		action.Opcode != 17 || action.ArgCount != 1 ||
		action.Args[0] != 11 {
		t.Fatalf("host submit action = %+v", action)
	}
	if !adapter.NextAction(&action) ||
		action.Kind != uint32(HostOperationActionCancelV1) ||
		action.SourceSlot != id.SourceSlot ||
		action.SourceGeneration != id.Generation ||
		action.Opcode != 17 || action.ArgCount != 1 ||
		action.Args[0] != 11 {
		t.Fatalf("host cancel action = %+v", action)
	}
	if adapter.NextAction(&action) || action != (HostOperationActionV1{}) {
		t.Fatalf("duplicate host operation action = %+v", action)
	}
	lease, ok := adapter.BeginComplete(id)
	if !ok || !adapter.AbortComplete(lease) {
		t.Fatal("abort exact host completion lease")
	}
	lease, ok = adapter.BeginComplete(id)
	if !ok || !adapter.CommitComplete(lease) || !adapter.Retire(id) {
		t.Fatal("complete canceled host operation")
	}
	stale := hostOperationIDForTest(t, 1, 8)
	if adapter.RequestCancel(stale) != HostOperationCancelInvalidV1 {
		t.Fatal("stale generation canceled a reusable host slot")
	}
}

func TestHostOperationCompletionMayWinCancelRace(t *testing.T) {
	adapter := new(HostOperationAdapter)
	id := hostOperationIDForTest(t, HostOperationCapacityV1, 2)
	if !adapter.Submit(id, 99, nil) {
		t.Fatal("submit host operation")
	}
	var action HostOperationActionV1
	if !adapter.NextAction(&action) ||
		adapter.RequestCancel(id) != HostOperationCancelRequestedV1 {
		t.Fatal("deliver then cancel host operation")
	}
	lease, ok := adapter.BeginComplete(id)
	if !ok || adapter.RequestCancel(id) != HostOperationCancelCompletionPendingV1 ||
		!adapter.CommitComplete(lease) ||
		adapter.RequestCancel(id) != HostOperationCancelCompletionPendingV1 ||
		!adapter.Retire(id) || !adapter.CanRelease() {
		t.Fatal("completion did not win exact cancel race")
	}
}
