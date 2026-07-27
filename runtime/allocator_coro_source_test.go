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
	"os"
	"strings"
	"testing"
)

func TestCoroRuntimeAllocatorUsesPrivateSynchronousBoundary(t *testing.T) {
	allocator, err := os.ReadFile("internal/runtime/z_gc.go")
	if err != nil {
		t.Fatal(err)
	}
	allocatorText := string(allocator)
	if got := strings.Count(allocatorText, "coroRuntimeGCMalloc(size)"); got != 2 {
		t.Errorf("managed object allocator private calls = %d, want AllocU and AllocZ", got)
	}
	if strings.Contains(allocatorText, "bdwgc.Malloc(size)") {
		t.Error("managed object allocator still reaches the generic GC_malloc declaration")
	}

	boundary, err := os.ReadFile("internal/runtime/z_gc_allocator_boundary.go")
	if err != nil {
		t.Fatal(err)
	}
	boundaryText := string(boundary)
	for _, required := range []string{
		"//llgo:coro sync\n//go:linkname coroRuntimeGCMalloc C.__llgo_coro_runtime_gc_malloc_v1",
		"without parking through the LLGo coroutine scheduler",
		"retaining the current coroutine frame",
		"sync is not a lock-free or bounded-latency claim",
		"Keep ordinary bdwgc.Malloc uncertified",
	} {
		if !strings.Contains(boundaryText, required) {
			t.Errorf("private allocator boundary lacks audit marker %q", required)
		}
	}

	bdwgcSource, err := os.ReadFile("internal/clite/bdwgc/bdwgc.go")
	if err != nil {
		t.Fatal(err)
	}
	bdwgcText := string(bdwgcSource)
	if !strings.Contains(bdwgcText, `LLGoFiles   = "$(pkg-config --cflags bdw-gc): _wrap/coro_allocator.c"`) {
		t.Error("bdwgc package does not own the private allocator wrapper source")
	}
	mallocDeclaration := "//go:linkname Malloc C.GC_malloc\nfunc Malloc(size uintptr) c.Pointer"
	if !strings.Contains(bdwgcText, mallocDeclaration) {
		t.Error("ordinary bdwgc.Malloc declaration changed unexpectedly")
	}
	if strings.Contains(bdwgcText, "//llgo:coro noblock\n"+mallocDeclaration) {
		t.Error("ordinary bdwgc.Malloc was globally certified")
	}
	if strings.Contains(bdwgcText, "//llgo:coro sync\n"+mallocDeclaration) ||
		strings.Contains(bdwgcText, "//llgo:coro worker\n"+mallocDeclaration) {
		t.Error("ordinary bdwgc.Malloc acquired a coroutine capability")
	}
	for _, declaration := range []string{
		"//llgo:coro sync\n//go:linkname Init C.GC_init",
		"//llgo:coro sync\n//go:linkname MallocUncollectable C.GC_malloc_uncollectable",
		"//llgo:coro sync\n//go:linkname Free C.GC_free",
		"//llgo:coro sync\n//go:linkname AddRoots C.GC_add_roots",
		"//llgo:coro sync\n//go:linkname RemoveRoots C.GC_remove_roots",
		"//llgo:coro sync\n//go:linkname RegisterFinalizer C.GC_register_finalizer",
	} {
		if !strings.Contains(bdwgcText, declaration) {
			t.Errorf("BDWGC root boundary lacks synchronous contract %q", declaration)
		}
	}
	for _, declaration := range []string{
		"//llgo:coro noblock\n//go:linkname Init C.GC_init",
		"//llgo:coro noblock\n//go:linkname MallocUncollectable C.GC_malloc_uncollectable",
		"//llgo:coro noblock\n//go:linkname Free C.GC_free",
		"//llgo:coro noblock\n//go:linkname AddRoots C.GC_add_roots",
		"//llgo:coro noblock\n//go:linkname RemoveRoots C.GC_remove_roots",
		"//llgo:coro noblock\n//go:linkname RegisterFinalizer C.GC_register_finalizer",
		"//llgo:coro worker\n//go:linkname RegisterFinalizer C.GC_register_finalizer",
	} {
		if strings.Contains(bdwgcText, declaration) {
			t.Errorf("BDWGC root boundary has the wrong capability %q", declaration)
		}
	}
	for _, marker := range []string{
		"returns synchronously on the",
		"calling thread and does not invoke fn during this call",
		"BDWGC retains fn and",
		"cd for later finalization",
		"oldFn and oldCd are only output slots",
		"scheduling contract, not a claim",
	} {
		if !strings.Contains(bdwgcText, marker) {
			t.Errorf("BDWGC finalizer boundary lacks lifetime marker %q", marker)
		}
	}
	if strings.Contains(boundaryText, "//llgo:coro noblock\n//go:linkname coroRuntimeGCMalloc") ||
		strings.Contains(boundaryText, "//llgo:coro worker\n//go:linkname coroRuntimeGCMalloc") {
		t.Error("private allocator boundary has a stronger or scheduler-only capability")
	}

	cWrapper, err := os.ReadFile("internal/clite/bdwgc/_wrap/coro_allocator.c")
	if err != nil {
		t.Fatal(err)
	}
	cText := string(cWrapper)
	for _, required := range []string{
		"void *__llgo_coro_runtime_gc_malloc_v1(size_t size)",
		"return GC_malloc(size);",
		"does not claim that GC_malloc is lock-free",
		"it cannot retain a\n * pointer into the calling LLVM coroutine frame",
		"this call neither\n * parks through the LLGo scheduler",
	} {
		if !strings.Contains(cText, required) {
			t.Errorf("private allocator C wrapper lacks audit marker %q", required)
		}
	}
}
