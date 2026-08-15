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
	"strings"
	"testing"
)

func TestCoroSpawnFusesTaskAndRuntimeContextAllocation(t *testing.T) {
	layout := readRuntimePollFile(t, "internal/runtime/coro_task_allocation.go")
	for _, required := range []string{
		"type coroTaskAllocation struct",
		"task    coro.G",
		"context coroRuntimeContext",
		"coroTaskAllocationTaskOffset    = unsafe.Offsetof(coroTaskAllocation{}.task)",
		"var _ [coroTaskAllocationTaskOffset]byte = [0]byte{}",
	} {
		if !strings.Contains(layout, required) {
			t.Errorf("combined task allocation lacks layout gate %q", required)
		}
	}

	spawn := readRuntimePollFile(t, "internal/runtime/coro_spawn.go")
	for _, required := range []string{
		"coroalloc.AllocTask(allocationSize)",
		"coro.Zero(raw, allocationSize)",
		"coroBindTaskAllocationRuntimeContextCompiler(child, parent)",
		"coroalloc.FreeTask(raw, allocationSize)",
	} {
		if !strings.Contains(spawn, required) {
			t.Errorf("spawn path lacks combined allocation marker %q", required)
		}
	}
	if strings.Contains(spawn, "coroalloc.AllocTask(taskSize)") ||
		strings.Contains(spawn, "coroBindRuntimeContext(child, parent, false)") {
		t.Fatal("spawn path retained a separately allocated runtime sidecar")
	}
	allocation := strings.Index(spawn, "raw := coroalloc.AllocTask(allocationSize)")
	initialize := strings.Index(spawn, "child, _, actualSize, allocationOK := coroTaskAllocationAt(raw)")
	if allocation < 0 || initialize < 0 || allocation >= initialize ||
		strings.Contains(spawn[allocation:initialize], "coro.Zero(") {
		t.Fatal("spawn path repeats the allocator's zero-filled storage contract")
	}

	context := readRuntimePollFile(t, "internal/runtime/coro_task_context.go")
	for _, required := range []string{
		"if ctx.g.coroEmbedded {",
		"ctx == &(*coroTaskAllocation)(unsafe.Pointer(task)).context",
		"if !embedded {",
		"FreeRoot(unsafe.Pointer(ctx))",
	} {
		if !strings.Contains(context, required) {
			t.Errorf("runtime-context release lacks shared-root gate %q", required)
		}
	}
}

func TestCoroAllocatorOwnsZeroFillAndRetirementSanitization(t *testing.T) {
	allocator := readRuntimePollFile(t, "internal/coroalloc/allocator.go")
	for _, marker := range []string{
		"const backendAllocationsAreZeroed = true",
		"allocates one zero-filled, explicitly owned",
		"if !backendAllocationsAreZeroed || !Ready()",
	} {
		if marker == "const backendAllocationsAreZeroed = true" {
			continue
		}
		if !strings.Contains(allocator, marker) {
			t.Errorf("coroutine allocator lacks zero-fill contract marker %q", marker)
		}
	}
	for _, path := range []string{
		"internal/coroalloc/backend_gc.go",
		"internal/coroalloc/backend_nogc.go",
		"internal/coroalloc/backend_webassembly.go",
		"internal/coroalloc/backend_tinygogc.go",
	} {
		source := readRuntimePollFile(t, path)
		if !strings.Contains(source, "const backendAllocationsAreZeroed = true") {
			t.Errorf("%s lacks zero-filled backend contract", path)
		}
	}
	for _, path := range []string{
		"internal/coroalloc/backend_nogc.go",
		"internal/coroalloc/backend_webassembly.go",
	} {
		if source := readRuntimePollFile(t, path); !strings.Contains(source, "return c.Calloc(1, size)") ||
			strings.Contains(source, "return c.Malloc(size)") {
			t.Errorf("%s does not implement the zero-filled libc contract", path)
		}
	}
	tiny := readRuntimePollFile(t, "internal/runtime/tinygogc/rooted.go")
	if !strings.Contains(tiny, "c.Memset(ptr, 0, size)") ||
		strings.Index(tiny, "c.Memset(ptr, 0, size)") > strings.Index(tiny, "unlinkRootedAllocation(root)") {
		t.Fatal("tinygogc does not sanitize a rooted allocation before unlink")
	}
	frameCore := readRuntimePollFile(t, "internal/coro/frame.go")
	register := strings.Index(frameCore, "func RegisterFrame(")
	publish := strings.Index(frameCore, "func FrameFromStorage(")
	if register < 0 || publish < 0 || register >= publish ||
		strings.Contains(frameCore[register:publish], "Zero(raw, total)") {
		t.Fatal("frame registration repeats the allocator's zero-fill contract")
	}
	frameRuntime := readRuntimePollFile(t, "internal/runtime/coro_frame.go")
	free := strings.Index(frameRuntime, "func __llgo_coro_frame_free_v1(")
	if free < 0 || strings.Contains(frameRuntime[free:], "coro.Zero(raw, total)") {
		t.Fatal("frame release sanitizes outside the selected allocator backend")
	}
}

func TestCoroLogicalContextBorrowsPhysicalMPOnlyWhileRunning(t *testing.T) {
	contextLayout := readRuntimePollFile(t, "internal/runtime/runtime_context.go")
	for _, required := range []string{
		"type coroRuntimeContext struct",
		"type runtimeContext struct",
		"coroRuntimeContext\n\tm m\n\tp p",
		"var _ [runtimeContextCoreOffset]byte = [0]byte{}",
	} {
		if !strings.Contains(contextLayout, required) {
			t.Errorf("runtime context split lacks marker %q", required)
		}
	}

	lifecycle := readRuntimePollFile(t, "internal/runtime/coro_task_context.go")
	for _, required := range []string{
		"mp := current.m",
		"gp.m = mp",
		"mp.curg = gp",
		"mp.curg = previous",
		"gp.m = nil",
	} {
		if !strings.Contains(lifecycle, required) {
			t.Errorf("logical context lacks physical M borrow marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"gp, pp := &ctx.g, &ctx.p",
		"setpstatus(pp, _Prunning)",
		"setpstatus(pp, _Pidle)",
	} {
		if strings.Contains(lifecycle, forbidden) {
			t.Errorf("logical context retained per-G physical P path %q", forbidden)
		}
	}
}
