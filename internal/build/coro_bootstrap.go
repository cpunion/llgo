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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"go/types"
	"sort"
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroProgramBootstrapVersionV1               uint32          = 1
	coroProgramBootstrapVersionV2               uint32          = 2
	coroProgramBootstrapFactorySymbolV1                         = "__llgo_coro_program_bootstrap_factory_v1"
	coroProgramBootstrapFactorySymbolV2                         = "__llgo_coro_program_bootstrap_factory_v2"
	coroProgramBootstrapFrameDescriptorPrefixV1                 = "__llgo_coro_program_bootstrap_frame_descriptor_v1."
	coroProgramBootstrapFrameDescriptorPrefixV2                 = "__llgo_coro_program_bootstrap_frame_descriptor_v2."
	coroProgramPublicRuntimeNoopSymbolV2                        = "__llgo_coro_public_runtime_init_noop_v2"
	coroProgramPublicRuntimeNoopIDV2            coro.FunctionID = "llgo.bootstrap.v2.public-runtime-init.noop"
	coroProgramBeginSymbolV1                                    = "__llgo_coro_program_begin_v1"
	coroProgramRunSymbolV1                                      = "__llgo_coro_program_run_v1"
	coroProgramContinueSymbolV1                                 = "__llgo_coro_program_continue_v1"
	coroProgramRunSliceSymbolV2                                 = "__llgo_coro_program_run_slice_v2"
	coroProgramContinueSliceSymbolV2                            = "__llgo_coro_program_continue_slice_v2"
	coroProgramMainReturnSymbolV1                               = "__llgo_coro_program_main_return_v1"
	coroNativePostWaitSymbolV1                                  = "__llgo_coro_native_post_wait_v1"
	coroWaitPrepareSymbolV1                                     = "__llgo_coro_wait_prepare_v1"
	coroWaitRollbackSymbolV1                                    = "__llgo_coro_wait_rollback_v1"
	coroWaitRetireCompletedSymbolV1                             = "__llgo_coro_wait_retire_completed_v1"
	coroRunDecisionTakeSymbolV1                                 = "__llgo_coro_run_decision_take_v1"
	coroRunDecisionTakeZeroSymbolV1                             = "__llgo_coro_run_decision_take_zero_v1"
	coroTimerPrepareAfterSymbolV1                               = "__llgo_coro_timer_prepare_after_v1"
	coroTimerRetireCompletedSymbolV1                            = "__llgo_coro_timer_retire_completed_v1"
	coroTimerPrepareAfterOrAbortSymbolV1                        = "__llgo_coro_timer_prepare_after_or_abort_v1"
	coroTimerRetireCompletedOrAbortSymbolV1                     = "__llgo_coro_timer_retire_completed_or_abort_v1"
	coroChanSendParkSymbolV1                                    = "__llgo_coro_chan_send_park_v1"
	coroChanRecvParkSymbolV1                                    = "__llgo_coro_chan_recv_park_v1"
	coroChanResumeSymbolV1                                      = "__llgo_coro_chan_resume_v1"
	coroChanSendClosedPanicSymbolV1                             = "__llgo_coro_chan_send_closed_panic_v1"
	coroWorkerParkSymbolV1                                      = "__llgo_coro_worker_park_v1"
	coroWorkerResumeSymbolV1                                    = "__llgo_coro_worker_resume_v1"

	// Step kinds and semantic roles are part of the cross-target bootstrap ABI.
	// Keep these numeric values synchronized with ssa and runtime/internal/coro.
	coroProgramStepDirectPlainV1 uint32 = 1
	coroProgramStepCoroRootV1    uint32 = 2
	coroProgramStepRoleInitV1    uint32 = 1
	coroProgramStepRoleMainV1    uint32 = 2

	coroProgramStepRoleRuntimeInitV2       uint32 = 1
	coroProgramStepRoleABIInitV2           uint32 = 2
	coroProgramStepRolePublicRuntimeInitV2 uint32 = 4
	coroProgramStepRolePackageInitV2       uint32 = 8
	coroProgramStepRoleMainV2              uint32 = 16

	// Native pipe targets use a fixed-stack compiler loop. Each public runtime
	// call executes at most this many certified scheduler reductions before it
	// must return an exact POD continuation tuple to the entry module.
	coroProgramNativeRunBudgetV2 uint32 = 1024

	coroProgramDriveCompleteV2 uint32 = 1
	coroProgramDriveYieldedV2  uint32 = 3

	coroProgramRunMoreV2          uint32 = 1 << 0
	coroProgramRunRequestInlineV2 uint32 = 1 << 3
)

