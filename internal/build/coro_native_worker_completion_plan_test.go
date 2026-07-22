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

package build

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestProductionNativeWorkerCompletionHasOneRawPlainEntry(t *testing.T) {
	testProductionNativeWorkerCompletionPlan(t)
}

func testProductionNativeWorkerCompletionPlan(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native coroutine worker plan requires Darwin or Linux")
	}

	verified := errors.New("native worker completion plan verified")
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroProfile = CoroProfileStackless
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		plan, err := input.Analyze(nil, coro.SSAConfig{
			DynamicResolution:    coro.DynamicCHAClosed,
			MaxPlainInstructions: -1,
		})
		if err != nil {
			return nil, err
		}

		completion, err := findUniqueCoroWorkerPlanFunction(
			input.Program, llssa.PkgRuntime, coroNativeWorkerCompleteSymbolV1,
		)
		if err != nil {
			return nil, err
		}
		if _, ok := input.requiredPlain[completion]; !ok {
			return nil, fmt.Errorf("native worker completion is outside the required plain island")
		}
		rootCount := 0
		for _, root := range input.requiredRoots {
			if root.Function != completion {
				continue
			}
			rootCount++
			if root.Demand != coro.SyncDemand {
				return nil, fmt.Errorf("native worker completion root demand = %s, want sync", root.Demand)
			}
		}
		if rootCount != 1 {
			return nil, fmt.Errorf("native worker completion root count = %d, want 1", rootCount)
		}

		got, ok := plan.FunctionPlan(completion)
		if !ok {
			return nil, fmt.Errorf("native worker completion has no function plan")
		}
		// The physical C worker calls this entry from its raw host stack. The
		// preliminary required root remains SyncDemand for liveness, then the
		// raw-ABI closure moves that exact crossing to one raw-only plain body.
		// It must never acquire an independent managed entry or a coroutine.
		if got.Demand != coro.SyncDemand || got.ManagedDemand != coro.NoDemand ||
			!got.RawPlainDemand || !got.RawPlainOnly || !got.RawPlainEntry ||
			got.DeclaredEffect != coro.NoSuspend || got.LocalEffect != coro.NoSuspend ||
			got.Exec.Contains(coro.BlockForeign) ||
			got.Exec.Contains(coro.NeedsPreempt) || got.Emission != coro.EmitRawPlain ||
			got.Primary != coro.PrimaryPlain || got.FuncRep != coro.DirectPlain ||
			!plan.HasRawPlainVariant(completion) {
			return nil, fmt.Errorf("native worker completion plan = %+v, raw variant=%t; want one raw-only no-suspend direct-plain entry",
				got, plan.HasRawPlainVariant(completion))
		}

		const workerPath = "github.com/goplus/llgo/runtime/internal/coroworker"
		for _, legacyName := range []string{"Call", "QueueWaitTake"} {
			legacy, err := findOptionalCoroWorkerPlanFunction(input.Program, workerPath, legacyName)
			if err != nil {
				return nil, err
			}
			if legacy == nil {
				continue
			}
			if _, required := input.requiredPlain[legacy]; required {
				return nil, fmt.Errorf("legacy Go worker function %s.%s remains in the required plain island", workerPath, legacyName)
			}
			for _, root := range input.requiredRoots {
				if root.Function == legacy {
					return nil, fmt.Errorf("legacy Go worker function %s.%s remains a required root with demand %s",
						workerPath, legacyName, root.Demand)
				}
			}
			if legacyPlan, present := plan.FunctionPlan(legacy); present && legacyPlan.Demand != coro.NoDemand {
				return nil, fmt.Errorf("legacy Go worker function %s.%s demand = %s, want none",
					workerPath, legacyName, legacyPlan.Demand)
			}
		}
		owner, err := findUniqueCoroWorkerPlanFunction(
			input.Program, llssa.PkgRuntime, coroNativeFleetOwnerSymbolV1,
		)
		if err != nil {
			return nil, err
		}
		if _, ok := input.requiredPlain[owner]; !ok {
			return nil, fmt.Errorf("native fleet owner is outside the required raw scheduler-stack island")
		}
		ownerRoots := 0
		for _, root := range input.requiredRoots {
			if root.Function == owner {
				ownerRoots++
				if root.Demand != coro.SyncDemand {
					return nil, fmt.Errorf("native fleet owner root demand = %s, want sync", root.Demand)
				}
			}
		}
		if ownerRoots != 1 {
			return nil, fmt.Errorf("native fleet owner root count = %d, want 1", ownerRoots)
		}
		got, present := plan.FunctionPlan(owner)
		if !present {
			return nil, fmt.Errorf("native fleet owner has no function plan")
		}
		if got.Demand != coro.SyncDemand || got.ManagedDemand != coro.NoDemand ||
			!got.RawPlainDemand || !got.RawPlainOnly || !got.RawPlainEntry ||
			got.Emission != coro.EmitRawPlain || got.Primary != coro.PrimaryPlain ||
			got.FuncRep != coro.DirectPlain || !plan.HasRawPlainVariant(owner) {
			return nil, fmt.Errorf("native fleet owner plan = %+v, raw variant=%t; want one raw-only direct-plain scheduler entry",
				got, plan.HasRawPlainVariant(owner))
		}
		return nil, verified
	}

	_, err := Do([]string{"../../cl/_testgo/print"}, conf)
	if !errors.Is(err, verified) {
		t.Fatalf("Do error = %v, want verified production native worker plan", err)
	}
}

