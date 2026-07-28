//go:build !llgo

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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestCoroPollOwnerFailStopABIAndCatalogSource(t *testing.T) {
	const ownerSource = "internal/runtime/coro_poll_owner_llgo.go"
	data, err := os.ReadFile(ownerSource)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), ownerSource, data, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			functions[function.Name.Name] = function
		}
	}
	v2Abort := functions["coroPollAbortV2"]
	if v2Abort == nil {
		t.Fatal("missing Poll V2 fail-stop helper")
	}
	v2AbortBody := coroTimerOwnerNodeText(t, v2Abort.Body)
	if !strings.Contains(v2AbortBody, "coroRuntimeAbort(") || !pollOwnerContainsTerminalLoop(v2Abort.Body) {
		t.Fatalf("Poll V2 abort helper is not terminal:\n%s", v2AbortBody)
	}

	tests := []struct {
		name      string
		params    []string
		result    string
		delegates string
		failStop  bool
	}{
		{
			name:      "__llgo_coro_poll_update_deadline_or_abort_v1",
			params:    []string{"uintptr", "uint32", "int64"},
			delegates: "coroProgramUpdatePollDeadlineV1",
			failStop:  true,
		},
		{
			name:      "__llgo_coro_poll_post_closing_or_abort_v1",
			params:    []string{"uintptr", "uint32"},
			delegates: "coroProgramPostPollClosingV1",
			failStop:  true,
		},
		{
			name: "__llgo_coro_poll_park_v2",
			params: []string{
				"unsafe.Pointer", "unsafe.Pointer", "unsafe.Pointer", "unsafe.Pointer",
				"uintptr", "int32", "uint32", "int64",
			},
			delegates: "coro.PrepareCurrentExecutorPollPark",
			failStop:  true,
		},
		{
			name:      "__llgo_coro_poll_resume_v2",
			params:    []string{"unsafe.Pointer", "unsafe.Pointer"},
			result:    "uint32",
			delegates: "coro.TakeResumePacket",
			failStop:  true,
		},
		{
			name:      "__llgo_coro_poll_post_event_v2",
			params:    []string{"uint32", "uint32", "uint32"},
			result:    "uint32",
			delegates: "coroProgramPostPollEventV2",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			function := functions[test.name]
			if function == nil {
				t.Fatalf("missing poll owner %q", test.name)
			}
			if got := coroTimerOwnerParameterTypes(t, function); !slices.Equal(got, test.params) {
				t.Fatalf("%s parameter types = %v, want %v", test.name, got, test.params)
			}
			gotResult := ""
			if function.Type.Results != nil && len(function.Type.Results.List) != 0 {
				if len(function.Type.Results.List) != 1 {
					t.Fatalf("%s result count = %d", test.name, len(function.Type.Results.List))
				}
				gotResult = coroTimerOwnerNodeText(t, function.Type.Results.List[0].Type)
			}
			if gotResult != test.result {
				t.Fatalf("%s result = %q, want %q", test.name, gotResult, test.result)
			}
			doc := ""
			if function.Doc != nil {
				for _, comment := range function.Doc.List {
					doc += comment.Text + "\n"
				}
			}
			if !strings.Contains(doc, "//export "+test.name) {
				t.Fatalf("%s lacks exact C export", test.name)
			}
			body := coroTimerOwnerNodeText(t, function.Body)
			if !strings.Contains(body, test.delegates+"(") {
				t.Fatalf("%s does not delegate to %s:\n%s", test.name, test.delegates, body)
			}
			directFailStop := strings.Contains(body, "coroRuntimeAbort(") && pollOwnerContainsTerminalLoop(function.Body)
			delegatedFailStop := strings.Contains(body, "coroPollAbortV2(")
			if test.failStop && !directFailStop && !delegatedFailStop {
				t.Fatalf("%s is not a fail-stop owner ABI:\n%s", test.name, body)
			}
		})
	}

	ownerText := string(data)
	for _, marker := range []string{
		"coroTargetPostPollOperationV2(",
		"coro.CurrentExecutorPollDriver(",
		"coro.PrepareCurrentExecutorPollPark(",
		"coro.BindSingleWaitSetResumePacket(",
		"coro.TakeResumePacket(",
		"packet    coro.ResumePacket",
		"coroPollDescPublishOperationV1(",
		"coroPollDescClearOperationV1(",
		"coroPollDescLoadOperationV1(",
		"coroPollDescDeadlineV1(",
		"route is the complete destination identity",
		"//export __llgo_coro_poll_park_v2",
		"//export __llgo_coro_poll_resume_v2",
		"//export __llgo_coro_poll_post_event_v2",
		"//export __llgo_coro_poll_update_deadline_or_abort_v1",
		"//export __llgo_coro_poll_post_closing_or_abort_v1",
	} {
		if !strings.Contains(ownerText, marker) {
			t.Errorf("%s lacks owner marker %q", ownerSource, marker)
		}
	}
	for _, obsolete := range []string{
		"__llgo_coro_poll_prepare_v1",
		"__llgo_coro_poll_retire_completed_v1",
		"__llgo_coro_poll_prepare_or_abort_v1",
		"__llgo_coro_poll_retire_completed_or_abort_v1",
		"coro.PrepareExecutorPollOperation(",
		"coro.RetireCompletedExecutorPollOperation(",
		"coroProgramFindActivePollSnapshotV2(",
		"coro.SnapshotExecutorPollOperation(",
		"coro.UpdateExecutorPollDeadlineExact(",
		"coro.TakeRunDecision(",
		"coro.FinishCurrentExecutorPollPark(",
	} {
		if strings.Contains(ownerText, obsolete) {
			t.Errorf("%s retains obsolete Poll V1 wait ABI %q", ownerSource, obsolete)
		}
	}
	for path, markers := range map[string][]string{
		"internal/runtime/coro_poll_route_native_fleet_llgo.go": {
			"coroNativeFleetPostPollV1(",
			"retains its route lease across exact source publication",
		},
	} {
		routeSource := readRuntimePollFile(t, path)
		for _, marker := range markers {
			if !strings.Contains(routeSource, marker) {
				t.Errorf("%s lacks exact poll route marker %q", path, marker)
			}
		}
	}

	executor := readRuntimePollFile(t, "internal/runtime/coro_executor.go")
	driver := readRuntimePollFile(t, "internal/runtime/coro_executor_driver_timer_llgo.go")
	normalizedExecutor := strings.Join(strings.Fields(executor), " ")
	normalizedDriver := strings.Join(strings.Fields(driver), " ")
	if !strings.Contains(normalizedExecutor, "coroProgramPollSourceV1State coro.PollOperationSource") ||
		strings.Count(executor, "!coroProgramPollSourceV1State.CanRelease()") != 2 ||
		!strings.Contains(normalizedDriver, "Poll: &coroProgramPollSourceV1State") {
		t.Fatal("global poll source is not covered by bind and release invariants")
	}

	target := readRuntimePollFile(t, "internal/runtime/coro_target_native_fleet_llgo.go")
	for _, marker := range []string{
		"func CoroNativePollServerDescriptorV1(fd uintptr) bool",
		"coroNativeFleetV1State.domains[index].doorbell.OwnsDescriptor(fd)",
	} {
		if !strings.Contains(target, marker) {
			t.Errorf("native target lacks exact shared-doorbell identity marker %q", marker)
		}
	}
}