type coroProgramBootstrapStepV1 struct {
	Kind       uint32
	Role       uint32
	FunctionID coro.FunctionID
	// Target is the exact callable symbol. For CoroRoot it is the function's
	// unique physical coroutine primary and is used by the compiler-owned
	// bootstrap; CatalogTarget is the linked package anchor validated by the
	// runtime startup table.
	Target        string
	Owner         string
	CatalogTarget string
	Aux           uint64
}

type coroProgramBootstrapV1 struct {
	Version  uint32
	StepHash [16]byte
	Steps    []coroProgramBootstrapStepV1
}

func (b *coroProgramBootstrapV1) abiVersion() uint32 {
	if b == nil || b.Version == 0 {
		return coroProgramBootstrapVersionV1
	}
	return b.Version
}

func validateCoroProgramBootstrapConfig(conf *Config) error {
	if conf == nil {
		return nil
	}
	if conf.EnableCoroClosedStaticSpawn && !conf.EnableCoroProgramBootstrapRun {
		return fmt.Errorf("enable coroutine closed static spawn: runnable program bootstrap v2 is required")
	}
	if conf.EnableCoroProgramBootstrapRun && !conf.EnableCoroProgramBootstrapABI {
		return fmt.Errorf("enable coroutine program bootstrap runtime: program bootstrap ABI is required")
	}
	if conf.EnableCoroChannel && !conf.EnableCoroProgramBootstrapRun {
		return fmt.Errorf("enable coroutine channel lowering: runnable program bootstrap is required")
	}
	if conf.EnableCoroWorker && !conf.EnableCoroProgramBootstrapRun {
		return fmt.Errorf("enable coroutine worker lowering: runnable program bootstrap is required")
	}
	if !conf.EnableCoroProgramBootstrapABI {
		return nil
	}
	switch {
	case !conf.EnableCoroEntryResolution:
		return fmt.Errorf("enable coroutine program bootstrap ABI: coroutine entry resolution is required")
	case !conf.EnableCoroPhysicalABI:
		return fmt.Errorf("enable coroutine program bootstrap ABI: coroutine physical ABI is required")
	case !conf.EnableCoroChildAwait:
		return fmt.Errorf("enable coroutine program bootstrap ABI: coroutine child await is required")
	case conf.BuildMode != BuildModeExe:
		return fmt.Errorf("enable coroutine program bootstrap ABI: executable build mode is required")
	default:
		return nil
	}
}

func prepareCoroProgramBootstrapsV1(ctx *context) (map[string]*coroProgramBootstrapV1, error) {
	if ctx == nil || ctx.buildConf == nil || !ctx.buildConf.EnableCoroProgramBootstrapABI {
		return nil, nil
	}
	bootstraps := make(map[string]*coroProgramBootstrapV1)
	for _, pkg := range ctx.initial {
		if pkg == nil || !needLink(pkg, ctx.mode) {
			continue
		}
		if _, exists := bootstraps[pkg.ID]; exists {
			return nil, fmt.Errorf("duplicate linked main package ID %q", pkg.ID)
		}
		var bootstrap *coroProgramBootstrapV1
		var err error
		if ctx.buildConf.EnableCoroProgramBootstrapRun {
			bootstrap, err = selectCoroProgramBootstrapV2(ctx, pkg)
		} else {
			bootstrap, err = selectCoroProgramBootstrapV1(ctx, pkg)
		}
		if err != nil {
			return nil, fmt.Errorf("package %q: %w", pkg.ID, err)
		}
		bootstraps[pkg.ID] = bootstrap
	}
	if len(bootstraps) == 0 {
		return nil, fmt.Errorf("no linked main package is available for the executable startup table")
	}
	return bootstraps, nil
}

