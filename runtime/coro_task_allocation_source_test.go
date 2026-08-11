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
		"coroBindTaskAllocationRuntimeContext(child, parent)",
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