func TestCoroPollDescriptorUsesOpaqueScalarOwner(t *testing.T) {
	const sourcePath = "internal/lib/runtime/poll_linkname_coro_llgo.go"
	source := readRuntimePollFile(t, sourcePath)
	requireRuntimeAnnotationFreeCDeclarations(
		t, sourcePath, "llgoCoroPollDescAllocV1", "llgoCoroPollDescFreeV1",
	)
	for _, marker := range []string{
		"//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal",
		"//llgo:managedlink\n//go:linkname poll_runtime_pollOpen internal/poll.runtime_pollOpen",
		"//llgo:managedlink\n//go:linkname poll_runtime_pollReadAttempt internal/poll.runtime_pollReadAttempt",
		"//llgo:managedlink\n//go:linkname poll_runtime_pollWriteAttempt internal/poll.runtime_pollWriteAttempt",
		"//llgo:managedlink\n//go:linkname poll_runtime_pollWait internal/poll.runtime_pollWait",
		"//go:linkname llgoCoroPollDescAllocV1 C.__llgo_runtime_poll_desc_alloc_v1",
		"//go:linkname llgoCoroPollDescFreeV1 C.__llgo_runtime_poll_desc_free_v1",
		"//llgo:coro noblock\n//go:linkname llgoCoroPollDescStateV1 C.__llgo_runtime_poll_desc_state_v1",
		"//llgo:coro noblock\n//go:linkname llgoCoroPollDescDeadlineV1 C.__llgo_runtime_poll_desc_deadline_v1",
		"//llgo:coro noblock\n//go:linkname llgoCoroPollDescSetDeadlineV1 C.__llgo_runtime_poll_desc_set_deadline_v1",
		"//llgo:coro noblock\n//go:linkname llgoCoroPollDescMarkClosingV1 C.__llgo_runtime_poll_desc_mark_closing_v1",
		"one opaque uintptr handle",
		"FD reference count delays Free",
		"ordinary foreign",
		"llgoCoroPollWaitV2(ctx uintptr, fd int32, interest uint32, deadline int64)",
		"llgoCoroPollUpdateDeadlineOrAbortV1(ctx uintptr, interest uint32, deadline int64)",
		"llgoCoroPollPostClosingOrAbortV1(ctx uintptr, interest uint32)",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("%s lacks explicit scalar context marker %q", sourcePath, marker)
		}
	}
	for _, forbidden := range []string{
		"pollDescRoots map[",
		"make(map[uintptr]*llgoPollDesc)",
		"delete(pollDescRoots",
		"unsafe.Pointer(ctx)",
		"uintptr(unsafe.Pointer",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s retains a Go pointer/catalog path %q", sourcePath, forbidden)
		}
	}

	const ownerPath = "internal/lib/runtime/_wrap/poll.c"
	owner, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	ownerSource := string(owner)
	for _, marker := range []string{
		"struct llgo_runtime_poll_desc_v1",
		"_Atomic uint32_t closing",
		"_Atomic int64_t read_deadline",
		"_Atomic uint64_t read_operation",
		"_Atomic uint64_t write_operation",
		"uintptr_t __llgo_runtime_poll_desc_alloc_v1(",
		"void __llgo_runtime_poll_desc_free_v1(uintptr_t context)",
		"__llgo_runtime_poll_desc_load_operation_v1(",
		"__llgo_runtime_poll_desc_publish_operation_v1(",
		"__llgo_runtime_poll_desc_clear_operation_v1(",
		"atomic_exchange_explicit(",
		"memory_order_seq_cst",
		"have no fixed table capacity",
	} {
		if !strings.Contains(ownerSource, marker) {
			t.Errorf("%s lacks opaque descriptor owner marker %q", ownerPath, marker)
		}
	}

	const adapterPath = "internal/runtime/coro_poll_descriptor_llgo.go"
	adapter := readRuntimePollFile(t, adapterPath)
	for _, marker := range []string{
		"C.__llgo_runtime_poll_desc_publish_operation_v1",
		"C.__llgo_runtime_poll_desc_clear_operation_v1",
		"C.__llgo_runtime_poll_desc_load_operation_v1",
		"func coroPollDescPublishOperationV1(",
		"func coroPollDescClearOperationV1(",
		"func coroPollDescLoadOperationV1(",
		"func coroPollDescDeadlineV1(",
	} {
		if !strings.Contains(adapter, marker) {
			t.Errorf("%s lacks exact descriptor operation adapter %q", adapterPath, marker)
		}
	}
}