// selectCoroProgramBootstrapV1 constructs the semantic [Init, Main] table from
// the exact SSA package selected by the linker. The current frontend gives
// these two top-level plain functions the physical names pkg.PkgPath+".init"
// and pkg.PkgPath+".main". We verify every premise of that mapping here and do
// not scan emitted LLVM modules or guess a replacement symbol.
func selectCoroProgramBootstrapV1(ctx *context, pkg *packages.Package) (*coroProgramBootstrapV1, error) {
	if ctx == nil || ctx.buildConf == nil || !ctx.buildConf.EnableCoroProgramBootstrapABI {
		return nil, nil
	}
	if err := validateCoroProgramBootstrapConfig(ctx.buildConf); err != nil {
		return nil, err
	}
	if pkg == nil {
		return nil, fmt.Errorf("coroutine program bootstrap: missing linked main package")
	}
	if ctx.prog == nil || ctx.coroEmission == nil || ctx.coroPlan == nil {
		return nil, fmt.Errorf("coroutine program bootstrap: LLVM program, frozen emission universe, and plan are required")
	}
	aPkg := ctx.pkgs[pkg]
	if aPkg == nil {
		aPkg = ctx.pkgByID[pkg.ID]
	}
	if aPkg == nil || aPkg.Package == nil || aPkg.SSA == nil || aPkg.SSA.Pkg == nil {
		return nil, fmt.Errorf("coroutine program bootstrap: linked main package %q has no exact SSA package", pkg.ID)
	}
	if aPkg.ID != pkg.ID || aPkg.PkgPath != pkg.PkgPath {
		return nil, fmt.Errorf("coroutine program bootstrap: selected SSA package %q/%q does not match linked main package %q/%q", aPkg.ID, aPkg.PkgPath, pkg.ID, pkg.PkgPath)
	}
	if got := llssa.PathOf(aPkg.SSA.Pkg); got != pkg.PkgPath {
		return nil, fmt.Errorf("coroutine program bootstrap: selected SSA package path %q does not match linked path %q", got, pkg.PkgPath)
	}

	steps := make([]coroProgramBootstrapStepV1, 0, 2)
	for _, spec := range []struct {
		name string
		role uint32
	}{
		{name: "init", role: coroProgramStepRoleInitV1},
		{name: "main", role: coroProgramStepRoleMainV1},
	} {
		step, err := selectCoroProgramPlainStepV1(ctx, aPkg, spec.name, spec.role)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	hash, err := coroProgramBootstrapHashV1(ctx, steps)
	if err != nil {
		return nil, err
	}
	return &coroProgramBootstrapV1{StepHash: hash, Steps: steps}, nil
}

// selectCoroProgramBootstrapV2 freezes the managed five-stage startup program:
// internal runtime init, compiler ABI init, public runtime init, main-package
// init, and main. Go bodies retain exactly one primary selected by the plan;
// compiler-owned stages are bounded direct-plain calls.
func selectCoroProgramBootstrapV2(ctx *context, pkg *packages.Package) (*coroProgramBootstrapV1, error) {
	if ctx == nil || ctx.buildConf == nil || !ctx.buildConf.EnableCoroProgramBootstrapRun {
		return nil, nil
	}
	if err := validateCoroProgramBootstrapConfig(ctx.buildConf); err != nil {
		return nil, err
	}
	if pkg == nil || ctx.prog == nil || ctx.coroEmission == nil || ctx.coroPlan == nil {
		return nil, fmt.Errorf("coroutine program bootstrap v2 requires a linked main package, LLVM program, frozen emission universe, and plan")
	}
	aPkg := ctx.pkgs[pkg]
	if aPkg == nil {
		aPkg = ctx.pkgByID[pkg.ID]
	}
	if aPkg == nil || aPkg.Package == nil || aPkg.SSA == nil || aPkg.SSA.Pkg == nil ||
		aPkg.ID != pkg.ID || aPkg.PkgPath != pkg.PkgPath || llssa.PathOf(aPkg.SSA.Pkg) != pkg.PkgPath {
		return nil, fmt.Errorf("coroutine program bootstrap v2: linked main package %q has no exact SSA package", pkg.ID)
	}

	runtimeInit, err := exactCoroRuntimeABIFunction(ctx, "init")
	if err != nil {
		return nil, err
	}
	publicRuntimeInit, hasPublicRuntimeInit, err := findCoroProgramFunction(ctx, "runtime", "init", "public runtime init")
	if err != nil {
		return nil, err
	}
	mainInit := aPkg.SSA.Func("init")
	mainMain := aPkg.SSA.Func("main")
	steps := make([]coroProgramBootstrapStepV1, 0, 5)
	for _, spec := range []struct {
		fn     *ssa.Function
		target string
		owner  string
		label  string
		role   uint32
	}{
		{runtimeInit, llssa.PkgRuntime + ".init", llssa.PkgRuntime, "internal runtime init", coroProgramStepRoleRuntimeInitV2},
		{mainInit, aPkg.PkgPath + ".init", aPkg.PkgPath, "main package init", coroProgramStepRolePackageInitV2},
		{mainMain, aPkg.PkgPath + ".main", aPkg.PkgPath, "main", coroProgramStepRoleMainV2},
	} {
		step, err := selectCoroProgramManagedStepV2(ctx, spec.fn, spec.target, spec.owner, spec.label, spec.role)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	publicRuntimeStep := coroProgramBootstrapStepV1{
		Kind:       coroProgramStepDirectPlainV1,
		Role:       coroProgramStepRolePublicRuntimeInitV2,
		FunctionID: coroProgramPublicRuntimeNoopIDV2,
		Target:     coroProgramPublicRuntimeNoopSymbolV2,
	}
	if hasPublicRuntimeInit {
		publicRuntimeStep, err = selectCoroProgramManagedStepV2(
			ctx, publicRuntimeInit, "runtime.init", "runtime", "public runtime init", coroProgramStepRolePublicRuntimeInitV2,
		)
		if err != nil {
			return nil, err
		}
	}
	// Insert the compiler-owned ABI stage between the internal and public
	// runtime initializers. It always exists in the entry module; profiles with
	// no work receive a canonical no-op body. Public runtime initialization is
	// an exact managed Go body above, never an assumed plain weak stub.
	steps = append(steps[:1], append([]coroProgramBootstrapStepV1{
		{
			Kind:       coroProgramStepDirectPlainV1,
			Role:       coroProgramStepRoleABIInitV2,
			FunctionID: "llgo.bootstrap.v2.compiler-abi-init",
			Target:     "init$abitypes",
		},
		publicRuntimeStep,
	}, steps[1:]...)...)
	hash, err := coroProgramBootstrapHash(ctx, coroProgramBootstrapVersionV2, steps)
	if err != nil {
		return nil, err
	}
	return &coroProgramBootstrapV1{Version: coroProgramBootstrapVersionV2, StepHash: hash, Steps: steps}, nil
}

func exactCoroRuntimeABIFunction(ctx *context, name string) (*ssa.Function, error) {
	return exactCoroProgramFunction(ctx, llssa.PkgRuntime, name, "internal runtime ABI")
}

// exactCoroProgramFunction selects one canonical emitted top-level Go body by
// package identity. It is used for startup stages whose source may come from a
// patch package (notably the public standard-library runtime package), so the
// selection is made from the frozen emission universe rather than from an
// import/package-name guess.
func exactCoroProgramFunction(ctx *context, pkgPath, name, label string) (*ssa.Function, error) {
	fn, ok, err := findCoroProgramFunction(ctx, pkgPath, name, label)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("coroutine program bootstrap %s %q has no emitted Go body in %q", label, name, pkgPath)
	}
	return fn, nil
}

// findCoroProgramFunction is exactCoroProgramFunction with an explicit absent
// result. Absence is valid only for optional startup packages such as the
// public standard-library runtime facade; ambiguity or a selected non-Go body
// still fails closed.
func findCoroProgramFunction(ctx *context, pkgPath, name, label string) (*ssa.Function, bool, error) {
	if ctx == nil || ctx.coroSSAEmission == nil || ctx.coroEmission == nil {
		return nil, false, fmt.Errorf("coroutine program bootstrap %s %q requires a complete frozen emission universe", label, name)
	}
	var found *ssa.Function
	for _, fn := range ctx.coroSSAEmission.Functions() {
		if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil || llssa.PathOf(fn.Pkg.Pkg) != pkgPath || fn.Name() != name {
			continue
		}
		if found != nil && found != fn {
			return nil, false, fmt.Errorf("coroutine program bootstrap %s %q has multiple canonical SSA bodies in %q", label, name, pkgPath)
		}
		found = fn
	}
	if found == nil {
		return nil, false, nil
	}
	goBody, err := frozenGoEmittedBody(ctx.coroEmission, found)
	if err != nil {
		return nil, false, fmt.Errorf("classify coroutine program bootstrap %s %q: %w", label, name, err)
	}
	if !goBody {
		return nil, false, fmt.Errorf("coroutine program bootstrap %s %q selected a non-Go body in %q", label, name, pkgPath)
	}
	return found, true, nil
}

func selectCoroProgramManagedStepV2(
	ctx *context, original *ssa.Function, target, owner, label string, role uint32,
) (coroProgramBootstrapStepV1, error) {
	if original == nil {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: exact SSA function is missing", label)
	}
	fn, ok := ctx.coroEmission.Resolve(original)
	if !ok || fn == nil || fn != original {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: exact function is absent from the frozen emission universe", label)
	}
	if fn.Pkg == nil || fn.Pkg.Pkg == nil || llssa.PathOf(fn.Pkg.Pkg) != owner || fn.Parent() != nil || fn.Origin() != nil || len(fn.TypeArgs()) != 0 {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: frozen target is not the exact top-level owner function", label)
	}
	if link, exists := ctx.prog.Linkname(target); exists && link != target {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: physical symbol is redirected from %q to %q", label, target, link)
	}
	sig := fn.Signature
	if sig == nil || sig.Recv() != nil || sig.Params().Len() != 0 || sig.Results().Len() != 0 || sig.Variadic() ||
		typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: target must have the exact func() signature", label)
	}

	rootID := coro.FunctionID("")
	rootDemand := coro.NoDemand
	for _, root := range ctx.coroPlan.Roots() {
		if root.Function != fn {
			continue
		}
		if rootID != "" {
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: duplicate explicit plan roots", label)
		}
		rootID, rootDemand = root.ID, root.Demand
	}
	if rootID == "" || !rootDemand.Contains(coro.AsyncDemand) {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: target is not an explicit async-capable plan root", label)
	}
	plan, ok := ctx.coroPlan.FunctionPlan(fn)
	if !ok || plan.ID != rootID || plan.External != coro.Defined || !plan.Demand.Contains(coro.AsyncDemand) {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: exact defined async-capable function plan is missing", label)
	}

	switch plan.Emission {
	case coro.EmitPlain:
		if plan.FuncRep != coro.DirectPlain || plan.Primary != coro.PrimaryPlain || plan.Effect != coro.NoSuspend || plan.Exec.Contains(coro.NeedsPreempt) {
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: plain target %q has incompatible plan (demand=%s rep=%s primary=%s effect=%s exec=%s)",
				label, plan.ID, plan.Demand, plan.FuncRep, plan.Primary, plan.Effect, plan.Exec)
		}
		// IRQUnsafe is an entry-context restriction, not a request for another
		// physical body. The program bootstrap runs as an ordinary G on the
		// executor, never as an interrupt callback, so a bounded plain stage may
		// retain this flag. ThreadAffine remains rejected until the bootstrap G has
		// an explicit locked-M/pinned-P contract.
		const supportedPlain = coro.MayUnwind | coro.NeedsCleanupFrame | coro.IRQUnsafe
		if unsupported := plan.Exec &^ supportedPlain; unsupported != 0 {
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: plain target %q has unsupported execution constraints %s", label, plan.ID, unsupported)
		}
		return coroProgramBootstrapStepV1{
			Kind: coroProgramStepDirectPlainV1, Role: role, FunctionID: plan.ID, Target: target,
		}, nil

	case coro.EmitCoroutine:
		if rootDemand != coro.AsyncDemand || plan.Demand != coro.AsyncDemand || plan.FuncRep != coro.DirectCoro || plan.Primary != coro.PrimaryCoroutine || !plan.Effect.MaySuspend() {
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: coroutine target %q is not one async-only direct coroutine (root=%s demand=%s rep=%s primary=%s effect=%s)",
				label, plan.ID, rootDemand, plan.Demand, plan.FuncRep, plan.Primary, plan.Effect)
		}
		if unsupported := plan.Exec &^ (coro.MayUnwind | coro.NeedsPreempt | coro.IRQUnsafe); unsupported != 0 {
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: coroutine target %q has unsupported execution constraints %s", label, plan.ID, unsupported)
		}
		index, err := coroProgramRootDescriptorIndexV2(ctx.coroPlan, fn)
		if err != nil {
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: %w", label, err)
		}
		return coroProgramBootstrapStepV1{
			Kind:       coroProgramStepCoroRootV1,
			Role:       role,
			FunctionID: plan.ID,
			Target:     target + "$coro",
			Owner:      owner,
			Aux:        index,
		}, nil

	default:
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: target %q has unsupported emission %s", label, plan.ID, plan.Emission)
	}
}

