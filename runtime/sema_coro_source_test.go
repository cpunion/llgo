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
	runtimeSemaphoreLegacySource = "internal/lib/runtime/sema_legacy_llgo.go"
	runtimeSemaphoreCoroSource   = "internal/lib/runtime/sema_coro_llgo.go"
)

func TestRuntimeSemaphoreSelectsEventDrivenCoroImplementation(t *testing.T) {
	coroSource := readRuntimePollFile(t, runtimeSemaphoreCoroSource)
	for _, marker := range []string{
		"C.__llgo_coro_sema_prepare_or_abort_v1",
		"C.__llgo_coro_sema_retire_completed_or_abort_v1",
		"C.__llgo_coro_sema_release_or_abort_v1",
		"//go:linkname llgoCoroSemaphoreParkV1 llgo.coroPark",
		"llgoCoroSemaphorePrepareOrAbortV1(",
		"unsafe.Pointer(addr)",
		"llgoCoroSemaphoreParkV1(&token, ticket)",
		"llgoCoroSemaphoreRetireCompletedOrAbortV1(",
		"latomic.CompareAndSwapUint32(addr, value, value-1)",
		"llgoCoroSemaphoreReleaseOrAbortV1(unsafe.Pointer(addr))",
	} {
		if !strings.Contains(coroSource, marker) {
			t.Errorf("%s lacks event-driven semaphore marker %q", runtimeSemaphoreCoroSource, marker)
		}
	}
	for _, forbidden := range []string{
		"legacySemaAcquire",
		"legacySemaRelease",
		"psync.",
		"pthread",
		".Cond",
		"coroSchedulerYield",
		"runtime.Gosched",
	} {
		if strings.Contains(coroSource, forbidden) {
			t.Errorf("%s retains blocking or busy-yield semaphore path %q", runtimeSemaphoreCoroSource, forbidden)
		}
	}
	assertRuntimeSemaphoreExactRetentionSpan(t, runtimeSemaphoreCoroSource, coroSource)

	native := []string{"llgo", "llgo_coro", "llgo_coro_native_pipe", "llgo_coro_native_timer"}
	tests := []struct {
		name      string
		goos      string
		buildTags []string
		legacy    bool
		coro      bool
	}{
		{name: "ordinary linux", goos: "linux", legacy: true},
		{name: "coro alone falls back", goos: "linux", buildTags: []string{"llgo", "llgo_coro"}, legacy: true},
		{name: "missing timer falls back", goos: "linux", buildTags: []string{"llgo", "llgo_coro", "llgo_coro_native_pipe"}, legacy: true},
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
				filepath.Base(runtimeSemaphoreLegacySource): test.legacy,
				filepath.Base(runtimeSemaphoreCoroSource):   test.coro,
			} {
				got, err := ctx.MatchFile(filepath.Dir(runtimeSemaphoreCoroSource), file)
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

func assertRuntimeSemaphoreExactRetentionSpan(t *testing.T, path, source string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "semaAcquire" {
			function = candidate
			break
		}
	}
	if function == nil || function.Body == nil || len(function.Body.List) != 1 {
		t.Fatalf("%s semaAcquire is not one retry loop", path)
	}
	loop, ok := function.Body.List[0].(*ast.ForStmt)
	if !ok || loop.Body == nil {
		t.Fatalf("%s semaAcquire does not retry token CAS after wake", path)
	}
	prepare, park, retire := -1, -1, -1
	for index, statement := range loop.Body.List {
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
			case "llgoCoroSemaphorePrepareOrAbortV1":
				prepare = index
			case "llgoCoroSemaphoreParkV1":
				park = index
			case "llgoCoroSemaphoreRetireCompletedOrAbortV1":
				retire = index
			}
			return true
		})
	}
	if prepare < 0 || park != prepare+1 || retire != park+1 {
		t.Fatalf("semaphore prepare/park/retire are not an exact adjacent span: %d/%d/%d", prepare, park, retire)
	}
}

