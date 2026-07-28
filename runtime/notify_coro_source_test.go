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
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	runtimeNotifyCommonSource = "internal/lib/runtime/sema_llgo.go"
	runtimeNotifyLegacySource = "internal/lib/runtime/notify_legacy_llgo.go"
	runtimeNotifyCoroSource   = "internal/lib/runtime/notify_coro_llgo.go"
)

func TestRuntimeNotifyListSelectsEventDrivenCoroImplementation(t *testing.T) {
	common := readRuntimePollFile(t, runtimeNotifyCommonSource)
	for _, forbidden := range []string{"psync.", "pthread", "notifyMap", "notifyState", ".Cond"} {
		if strings.Contains(common, forbidden) {
			t.Errorf("%s retains capability-specific notify implementation %q", runtimeNotifyCommonSource, forbidden)
		}
	}
	for _, marker := range []string{"type notifyList struct", "notifyListTicketLess", "sync_runtime_notifyListAdd", "sync_runtime_notifyListCheck"} {
		if !strings.Contains(common, marker) {
			t.Errorf("%s lacks common notify ABI marker %q", runtimeNotifyCommonSource, marker)
		}
	}

	coroSource := readRuntimePollFile(t, runtimeNotifyCoroSource)
	requireRuntimeAnnotationFreeCDeclarations(
		t,
		runtimeNotifyCoroSource,
		"llgoCoroNotifyPrepareOrAbortV2",
		"llgoCoroNotifyOneOrAbortV2",
		"llgoCoroNotifyAllOrAbortV2",
	)
	for _, marker := range []string{
		"C.__llgo_coro_notify_prepare_or_abort_v2",
		"C.__llgo_coro_notify_one_or_abort_v2",
		"C.__llgo_coro_notify_all_or_abort_v2",
		"//go:linkname llgoCoroNotifySuspendV2 llgo.coroPark",
		"notifyListTicketLess(target, latomic.LoadUint32(&l.notify))",
		"unsafe.Pointer(&l.notify)",
		"llgoCoroNotifyPrepareOrAbortV2(unsafe.Pointer(&state), unsafe.Pointer(&l.notify), target)",
		"llgoCoroNotifySuspendV2(&state, 0)",
		"storage [256 - unsafe.Sizeof(uintptr(0))]byte",
	} {
		if !strings.Contains(coroSource, marker) {
			t.Errorf("%s lacks event-driven notify marker %q", runtimeNotifyCoroSource, marker)
		}
	}
	for _, forbidden := range []string{"psync.", "pthread", "notifyMap", "notifyState", ".Cond", "runtime.Gosched"} {
		if strings.Contains(coroSource, forbidden) {
			t.Errorf("%s retains blocking notify path %q", runtimeNotifyCoroSource, forbidden)
		}
	}
	assertRuntimeNotifyExactRetentionSpan(t, runtimeNotifyCoroSource, coroSource)

	native := []string{"llgo", "llgo_coro", "llgo_coro_native_pipe", "llgo_coro_native_timer"}
	tests := []struct {
		name      string
		goos      string
		buildTags []string
		legacy    bool
		coro      bool
	}{
		{name: "ordinary linux", goos: "linux", legacy: true},
		{name: "partial profile", goos: "linux", buildTags: []string{"llgo", "llgo_coro"}, legacy: true},
		{name: "complete linux", goos: "linux", buildTags: native, coro: true},
		{name: "complete darwin", goos: "darwin", buildTags: native, coro: true},
		{name: "adapter selects legacy", goos: "linux", buildTags: append(slices.Clone(native), "coro_runtime_adapter_test"), legacy: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = "amd64"
			ctx.BuildTags = slices.Clone(test.buildTags)
			for file, want := range map[string]bool{
				filepath.Base(runtimeNotifyLegacySource): test.legacy,
				filepath.Base(runtimeNotifyCoroSource):   test.coro,
			} {
				got, err := ctx.MatchFile(filepath.Dir(runtimeNotifyCoroSource), file)
				if err != nil {
					t.Fatalf("MatchFile(%q): %v", file, err)
				}
				if got != want {
					t.Errorf("MatchFile(%q) = %t, want %t", file, got, want)
				}
			}
		})
	}
}

func assertRuntimeNotifyExactRetentionSpan(t *testing.T, path, source string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "sync_runtime_notifyListWait" {
			function = candidate
			break
		}
	}
	if function == nil || function.Body == nil {
		t.Fatalf("%s lacks notifyListWait", path)
	}
	prepare, park := -1, -1
	for index, statement := range function.Body.List {
		ast.Inspect(statement, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			identifier, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			switch identifier.Name {
			case "llgoCoroNotifyPrepareOrAbortV2":
				prepare = index
			case "llgoCoroNotifySuspendV2":
				park = index
			}
			return true
		})
	}
	if prepare < 0 || park != prepare+1 {
		t.Fatalf("notify prepare/park are not an exact adjacent span: %d/%d", prepare, park)
	}
}

func TestCoroNotifyOwnerV2FailStopABIAndKeyedTransactions(t *testing.T) {
	const ownerPath = "internal/runtime/coro_notify_owner_llgo.go"
	source := readRuntimePollFile(t, ownerPath)
	file, err := parser.ParseFile(token.NewFileSet(), ownerPath, source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			functions[function.Name.Name] = function
		}
	}
	tests := []struct {
		name   string
		params []string
	}{
		{name: "__llgo_coro_notify_prepare_or_abort_v2", params: []string{"unsafe.Pointer", "unsafe.Pointer", "uint32"}},
		{name: "__llgo_coro_notify_one_or_abort_v2", params: []string{"unsafe.Pointer", "uint32"}},
		{name: "__llgo_coro_notify_all_or_abort_v2", params: []string{"unsafe.Pointer", "uint32"}},
	}
	for _, test := range tests {
		function := functions[test.name]
		if function == nil {
			t.Fatalf("missing notify owner %q", test.name)
		}
		if function.Type.Results != nil && len(function.Type.Results.List) != 0 {
			t.Fatalf("%s returns a result; want void owner ABI", test.name)
		}
		if got := coroTimerOwnerParameterTypes(t, function); !slices.Equal(got, test.params) {
			t.Fatalf("%s parameter types = %v, want %v", test.name, got, test.params)
		}
		if function.Doc == nil || !strings.Contains(source, "//export "+test.name) {
			t.Fatalf("%s lacks exact export annotation", test.name)
		}
	}
	for _, marker := range []string{
		"coroPrepareKeyedStateV2(",
		"coroKeyedParkNotifyV2",
		"catomic.Store(notify, current+1)",
		"coroKeyedPostOneV2(",
		"catomic.Store(notify, waitSnapshot)",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("%s lacks notify transaction marker %q", ownerPath, marker)
		}
	}
	for _, obsolete := range []string{
		"__llgo_coro_notify_prepare_or_abort_v1",
		"__llgo_coro_notify_retire_completed_or_abort_v1",
		"__llgo_coro_notify_one_or_abort_v1",
		"__llgo_coro_notify_all_or_abort_v1",
		"PrepareExecutorNotifyWait",
		"RetireCompletedExecutorNotifyWait",
	} {
		if strings.Contains(source, obsolete) {
			t.Errorf("%s retains obsolete notify path %q", ownerPath, obsolete)
		}
	}
	for _, forbidden := range []string{"pthread", "psync.", ".Cond", "notifyMap", "runtime.Gosched"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s contains blocking notify implementation %q", ownerPath, forbidden)
		}
	}
}