func coroProgramRootDescriptorIndexV2(plan *coro.SSAPlan, target *ssa.Function) (uint64, error) {
	if plan == nil || target == nil || target.Pkg == nil {
		return 0, fmt.Errorf("coroutine root descriptor index requires an exact owned target")
	}
	type rootEntry struct {
		id coro.FunctionID
		fn *ssa.Function
	}
	var entries []rootEntry
	for _, root := range plan.Roots() {
		fnPlan, ok := plan.FunctionPlan(root.Function)
		if !ok || root.Function == nil || root.Function.Pkg != target.Pkg || fnPlan.Emission != coro.EmitCoroutine {
			continue
		}
		entries = append(entries, rootEntry{id: root.ID, fn: root.Function})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	for index, entry := range entries {
		if entry.fn == target {
			return uint64(index), nil
		}
	}
	return 0, fmt.Errorf("coroutine target is absent from its owner's explicit root descriptor order")
}

// bindCoroProgramBootstrapV2 resolves semantic coroutine owners to the exact
// cache-visible package anchors produced by cl. It returns a copy so the
// pre-codegen semantic table and hash remain immutable; the final manifest hash
// additionally covers the complete sorted anchor catalog.
func bindCoroProgramBootstrapV2(bootstrap *coroProgramBootstrapV1, linked []Package) (*coroProgramBootstrapV1, error) {
	if bootstrap == nil || bootstrap.abiVersion() != coroProgramBootstrapVersionV2 {
		return bootstrap, nil
	}
	anchors := make(map[string]string)
	for _, pkg := range linked {
		if pkg == nil || pkg.PkgPath == "" || pkg.CoroRootAnchorV1 == "" {
			continue
		}
		if !validCoroRootPackageAnchorV1(pkg.CoroRootAnchorV1) {
			return nil, fmt.Errorf("package %s has invalid coroutine root anchor %q", pkg.PkgPath, pkg.CoroRootAnchorV1)
		}
		if previous, duplicate := anchors[pkg.PkgPath]; duplicate && previous != pkg.CoroRootAnchorV1 {
			return nil, fmt.Errorf("package %s has conflicting coroutine root anchors %q and %q", pkg.PkgPath, previous, pkg.CoroRootAnchorV1)
		}
		anchors[pkg.PkgPath] = pkg.CoroRootAnchorV1
	}
	bound := *bootstrap
	bound.Steps = append([]coroProgramBootstrapStepV1(nil), bootstrap.Steps...)
	for index := range bound.Steps {
		step := &bound.Steps[index]
		switch step.Kind {
		case coroProgramStepDirectPlainV1:
			if step.Owner != "" || step.CatalogTarget != "" || step.Aux != 0 {
				return nil, fmt.Errorf("coroutine program bootstrap v2 direct step %d has catalog state", index)
			}
		case coroProgramStepCoroRootV1:
			anchor := anchors[step.Owner]
			if anchor == "" {
				return nil, fmt.Errorf("coroutine program bootstrap v2 step %d owner %q has no linked root anchor", index, step.Owner)
			}
			step.CatalogTarget = anchor
		default:
			return nil, fmt.Errorf("coroutine program bootstrap v2 step %d has invalid kind %d", index, step.Kind)
		}
	}
	return &bound, nil
}

func selectCoroProgramPlainStepV1(ctx *context, aPkg *aPackage, name string, role uint32) (coroProgramBootstrapStepV1, error) {
	want := aPkg.PkgPath + "." + name
	if name == "init" {
		if _, patched := ctx.patches[aPkg.PkgPath]; patched {
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap init: patched package %q does not use the strict legacy init symbol %q", aPkg.PkgPath, want)
		}
	}
	original := aPkg.SSA.Func(name)
	if original == nil {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: exact SSA function is missing", name)
	}
	fn, ok := ctx.coroEmission.Resolve(original)
	if !ok || fn == nil {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: function is absent from the frozen emission universe", name)
	}
	if fn != original || fn.Pkg != aPkg.SSA || fn.Parent() != nil || fn.Name() != name || fn.Origin() != nil || len(fn.TypeArgs()) != 0 {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: frozen target is not the exact top-level main-package function", name)
	}
	if fn.Pkg.Pkg == nil || llssa.PathOf(fn.Pkg.Pkg) != aPkg.PkgPath {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: frozen target belongs to another package", name)
	}
	if link, exists := ctx.prog.Linkname(want); exists && link != want {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: physical symbol is redirected from %q to %q", name, want, link)
	}

	sig := fn.Signature
	if sig == nil || sig.Recv() != nil || sig.Params().Len() != 0 || sig.Results().Len() != 0 || sig.Variadic() || typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: target must have the exact func() signature", name)
	}
	goBody, err := frozenGoEmittedBody(ctx.coroEmission, fn)
	if err != nil {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: classify frozen target: %w", name, err)
	}
	if !goBody {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: target has no owned body", name)
	}

	rootID := coro.FunctionID("")
	rootDemand := coro.NoDemand
	for _, root := range ctx.coroPlan.Roots() {
		if root.Function == fn {
			if rootID != "" {
				return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: duplicate explicit plan roots", name)
			}
			rootID, rootDemand = root.ID, root.Demand
		}
	}
	if rootID == "" {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: target is not an explicit plan root", name)
	}
	if !rootDemand.Contains(coro.AsyncDemand) {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: explicit root demand is %s, want async capability", name, rootDemand)
	}
	plan, ok := ctx.coroPlan.FunctionPlan(fn)
	if !ok || plan.ID != rootID {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: exact function plan is missing or does not match its root", name)
	}
	if !plan.Demand.Contains(coro.AsyncDemand) || plan.External != coro.Defined || plan.Emission != coro.EmitPlain || plan.FuncRep != coro.DirectPlain || plan.Primary != coro.PrimaryPlain || plan.Effect != coro.NoSuspend || plan.Exec.Contains(coro.NeedsPreempt) {
		return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: target %q is not a defined async-demand plain direct non-suspending root without preemption (demand=%s external=%s emission=%s rep=%s primary=%s effect=%s exec=%s)",
			name, plan.ID, plan.Demand, plan.External, plan.Emission, plan.FuncRep, plan.Primary, plan.Effect, plan.Exec)
	}
	// init/main execute as one bounded plain activation inside the bootstrap
	// resume episode. Legacy defer/recover therefore remains local to that
	// activation, and an unrecovered panic terminates through the existing panic
	// path without requiring a suspended-parent transport. Every other execution
	// constraint remains fail-closed until its scheduler protocol exists.
	if ctx.buildConf.EnableCoroProgramBootstrapRun {
		const supported = coro.MayUnwind | coro.NeedsCleanupFrame
		if unsupported := plan.Exec &^ supported; unsupported != 0 {
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap runtime %s: target %q has unsupported execution constraints %s (complete=%s)", name, plan.ID, unsupported, plan.Exec)
		}
	}
	return coroProgramBootstrapStepV1{
		Kind:       coroProgramStepDirectPlainV1,
		Role:       role,
		FunctionID: plan.ID,
		Target:     want,
		Aux:        0,
	}, nil
}