func TestCoroSemaphoreOwnerFailStopABIAndCommonWaitSource(t *testing.T) {
	const ownerPath = "internal/runtime/coro_sema_owner_llgo.go"
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
		name       string
		params     []string
		delegates  string
		exportLine string
	}{
		{
			name:       "__llgo_coro_sema_prepare_or_abort_v1",
			params:     []string{"unsafe.Pointer", "unsafe.Pointer", "*uint32", "*uint32", "*uint32"},
			delegates:  "__llgo_coro_sema_prepare_v1",
			exportLine: "//export __llgo_coro_sema_prepare_or_abort_v1",
		},
		{
			name:       "__llgo_coro_sema_retire_completed_or_abort_v1",
			params:     []string{"unsafe.Pointer", "uint32", "uint32", "uint32"},
			delegates:  "__llgo_coro_sema_retire_completed_v1",
			exportLine: "//export __llgo_coro_sema_retire_completed_or_abort_v1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			function := functions[test.name]
			if function == nil {
				t.Fatalf("missing semaphore owner %q", test.name)
			}
			if function.Type.Results != nil && len(function.Type.Results.List) != 0 {
				t.Fatalf("%s returns a result; want fail-stop void ABI", test.name)
			}
			if got := coroTimerOwnerParameterTypes(t, function); !slices.Equal(got, test.params) {
				t.Fatalf("%s parameter types = %v, want %v", test.name, got, test.params)
			}
			doc := ""
			if function.Doc != nil {
				for _, comment := range function.Doc.List {
					doc += comment.Text + "\n"
				}
			}
			body := coroTimerOwnerNodeText(t, function.Body)
			if !strings.Contains(doc, test.exportLine) || !strings.Contains(body, test.delegates+"(") ||
				!strings.Contains(body, "coroRuntimeAbort(") || !pollOwnerContainsTerminalLoop(function.Body) {
				t.Fatalf("%s is not an exact fail-stop owner adapter:\n%s", test.name, body)
			}
		})
	}
	for _, marker := range []string{
		"coro.PrepareExecutorSemaphoreWait(",
		"catomic.Load((*uint32)(addr)) != 0",
		"coro.PostPreparedExecutorSemaphoreWait(",
		"coro.RetireCompletedExecutorSemaphoreWait(",
		"coro.PostExecutorSemaphoreWait(",
		"key := uintptr(addr)",
		"coroTargetRequestExecutorV1(",
		"//export __llgo_coro_sema_release_or_abort_v1",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("%s lacks owner/common-source marker %q", ownerPath, marker)
		}
	}
	for _, internal := range []string{
		"//export __llgo_coro_sema_prepare_v1",
		"//export __llgo_coro_sema_retire_completed_v1",
	} {
		if strings.Contains(source, internal) {
			t.Errorf("%s exports internal semaphore implementation %q; only fail-stop ABI adapters may cross the raw boundary", ownerPath, internal)
		}
	}
	prepareStart := strings.Index(source, "func __llgo_coro_sema_prepare_v1(")
	if prepareStart < 0 {
		t.Fatal("semaphore owner lacks prepare body")
	}
	prepareEnd := strings.Index(source[prepareStart:], "\n}\n")
	if prepareEnd < 0 {
		t.Fatal("semaphore owner lacks bounded prepare body")
	}
	prepareBody := source[prepareStart : prepareStart+prepareEnd]
	publication := strings.Index(prepareBody, "*generation = handle.Generation")
	recheck := strings.Index(prepareBody, "catomic.Load((*uint32)(addr)) != 0")
	repair := strings.Index(prepareBody, "coro.PostPreparedExecutorSemaphoreWait(")
	if publication < 0 || recheck <= publication || repair <= recheck {
		t.Fatalf("semaphore release-before-prepare repair is not publication -> counter recheck -> exact post:\n%s", prepareBody)
	}
	for _, forbidden := range []string{"pthread", "psync.", ".Cond", "runtime.Gosched"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s contains blocking semaphore implementation %q", ownerPath, forbidden)
		}
	}
}