func TestNativeWorkerCompletionRootRequiresNativeWorkerCapability(t *testing.T) {
	t.Run("host pull without a worker adapter", func(t *testing.T) {
		fixture := buildRequiredCoroRuntimeFixture(t, `
type coroProgramRunResultV2 struct { Flags, Used, ExecutorSlot, ExecutorGeneration, Epoch, DeadlineLo, DeadlineHi, Reserved uint32 }
type hostActionV1 struct { Kind, ExecutorSlot, ExecutorGeneration, Epoch, DeadlineLo, DeadlineHi, Reserved0, Reserved1 uint32 }
func __llgo_coro_program_run_slice_v2(unsafe.Pointer, unsafe.Pointer, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_program_continue_slice_v2(uint32, uint32, uint32, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_host_next_action_v1(*hostActionV1) uint32 { return 0 }
func __llgo_coro_host_profile_v1() uint32 { return 0 }
func __llgo_coro_host_next_deadline_v1(*hostActionV1) bool { return false }
func __llgo_coro_host_publish_time_v1(uint32, uint32) bool { return false }
func __llgo_coro_host_ack_cancel_v1(uint32, uint32, uint32, uint32) bool { return false }
func __llgo_coro_host_continue_slice_v1(uint32, uint32, uint32, uint32, uint32, uint32, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_native_worker_complete_v1(uint32, uint32, uintptr, uintptr, uintptr) uint32 { return 1 }
func install() {}
`)
		fixture.ctx.buildConf = &Config{
			BuildMode: BuildModeExe,
			Goos:      "wasip1",
			Goarch:    "wasm", CoroProfile: CoroProfileStackless,
		}
		assertCoroWorkerCompletionExcluded(t, fixture)
	})
}

func assertCoroWorkerCompletionExcluded(t *testing.T, fixture requiredCoroRuntimeFixture) {
	t.Helper()
	roots, requiredPlain, directPlain, closedDynamic, err := requiredCoroProgramRuntimePlan(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	completion := fixture.pkg.Func(coroNativeWorkerCompleteSymbolV1)
	if completion == nil {
		t.Fatal("worker completion fixture function is absent")
	}
	if _, required := requiredPlain[completion]; required {
		t.Fatal("native worker completion entered the required plain island without the native worker capability")
	}
	workerHooks := 0
	for _, root := range roots {
		if root.Function == completion {
			t.Fatalf("native worker completion became a root without the native worker capability: %+v", root)
		}
		if root.Function != nil && (root.Function.Name() == coroWorkerParkSymbolV1 || root.Function.Name() == coroWorkerResumeSymbolV1) {
			workerHooks++
		}
	}
	if workerHooks != 0 {
		t.Fatalf("worker-disabled hook roots = %d, want 0", workerHooks)
	}

	input := fixture.input
	input.requiredRoots = roots
	input.requiredPlain = requiredPlain
	input.requiredDirectPlain = directPlain
	input.requiredClosedDynamic = closedDynamic
	functionIDs := fixture.functionIDs
	config := coro.SSAConfig{FunctionIDs: functionIDs, MaxPlainInstructions: -1}
	plan, err := input.Analyze(nil, config)
	if err != nil {
		t.Fatal(err)
	}
	got, present := plan.FunctionPlan(completion)
	if !present {
		t.Fatal("worker completion fixture function has no plan")
	}
	if got.Demand != coro.NoDemand || got.Emission != coro.EmitNone || got.RawPlainEntry || plan.HasRawPlainVariant(completion) {
		t.Fatalf("excluded native worker completion plan = %+v, raw variant=%t; want no demand/emission/extra entry",
			got, plan.HasRawPlainVariant(completion))
	}
}

func findUniqueCoroWorkerPlanFunction(program *ssa.Program, path, name string) (*ssa.Function, error) {
	function, err := findOptionalCoroWorkerPlanFunction(program, path, name)
	if err != nil {
		return nil, err
	}
	if function == nil {
		return nil, fmt.Errorf("coroutine worker function %s.%s is absent", path, name)
	}
	return function, nil
}

func findOptionalCoroWorkerPlanFunction(program *ssa.Program, path, name string) (*ssa.Function, error) {
	if program == nil {
		return nil, fmt.Errorf("find coroutine worker function %s.%s in a nil SSA program", path, name)
	}
	var found *ssa.Function
	for _, pkg := range program.AllPackages() {
		if pkg == nil || pkg.Pkg == nil || pkg.Pkg.Path() != path {
			continue
		}
		for _, member := range pkg.Members {
			function, ok := member.(*ssa.Function)
			if !ok || function.Name() != name {
				continue
			}
			if found != nil && found != function {
				return nil, fmt.Errorf("coroutine worker function %s.%s is ambiguous", path, name)
			}
			found = function
		}
	}
	return found, nil
}
