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
			params:    []string{"int32", "uint32", "int64"},
			delegates: "coroProgramUpdatePollDeadlineV1",
			failStop:  true,
		},
		{
			name:      "__llgo_coro_poll_post_closing_or_abort_v1",
			params:    []string{"int32", "uint32"},
			delegates: "coroProgramPostPollClosingV1",
			failStop:  true,
		},
		{
			name: "__llgo_coro_poll_park_v2",
			params: []string{
				"unsafe.Pointer", "unsafe.Pointer", "unsafe.Pointer", "unsafe.Pointer",
				"int32", "uint32", "int64",
			},
			delegates: "coro.PrepareCurrentExecutorPollPark",
			failStop:  true,
		},
		{
			name:      "__llgo_coro_poll_resume_v2",
			params:    []string{"unsafe.Pointer", "unsafe.Pointer"},
			result:    "uint32",
			delegates: "coro.FinishCurrentExecutorPollPark",
			failStop:  true,
		},
		{
			name:      "__llgo_coro_poll_post_event_v2",
			params:    []string{"uint32", "uint32", "uint32"},
			result:    "uint32",
			delegates: "coro.PostExecutorPollEvent",
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
		"coro.UpdateExecutorPollDeadlineExact(",
		"coro.PostExecutorPollEvent(",
		"coroTargetRequestExecutorV1(",
		"coro.CurrentExecutorPollDriver(",
		"coro.PrepareCurrentExecutorPollPark(",
		"coro.FinishCurrentExecutorPollPark(",
		"It is not a foreign-thread ingress",
		"retain one lease across both source publication and executor request",
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
	} {
		if strings.Contains(ownerText, obsolete) {
			t.Errorf("%s retains obsolete Poll V1 wait ABI %q", ownerSource, obsolete)
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

	target := readRuntimePollFile(t, "internal/runtime/coro_target_native_llgo.go")
	for _, marker := range []string{
		"func CoroNativePollServerDescriptorV1(fd uintptr) bool",
		"coroNativeTargetV1State.doorbell.OwnsDescriptor(fd)",
	} {
		if !strings.Contains(target, marker) {
			t.Errorf("native target lacks exact shared-doorbell identity marker %q", marker)
		}
	}
}

func TestCoroPollDescriptorUsesOpaqueHandleAndTypedRoot(t *testing.T) {
	const sourcePath = "internal/lib/runtime/poll_linkname_coro_llgo.go"
	source := readRuntimePollFile(t, sourcePath)
	for _, marker := range []string{
		"pollDescRoots map[uintptr]*llgoPollDesc",
		"pollDescNext++",
		"func pollRootGet(ctx uintptr) *llgoPollDesc",
		"pd := pollDescRoots[ctx]",
		"The catalog remains the typed lifetime root",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("%s lacks opaque-handle root marker %q", sourcePath, marker)
		}
	}
	for _, forbidden := range []string{
		"uintptr(unsafe.Pointer(pd))",
		"(*llgoPollDesc)(unsafe.Pointer(ctx))",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s reconstructs a poll descriptor through unrooted address word %q", sourcePath, forbidden)
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
