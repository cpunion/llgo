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
	requireRuntimeAnnotationFreeCDeclarations(
		t,
		runtimeSemaphoreCoroSource,
		"llgoCoroSemaphorePrepareOrAbortV2",
		"llgoCoroSemaphoreReleaseOrAbortV2",
	)
	for _, marker := range []string{
		"C.__llgo_coro_sema_prepare_or_abort_v2",
		"C.__llgo_coro_sema_release_or_abort_v2",
		"//go:linkname llgoCoroSemaphoreSuspendV2 llgo.coroPark",
		"llgoCoroSemaphorePrepareOrAbortV2(",
		"unsafe.Pointer(addr)",
		"llgoCoroSemaphoreSuspendV2(&state, 0)",
		"latomic.CompareAndSwapUint32(addr, value, value-1)",
		"llgoCoroSemaphoreReleaseOrAbortV2(unsafe.Pointer(addr))",
		"storage [256 - unsafe.Sizeof(uintptr(0))]byte",
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
	prepare, park := -1, -1
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
			case "llgoCoroSemaphorePrepareOrAbortV2":
				prepare = index
			case "llgoCoroSemaphoreSuspendV2":
				park = index
			}
			return true
		})
	}
	if prepare < 0 || park != prepare+1 {
		t.Fatalf("semaphore prepare/park are not an exact adjacent span: %d/%d", prepare, park)
	}
}

func TestCoroSemaphoreOwnerV2FailStopABIAndKeyedSource(t *testing.T) {
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
			name:       "__llgo_coro_sema_prepare_or_abort_v2",
			params:     []string{"unsafe.Pointer", "unsafe.Pointer"},
			delegates:  "coroPrepareKeyedStateV2",
			exportLine: "//export __llgo_coro_sema_prepare_or_abort_v2",
		},
		{
			name:       "__llgo_coro_sema_release_or_abort_v2",
			params:     []string{"unsafe.Pointer"},
			delegates:  "coroKeyedPostOneV2",
			exportLine: "//export __llgo_coro_sema_release_or_abort_v2",
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
				!strings.Contains(body, "coroKeyedAbortV2(") {
				t.Fatalf("%s is not an exact fail-stop owner adapter:\n%s", test.name, body)
			}
		})
	}
	for _, marker := range []string{
		"coroPrepareKeyedStateV2(",
		"coroKeyedParkSemaphoreV2",
		"coroKeyedPostOneV2(",
		"NoWaiter is ordinary",
		"//export __llgo_coro_sema_release_or_abort_v2",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("%s lacks owner/common-source marker %q", ownerPath, marker)
		}
	}
	for _, obsolete := range []string{
		"__llgo_coro_sema_prepare_or_abort_v1",
		"__llgo_coro_sema_retire_completed_or_abort_v1",
		"__llgo_coro_sema_release_or_abort_v1",
		"PrepareExecutorSemaphoreWait",
		"RetireCompletedExecutorSemaphoreWait",
	} {
		if strings.Contains(source, obsolete) {
			t.Errorf("%s retains obsolete semaphore path %q", ownerPath, obsolete)
		}
	}
	for _, forbidden := range []string{"pthread", "psync.", ".Cond", "runtime.Gosched"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s contains blocking semaphore implementation %q", ownerPath, forbidden)
		}
	}
}

func TestCoroKeyedResumeIsPNeutralAndRegistryIsPreemptibleLockFree(t *testing.T) {
	const (
		parkPath        = "internal/runtime/coro_keyed_park.go"
		manualPath      = "internal/coro/manual_park_owner.go"
		materializePath = "internal/runtime/coro_resume_materialize.go"
	)
	park := readRuntimePollFile(t, parkPath)
	manual := readRuntimePollFile(t, manualPath)
	materialize := readRuntimePollFile(t, materializePath)
	for _, marker := range []string{
		"coro.PrepareCurrentExecutorManualCleanupParkReserved(",
		"coro.BeginOwnerLocalManualCompletionCurrent(",
		"coro.FinishOwnerLocalManualCompletionCurrent(",
		"coro.TakeResumePacket(",
		"validMaterializedCoroKeyedParkV2(state)",
	} {
		if !strings.Contains(park, marker) {
			t.Errorf("%s lacks keyed P-neutral marker %q", parkPath, marker)
		}
	}
	for _, marker := range []string{
		"func PrepareCurrentExecutorManualCleanupParkReserved(",
		"installWaitSetResumeCleanup(",
		"Kind:         ResumeCleanupKeyedPark",
		"Context:      context",
		"Entries:      unsafe.Pointer(entry)",
		"RuntimeCount: 1",
	} {
		if !strings.Contains(manual, marker) {
			t.Errorf("%s lacks fused keyed cleanup marker %q", manualPath, marker)
		}
	}
	if strings.Contains(park, "coro.BindWaitSetResumeCleanup(") {
		t.Errorf("%s repeats the fused keyed cleanup binding", parkPath)
	}
	resumeStart := strings.Index(park, "func __llgo_coro_keyed_resume_v2(")
	if resumeStart < 0 {
		t.Fatalf("%s lacks keyed resume", parkPath)
	}
	for _, marker := range []string{
		"coro.TakeRunDecision(",
		"coro.CurrentExecutorManualDriver(",
		"coro.FinishCurrentExecutorManualPark(",
		"coroProgramKeyedRegistryV2State.retire(",
	} {
		if strings.Contains(park[resumeStart:], marker) {
			t.Errorf("%s keyed resume revisits old owner via %q", parkPath, marker)
		}
	}
	for _, marker := range []string{
		"func coroMaterializeResumeCleanupStepV1(step coro.ResumeCleanupStep) bool {",
		"return coroMaterializeChannelResumeCleanupStepV1(step)",
		"return coroMaterializePrivateResumeCleanupStepV1(step)",
		"case coro.ResumeCleanupKeyedPark:",
		"coroProgramKeyedRegistryV2State.retire(state.registry, state.operation)",
		"state.magic = coroKeyedParkMaterializedMagicV2",
		"return coro.CommitResumeCleanupStep(step, coro.ResumeSmallInvalid)",
		"coroKeyedRegistryControlStateV2(control)",
		"coroKeyedAtomicCompareAndSwapUint32(&slot.control, control, free)",
	} {
		if !strings.Contains(materialize, marker) {
			t.Errorf("%s lacks keyed old-owner materialization marker %q", materializePath, marker)
		}
	}
	retireStart := strings.Index(materialize, "func (registry *coroKeyedRegistryV2) retire(")
	if retireStart < 0 {
		t.Fatalf("%s lacks keyed retire function", materializePath)
	}
	retireEnd := strings.Index(materialize[retireStart:], "var coroHostOperationAdapterV1State")
	if retireEnd < 0 {
		t.Fatalf("%s lacks keyed retire span", materializePath)
	}
	retire := materialize[retireStart : retireStart+retireEnd]
	for _, marker := range []string{
		"for {",
		"coroKeyedAtomicLoadUint32(&slot.control)",
		"coroKeyedAtomicCompareAndSwapUint32(&slot.control, control, free)",
	} {
		if !strings.Contains(retire, marker) {
			t.Errorf("%s keyed registry retire lacks lock-free retry marker %q", materializePath, marker)
		}
	}
	for _, forbidden := range []string{"registry.mutex", "channelMutex", ".Lock()", ".Unlock()"} {
		if strings.Contains(park, forbidden) || strings.Contains(retire, forbidden) {
			t.Errorf("keyed registry retains non-preemptible owner gate %q", forbidden)
		}
	}
}