func typeParamLen(list *types.TypeParamList) int {
	if list == nil {
		return 0
	}
	return list.Len()
}

func coroProgramBootstrapHashV1(ctx *context, steps []coroProgramBootstrapStepV1) ([16]byte, error) {
	return coroProgramBootstrapHash(ctx, coroProgramBootstrapVersionV1, steps)
}

func coroProgramBootstrapHash(ctx *context, version uint32, steps []coroProgramBootstrapStepV1) ([16]byte, error) {
	if ctx == nil || ctx.prog == nil || ctx.buildConf == nil || ctx.coroPlan == nil {
		return [16]byte{}, fmt.Errorf("coroutine program bootstrap hash requires a complete build context and plan")
	}
	decoded, err := hex.DecodeString(ctx.coroPlanDigest)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != ctx.coroPlanDigest {
		return [16]byte{}, fmt.Errorf("coroutine program bootstrap hash requires a canonical CoroPlanDigest")
	}
	metadata, err := buildCoroPlanDigestMetadata(ctx)
	if err != nil {
		return [16]byte{}, fmt.Errorf("coroutine program bootstrap hash metadata: %w", err)
	}
	metadata.FrameRetentionABI = ctx.coroPlanMetadata.FrameRetentionABI
	target := ctx.prog.TargetSpec()
	h := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		h.Write(length[:])
		h.Write([]byte(value))
	}
	if version != coroProgramBootstrapVersionV1 && version != coroProgramBootstrapVersionV2 {
		return [16]byte{}, fmt.Errorf("coroutine program bootstrap hash has unsupported version %d", version)
	}
	write("llgo.coro.program-bootstrap.v" + strconv.FormatUint(uint64(version), 10))
	write(strconv.FormatUint(uint64(version), 10))
	write("flags=0")
	write("step={kind:u32,flags:u32,target:ptr,aux:uintptr}")
	write("bootstrap={version:u32,flags:u32,hash-lo:u64,hash-hi:u64,step-count:uintptr,steps:ptr,factory:ptr}")
	write("direct-plain=" + strconv.FormatUint(uint64(coroProgramStepDirectPlainV1), 10))
	write("coro-root=" + strconv.FormatUint(uint64(coroProgramStepCoroRootV1), 10))
	if ctx.buildConf.EnableCoroProgramBootstrapRun {
		factory := coroProgramBootstrapFactorySymbolV1
		if version == coroProgramBootstrapVersionV2 {
			factory = coroProgramBootstrapFactorySymbolV2
		}
		write("factory=compiler-static-mixed-v" + strconv.FormatUint(uint64(version), 10) + ":" + factory)
		if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
			write("driver=runtime-static-single-p-native-v2:" +
				coroProgramBeginSymbolV1 + ":" +
				coroProgramRunSliceSymbolV2 + "(g:ptr,handle:ptr,budget:u32,out:*run-result-v2)->u32:" +
				coroProgramContinueSliceSymbolV2 + "(executor-slot:u32,executor-generation:u32,epoch:u32,budget:u32,out:*run-result-v2)->u32:" +
				"budget=" + strconv.FormatUint(uint64(coroProgramNativeRunBudgetV2), 10) + ":" +
				"run-result-v2={flags:u32,used:u32,executor-slot:u32,executor-generation:u32,epoch:u32,deadline-lo:u32,deadline-hi:u32,reserved:u32}:" +
				"complete=" + strconv.FormatUint(uint64(coroProgramDriveCompleteV2), 10) + ":" +
				"yielded=" + strconv.FormatUint(uint64(coroProgramDriveYieldedV2), 10) + ":" +
				"inline-flags=" + strconv.FormatUint(uint64(coroProgramRunMoreV2|coroProgramRunRequestInlineV2), 10))
		} else {
			write("driver=runtime-static-single-p-v1:" + coroProgramBeginSymbolV1 + ":" + coroProgramRunSymbolV1 + ":" + coroProgramContinueSymbolV1 + ":continue(epoch:u32)->void")
		}
		write("resume-decision-v1=" + coroRunDecisionTakeSymbolV1 + "(g:ptr,expected-epoch:u32,expected-generation:u32,outcome:*u32,case:*u32,task-kind:*u32,operation-source-slot:*u32,operation-generation:*u32)->void")
		write("resume-decision-zero-v1=" + coroRunDecisionTakeZeroSymbolV1 + "(g:ptr)->u32")
		write("wait-owner-v1=" +
			coroWaitPrepareSymbolV1 + "(token:ptr,ticket-out:*u32,wait-slot-out:*u32,wait-generation-out:*u32,executor-slot-out:*u32,executor-generation-out:*u32)->bool;" +
			coroWaitRollbackSymbolV1 + "(token:ptr,ticket:u32,wait-slot:u32,wait-generation:u32)->bool;" +
			coroWaitRetireCompletedSymbolV1 + "(token:ptr,ticket:u32,wait-slot:u32,wait-generation:u32)->bool")
		if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
			write("native-doorbell=pipe-poll-v1:" + coroNativePostWaitSymbolV1 + ":post(wait-slot:u32,wait-generation:u32,executor-slot:u32,executor-generation:u32)->u32")
		}
		if nativeCoroTimerRuntimeABI(ctx.buildConf) {
			write("native-timer=monotonic-poll-deadline-v1:" +
				coroTimerPrepareAfterOrAbortSymbolV1 + "(token:ptr,delay-ns:i64,ticket-out:*u32,timer-slot-out:*u32,timer-generation-out:*u32)->void;" +
				coroTimerRetireCompletedOrAbortSymbolV1 + "(token:ptr,ticket:u32,timer-slot:u32,timer-generation:u32)->void")
		}
		if ctx.buildConf.EnableCoroChannel {
			write("channel-v1=" +
				coroChanSendParkSymbolV1 + "(g:ptr,handle:ptr,header:ptr,channel:ptr,elem:ptr,state:ptr,size:uintptr)->void;" +
				coroChanRecvParkSymbolV1 + "(g:ptr,handle:ptr,header:ptr,channel:ptr,elem:ptr,state:ptr,size:uintptr)->void;" +
				coroChanResumeSymbolV1 + "(g:ptr,state:ptr)->u32;" +
				coroChanSendClosedPanicSymbolV1 + "(g:ptr,handle:ptr,header:ptr)->void")
		}
		if ctx.buildConf.EnableCoroWorker {
			write("worker-v1=" +
				coroWorkerParkSymbolV1 + "(g:ptr,handle:ptr,header:ptr,state:ptr,fn:uintptr,argc:u32,a0:uintptr,a1:uintptr,a2:uintptr,a3:uintptr,a4:uintptr,a5:uintptr)->void;" +
				coroWorkerResumeSymbolV1 + "(g:ptr,state:ptr,r1:*uintptr,r2:*uintptr,errno:*uintptr)->u32")
		}
		write("header=physical-abi-v1")
	} else {
		write("factory=null")
		write("driver=descriptor-only")
	}
	write(ctx.coroPlanDigest)
	write(metadata.CoroABI)
	write(metadata.SchedulerABI)
	write(metadata.PanicABI)
	write(metadata.FuncRepABI)
	write(metadata.FrameRetentionABI)
	write(target.Triple)
	write(target.CPU)
	write(target.Features)
	write(target.TargetABI)
	write(strconv.Itoa(ctx.prog.PointerSize() * 8))
	write(metadata.Endianness)
	write(ctx.prog.DataLayout())
	write(strconv.Itoa(len(steps)))
	for _, step := range steps {
		write(strconv.FormatUint(uint64(step.Kind), 10))
		write(strconv.FormatUint(uint64(step.Role), 10))
		write(string(step.FunctionID))
		write(step.Target)
		write(step.Owner)
		write(strconv.FormatUint(step.Aux, 10))
	}
	sum := h.Sum(nil)
	var hash [16]byte
	copy(hash[:], sum[:len(hash)])
	return hash, nil
}