func TestCoroHostPollDescriptorReservationIsPreemptionSafe(t *testing.T) {
	const sourcePath = "internal/lib/runtime/poll_host_operation_llgo.go"
	source := readRuntimePollFile(t, sourcePath)
	for _, marker := range []string{
		"active  uint32",
		"catomic.Load(&desc.active)",
		"catomic.CompareAndExchange(&desc.active, 0, 1)",
		"catomic.Store(&desc.active, 0)",
		"The scan is preemptible.",
		"publish an identical runtimeCtx",
		"func poll_runtime_pollDeadlineEpoch(ctx uintptr, mode int) (int64, uintptr)",
		"llrt.CoroHostOperationControlEpochV1(ctx, lane)",
		"Snapshot the control epoch before the deadline.",
		"scalar deadline first and advances the epoch second",
		"forcedMismatch := epoch + 1",
		"internal/poll holds its fd read/write reference",
		"throw(\"runtime: close host coroutine polldesc without completed unblock\")",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("%s lacks preemption-safe reservation marker %q", sourcePath, marker)
		}
	}
	for _, forbidden := range []string{
		"active  bool",
		"*desc = coroHostPollDescV1",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s retains non-atomic descriptor reservation %q", sourcePath, forbidden)
		}
	}

	openStart := strings.Index(source, "func poll_runtime_pollOpen(")
	closeStart := strings.Index(source, "func poll_runtime_pollClose(")
	if openStart < 0 || closeStart <= openStart {
		t.Fatalf("%s lacks ordered open/close owners", sourcePath)
	}
	open := source[openStart:closeStart]
	reserve := strings.Index(open, "catomic.CompareAndExchange(&desc.active, 0, 1)")
	retirementGate := strings.Index(open, "llrt.CoroHostOperationControlIdleV1(ctx)")
	if reserve < 0 || retirementGate < 0 || reserve >= retirementGate {
		t.Fatalf("%s does not reserve before checking the cross-table retirement gate", sourcePath)
	}

	closeEnd := strings.Index(source[closeStart:], "//go:linkname poll_runtime_pollWait ")
	if closeEnd < 0 {
		t.Fatalf("%s lacks poll close boundary", sourcePath)
	}
	closeBody := source[closeStart : closeStart+closeEnd]
	release := strings.LastIndex(closeBody, "catomic.Store(&desc.active, 0)")
	if release < 0 {
		t.Fatalf("%s poll close does not release its reservation", sourcePath)
	}
	for _, clear := range []string{
		"desc.fd = 0",
		"desc.closing = false",
		"desc.read = 0",
		"desc.write = 0",
	} {
		index := strings.Index(closeBody, clear)
		if index < 0 || index >= release {
			t.Errorf("%s poll close does not clear %q before publishing inactivity", sourcePath, clear)
		}
	}

	controlPath := "internal/runtime/coro_host_operation_control_llgo.go"
	control := readRuntimePollFile(t, controlPath)
	materializePath := "internal/runtime/coro_resume_materialize.go"
	controlMaterialization := readRuntimePollFile(t, materializePath)
	controlContract := controlMaterialization + "\n" + control
	for _, marker := range []string{
		"type coroHostOperationControlLaneV1 struct",
		"operation coro.OperationID",
		"epoch     uint32",
		"func CoroHostOperationControlEpochV1(",
		"coroHostOperationControlAdvanceEpochV1(cell)",
		"catomic.CompareAndExchange(&cell.epoch, current, next)",
		"If no operation is bound yet, its later park hook detects the epoch",
	} {
		if !strings.Contains(controlContract, marker) {
			t.Errorf("%s and %s lack reconfiguration epoch marker %q",
				materializePath, controlPath, marker)
		}
	}

	deadlineOwnerPath := "internal/runtime/coro_host_operation_deadline_owner_llgo.go"
	deadlineOwner := readRuntimePollFile(t, deadlineOwnerPath)
	for _, marker := range []string{
		"controlKey, controlLane, controlEpoch uintptr",
		"expectedEpoch := uint32(controlEpoch)",
		"currentEpoch != expectedEpoch",
		"coro.RequestCurrentExecutorWorkerParkCancel(driver, task, worker, ticket)",
		"cannot cancel reconfigured deadline coroutine host operation",
		"coro.BindWaitSetResumeCleanup(",
		"coro.TakeResumePacket(",
	} {
		if !strings.Contains(deadlineOwner, marker) {
			t.Errorf("%s lacks exact reconfiguration reconciliation %q", deadlineOwnerPath, marker)
		}
	}

	hostOwnerPath := "internal/runtime/coro_host_operation_owner_llgo.go"
	hostOwner := readRuntimePollFile(t, hostOwnerPath)
	for _, marker := range []string{
		"coro.BindWaitSetResumeCleanup(",
		"coro.TakeResumePacket(",
	} {
		if !strings.Contains(hostOwner, marker) {
			t.Errorf("%s lacks P-neutral resume marker %q", hostOwnerPath, marker)
		}
	}
	hostResumeStart := strings.Index(hostOwner, "func __llgo_coro_host_operation_resume_v1(")
	deadlineResumeStart := strings.Index(
		deadlineOwner,
		"func __llgo_coro_host_operation_deadline_resume_v1(",
	)
	if hostResumeStart < 0 || deadlineResumeStart < 0 {
		t.Fatal("host operation sources lack resume functions")
	}
	for _, source := range []struct {
		path string
		body string
	}{
		{path: hostOwnerPath, body: hostOwner[hostResumeStart:]},
		{path: deadlineOwnerPath, body: deadlineOwner[deadlineResumeStart:]},
	} {
		for _, marker := range []string{
			"coro.TakeRunDecision(",
			"coro.CurrentExecutorWorkerDriver(",
			"coro.FinishCurrentExecutorWorkerPark(",
			"coro.FinishCurrentExecutorTimerPark(",
			"coroHostOperationAdapterV1State.Retire(",
			"coroHostOperationControlUnbindV1(",
		} {
			if strings.Contains(source.body, marker) {
				t.Errorf("%s revisits old-owner state during resume via %q", source.path, marker)
			}
		}
	}
	for _, marker := range []string{
		"case coro.ResumeCleanupHostOperation:",
		"case coro.ResumeCleanupHostOperationDeadline:",
		"coroHostOperationAdapterV1State.Retire(",
		"coroHostOperationControlUnbindV1(",
		"return coro.CommitResumeCleanupStep(",
	} {
		if !strings.Contains(controlMaterialization, marker) {
			t.Errorf("%s lacks old-owner materialization marker %q", materializePath, marker)
		}
	}
}

func pollOwnerContainsTerminalLoop(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		loop, ok := node.(*ast.ForStmt)
		if ok && loop.Init == nil && loop.Cond == nil && loop.Post == nil && loop.Body != nil && len(loop.Body.List) == 0 {
			found = true
			return false
		}
		return true
	})
	return found
}
