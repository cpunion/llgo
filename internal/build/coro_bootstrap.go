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
	"strings"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroProgramBootstrapVersionV2               uint32          = 2
	coroProgramBootstrapFactorySymbolV2                         = "__llgo_coro_program_bootstrap_factory_v2"
	coroProgramBootstrapFrameDescriptorPrefixV2                 = "__llgo_coro_program_bootstrap_frame_descriptor_v2."
	coroProgramPublicRuntimeNoopSymbolV2                        = "__llgo_coro_public_runtime_init_noop_v2"
	coroProgramPublicRuntimeNoopIDV2            coro.FunctionID = "llgo.bootstrap.v2.public-runtime-init.noop"
	coroProgramBeginSymbolV1                                    = "__llgo_coro_program_begin_v1"
	coroProgramRunSymbolV1                                      = "__llgo_coro_program_run_v1"
	coroProgramContinueSymbolV1                                 = "__llgo_coro_program_continue_v1"
	coroProgramRunSliceSymbolV2                                 = "__llgo_coro_program_run_slice_v2"
	coroProgramContinueSliceSymbolV2                            = "__llgo_coro_program_continue_slice_v2"
	coroProgramReportPanicSymbolV1                              = "__llgo_coro_program_report_panic_v1"
	coroPanicTraceReplaceSymbolV1                               = "__llgo_coro_panic_trace_replace_v1"
	coroProgramMainReturnSymbolV1                               = "__llgo_coro_program_main_return_v1"
	coroNativeWorkerCompleteSymbolV1                            = "__llgo_coro_native_worker_complete_v1"
	coroNativeFleetOwnerSymbolV2                                = "__llgo_coro_native_fleet_owner_v2"
	coroHostNextActionSymbolV1                                  = "__llgo_coro_host_next_action_v1"
	coroHostProfileSymbolV1                                     = "__llgo_coro_host_profile_v1"
	coroHostNextDeadlineSymbolV1                                = "__llgo_coro_host_next_deadline_v1"
	coroHostPublishTimeSymbolV1                                 = "__llgo_coro_host_publish_time_v1"
	coroHostPublishWallTimeSymbolV1                             = "__llgo_coro_host_publish_wall_time_v1"
	coroHostAckCancelSymbolV1                                   = "__llgo_coro_host_ack_cancel_v1"
	coroHostContinueSliceSymbolV1                               = "__llgo_coro_host_continue_slice_v1"
	coroHostNextOperationSymbolV1                               = "__llgo_coro_host_next_operation_v1"
	coroHostCompleteOperationSymbolV1                           = "__llgo_coro_host_complete_operation_v1"
	coroRunDecisionTakeSymbolV1                                 = "__llgo_coro_run_decision_take_v1"
	coroRunDecisionTakeZeroSymbolV1                             = "__llgo_coro_run_decision_take_zero_v1"
	coroTimerParkSymbolV2                                       = "__llgo_coro_timer_park_v2"
	coroTimerParkControlledSymbolV2                             = "__llgo_coro_timer_park_controlled_v2"
	coroTimerResumeSymbolV2                                     = "__llgo_coro_timer_resume_v2"
	coroTimerRequestControlledSymbolV2                          = "__llgo_coro_timer_request_controlled_v2"
	coroPollParkSymbolV2                                        = "__llgo_coro_poll_park_v2"
	coroPollResumeSymbolV2                                      = "__llgo_coro_poll_resume_v2"
	coroPollUpdateDeadlineOrAbortSymbolV1                       = "__llgo_coro_poll_update_deadline_or_abort_v1"
	coroPollPostClosingOrAbortSymbolV1                          = "__llgo_coro_poll_post_closing_or_abort_v1"
	coroKeyedParkSymbolV2                                       = "__llgo_coro_keyed_park_v2"
	coroKeyedResumeSymbolV2                                     = "__llgo_coro_keyed_resume_v2"
	coroSemaphorePrepareOrAbortSymbolV2                         = "__llgo_coro_sema_prepare_or_abort_v2"
	coroSemaphoreReleaseOrAbortSymbolV2                         = "__llgo_coro_sema_release_or_abort_v2"
	coroNotifyPrepareOrAbortSymbolV2                            = "__llgo_coro_notify_prepare_or_abort_v2"
	coroNotifyOneOrAbortSymbolV2                                = "__llgo_coro_notify_one_or_abort_v2"
	coroNotifyAllOrAbortSymbolV2                                = "__llgo_coro_notify_all_or_abort_v2"
	coroChanSendParkSymbolV1                                    = "__llgo_coro_chan_send_park_v1"
	coroChanRecvParkSymbolV1                                    = "__llgo_coro_chan_recv_park_v1"
	coroChanResumeSymbolV1                                      = "__llgo_coro_chan_resume_v1"
	coroWorkerParkSymbolV1                                      = "__llgo_coro_worker_park_v1"
	coroWorkerResumeSymbolV1                                    = "__llgo_coro_worker_resume_v1"
	coroHostOperationParkSymbolV1                               = "__llgo_coro_host_operation_park_v1"
	coroHostOperationResumeSymbolV1                             = "__llgo_coro_host_operation_resume_v1"
	coroOSThreadLockSymbolV1                                    = "__llgo_coro_os_thread_lock_v1"
	coroOSThreadUnlockSymbolV1                                  = "__llgo_coro_os_thread_unlock_v1"
	coroOSThreadLockedSymbolV1                                  = "__llgo_coro_os_thread_locked_v1"
	coroOSThreadForeignCallSymbolV1                             = "__llgo_coro_os_thread_foreign_call_v1"
	coroForeignReentryAcquireSymbolV1                           = "__llgo_coro_foreign_reentry_acquire_v1"
	coroForeignReentryRunSymbolV1                               = "__llgo_coro_foreign_reentry_run_v1"
	coroForeignReentryFailureSymbolV1                           = "__llgo_coro_foreign_reentry_failure_v1"
	coroSameMForeignCallSymbolV1                                = "__llgo_coro_same_m_foreign_call_v1"

	// Step kinds and semantic roles are part of the cross-target bootstrap ABI.
	// Keep these numeric values synchronized with ssa and runtime/internal/coro.
	coroProgramStepDirectPlainV1           uint32 = 1
	coroProgramStepCoroRootV1              uint32 = 2
	coroProgramStepRoleRuntimeInitV2       uint32 = 1
	coroProgramStepRoleABIInitV2           uint32 = 2
	coroProgramStepRolePublicRuntimeInitV2 uint32 = 4
	coroProgramStepRolePackageInitV2       uint32 = 8
	coroProgramStepRoleMainV2              uint32 = 16

	// Native pipe targets use a fixed-stack compiler loop. Each public runtime
	// call executes at most this many certified scheduler reductions before it
	// must return an exact POD continuation tuple to the entry module.
	coroProgramNativeRunBudgetV2 uint32 = 1024

	coroProgramDriveCompleteV2  uint32 = 1
	coroProgramDriveSuspendedV2 uint32 = 2
	coroProgramDriveYieldedV2   uint32 = 3
	coroProgramDrivePanicV2     uint32 = 4

	coroProgramRunMoreV2          uint32 = 1 << 0
	coroProgramRunBlockedV2       uint32 = 1 << 1
	coroProgramRunHasDeadlineV2   uint32 = 1 << 2
	coroProgramRunRequestInlineV2 uint32 = 1 << 3
	coroProgramRunRequestQueuedV2 uint32 = 1 << 4
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

func validateCoroProgramBootstrapConfig(conf *Config) error {
	if conf == nil {
		return nil
	}
	if conf.coroNativeFleetSupported() && !conf.coroWorkerSupported() {
		return fmt.Errorf("enable native coroutine fleet: bounded native worker capability is required")
	}
	if conf.coroWorkerSupported() && !nativeCoroWorkerRuntimeABI(conf) {
		return fmt.Errorf("enable coroutine worker lowering: a native Darwin/Linux pthread worker adapter is required")
	}
	if conf.coroHostOperationSupported() && !hostCoroPullRuntimeABI(conf) {
		return fmt.Errorf("enable coroutine host operation lowering: a host-pull operation adapter is required")
	}
	if conf.coroNativeFleetSupported() && !nativeCoroTimerRuntimeABI(conf) {
		return fmt.Errorf("enable native coroutine fleet: a 64-bit native Darwin/Linux timer reactor is required")
	}
	return nil
}

func prepareCoroProgramBootstrapsV1(ctx *context) (map[string]*coroProgramBootstrapV1, error) {
	if ctx == nil || ctx.buildConf == nil {
		return nil, nil
	}
	if ctx.buildConf.BuildMode != BuildModeExe {
		return map[string]*coroProgramBootstrapV1{}, nil
	}
	bootstraps := make(map[string]*coroProgramBootstrapV1)
	for _, pkg := range ctx.initial {
		if pkg == nil || !needLink(pkg, ctx.mode) {
			continue
		}
		if _, exists := bootstraps[pkg.ID]; exists {
			return nil, fmt.Errorf("duplicate linked main package ID %q", pkg.ID)
		}
		bootstrap, err := selectCoroProgramBootstrapV2(ctx, pkg)
		if err != nil {
			return nil, fmt.Errorf("package %q: %w", pkg.ID, err)
		}
		bootstraps[pkg.ID] = bootstrap
	}
	// Some compiler/runtime-island tests build executable-shaped package
	// modules without performing a final link. There is no program entry module
	// to bind in that phase, so an empty table set is valid. The actual
	// executable link still fails closed in linkPkgs when its exact main-package
	// table is absent.
	return bootstraps, nil
}

// selectCoroProgramBootstrapV2 freezes the managed startup program: internal
// runtime init, compiler ABI init, public runtime init, every dependency init
// in Go initialization order, the main-package init, and main. Go bodies retain
// exactly one primary selected by the plan; compiler-owned stages are bounded
// direct-plain calls.
func selectCoroProgramBootstrapV2(ctx *context, pkg *packages.Package) (*coroProgramBootstrapV1, error) {
	if ctx == nil || ctx.buildConf == nil {
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
		aPkg.ID != pkg.ID || aPkg.PkgPath != pkg.PkgPath || aPkg.SSA.Pkg.Path() != pkg.PkgPath {
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
	mainSymbolPrefix := aPkg.PkgPath
	if pkg.Types != nil && pkg.Types.Name() == "main" {
		mainSymbolPrefix = "main"
	}
	packageInits, err := packageInitEntries(pkg, func(imported *packages.Package) Package {
		return contextPackage(ctx, imported)
	})
	if err != nil {
		return nil, err
	}
	patchInits, err := coroProgramPatchInitializersByOwner(ctx)
	if err != nil {
		return nil, err
	}
	steps := make([]coroProgramBootstrapStepV1, 0, len(packageInits)+5)
	appendManaged := func(fn *ssa.Function, target, owner, label string, role uint32) error {
		step, err := selectCoroProgramManagedStepV2(ctx, fn, target, owner, label, role)
		if err != nil {
			return err
		}
		steps = append(steps, step)
		return nil
	}
	if err := appendManaged(runtimeInit, llssa.PkgRuntime+".init", llssa.PkgRuntime, "internal runtime init", coroProgramStepRoleRuntimeInitV2); err != nil {
		return nil, err
	}
	steps = append(steps, coroProgramBootstrapStepV1{
		Kind:       coroProgramStepDirectPlainV1,
		Role:       coroProgramStepRoleABIInitV2,
		FunctionID: "llgo.bootstrap.v2.compiler-abi-init",
		Target:     "init$abitypes",
	})
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
	steps = append(steps, publicRuntimeStep)
	scheduled := map[*ssa.Function]struct{}{runtimeInit: {}}
	if hasPublicRuntimeInit {
		scheduled[publicRuntimeInit] = struct{}{}
	}
	for _, entry := range packageInits {
		function, err := canonicalCoroProgramPackageInit(ctx, entry, patchInits)
		if err != nil {
			return nil, err
		}
		if _, duplicate := scheduled[function]; duplicate {
			continue
		}
		scheduled[function] = struct{}{}
		owner := coroProgramSourcePackagePath(function.Pkg.Pkg)
		if err := appendManaged(
			function,
			entry.name,
			owner,
			"package initializer "+entry.pkg.PkgPath,
			coroProgramStepRolePackageInitV2,
		); err != nil {
			return nil, err
		}
	}
	if err := appendManaged(aPkg.SSA.Func("init"), mainSymbolPrefix+".init", aPkg.PkgPath, "main package init", coroProgramStepRolePackageInitV2); err != nil {
		return nil, err
	}
	if err := appendManaged(aPkg.SSA.Func("main"), mainSymbolPrefix+".main", aPkg.PkgPath, "main", coroProgramStepRoleMainV2); err != nil {
		return nil, err
	}
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

// coroProgramPatchInitializersByOwner indexes the exact public initializer of
// every active patch package. x/tools keeps dependency-init calls pointed at
// the original package's synthetic init, while the frontend replaces that
// occurrence with this public patch initializer. Bootstrap selection must
// consume the same frozen mapping instead of rediscovering it from names.
func coroProgramPatchInitializersByOwner(ctx *context) (map[string]*ssa.Function, error) {
	if ctx == nil || ctx.coroEmission == nil {
		return nil, fmt.Errorf("coroutine package initializer resolution requires a frozen emission universe")
	}
	entries, err := ctx.coroEmission.CoroPatchInitEntries()
	if err != nil {
		return nil, err
	}
	byOwner := make(map[string]*ssa.Function, len(entries))
	for _, function := range entries {
		if function == nil || function.Pkg == nil || function.Pkg.Pkg == nil {
			return nil, fmt.Errorf("coroutine patch initializer has no exact package owner")
		}
		owner := coroProgramSourcePackagePath(function.Pkg.Pkg)
		if owner == "" || function.Name() != "init" {
			return nil, fmt.Errorf("coroutine patch initializer %q has invalid owner %q or function name", function.Name(), owner)
		}
		if previous := byOwner[owner]; previous != nil && previous != function {
			return nil, fmt.Errorf("coroutine patch package %q has multiple public initializers", owner)
		}
		byOwner[owner] = function
	}
	return byOwner, nil
}

func canonicalCoroProgramPackageInit(
	ctx *context, entry packageInitEntry, patchInits map[string]*ssa.Function,
) (*ssa.Function, error) {
	if ctx == nil || ctx.coroEmission == nil || entry.pkg == nil || entry.function == nil {
		return nil, fmt.Errorf("coroutine package initializer has incomplete frozen identity")
	}
	function := patchInits[entry.pkg.PkgPath]
	if function == nil {
		var ok bool
		function, ok = ctx.coroEmission.Resolve(entry.function)
		if !ok || function == nil {
			return nil, fmt.Errorf(
				"coroutine package initializer %s is absent from the frozen emission universe",
				entry.pkg.PkgPath,
			)
		}
	}
	if function.Pkg == nil || function.Pkg.Pkg == nil ||
		coroProgramSourcePackagePath(function.Pkg.Pkg) != entry.pkg.PkgPath ||
		function.Name() != "init" || function.Parent() != nil || function.Origin() != nil ||
		len(function.TypeArgs()) != 0 {
		return nil, fmt.Errorf(
			"coroutine package initializer %s did not resolve to its exact canonical top-level init",
			entry.pkg.PkgPath,
		)
	}
	return function, nil
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
	if fn.Pkg == nil || fn.Pkg.Pkg == nil ||
		coroProgramSourcePackagePath(fn.Pkg.Pkg) != owner ||
		fn.Parent() != nil || fn.Origin() != nil || len(fn.TypeArgs()) != 0 {
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
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: plain function %q target %q has unsupported execution constraints %s", label, fn.String(), plan.ID, unsupported)
		}
		return coroProgramBootstrapStepV1{
			Kind: coroProgramStepDirectPlainV1, Role: role, FunctionID: plan.ID, Target: target,
		}, nil

	case coro.EmitCoroutine:
		if rootDemand != coro.AsyncDemand || plan.Demand != coro.AsyncDemand || plan.FuncRep != coro.DirectCoro || plan.Primary != coro.PrimaryCoroutine || !plan.Effect.MaySuspend() {
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: coroutine target %q is not one async-only direct coroutine (root=%s demand=%s rep=%s primary=%s effect=%s; value-sites=%v)",
				label, plan.ID, rootDemand, plan.Demand, plan.FuncRep, plan.Primary, plan.Effect, coroProgramFunctionValueSites(ctx.coroPlan, fn))
		}
		// NeedsCleanupFrame is a property of the already-frozen coroutine body,
		// not a separate bootstrap execution protocol. Physical preflight proves
		// and emits that body's defer/recover frame before this selector publishes
		// its ordinary scheduler root descriptor.
		const supportedCoroutine = coro.MayUnwind | coro.NeedsCleanupFrame | coro.NeedsPreempt | coro.IRQUnsafe
		if unsupported := plan.Exec &^ supportedCoroutine; unsupported != 0 {
			trace := ""
			if unsupported.Contains(coro.OpaqueExec) {
				trace = "; opaque path: " + coroProgramOpaqueExecPath(ctx.coroPlan, fn)
			}
			return coroProgramBootstrapStepV1{}, fmt.Errorf("coroutine program bootstrap %s: coroutine function %q target %q has unsupported execution constraints %s%s", label, fn.String(), plan.ID, unsupported, trace)
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

// coroProgramSourcePackagePath is an ownership identity, not a linker-symbol
// projection. Alternate runtime packages retain their canonical source owner,
// while a user main package keeps its import path even when its physical ABI
// symbols are intentionally rewritten to main.*.
func coroProgramSourcePackagePath(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return strings.TrimPrefix(pkg.Path(), altPkgPathPrefix)
}

func coroProgramFunctionValueSites(plan *coro.SSAPlan, target *ssa.Function) []string {
	if plan == nil || target == nil {
		return nil
	}
	var sites []string
	for _, item := range plan.Functions() {
		owner := item.Function
		if owner == nil || plan.IgnoresBody(owner) {
			continue
		}
		operands := make([]*ssa.Value, 0, 8)
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				operands = instruction.Operands(operands[:0])
				for _, operand := range operands {
					if operand == nil || *operand != target {
						continue
					}
					if call, ok := instruction.(ssa.CallInstruction); ok && operand == &call.Common().Value && call.Common().StaticCallee() == target {
						continue
					}
					sites = append(sites, owner.String()+": "+instruction.String())
				}
			}
		}
	}
	sort.Strings(sites)
	return sites
}

func coroProgramOpaqueExecPath(plan *coro.SSAPlan, root *ssa.Function) string {
	if plan == nil || root == nil {
		return "unavailable"
	}
	seen := make(map[*ssa.Function]bool)
	var visit func(*ssa.Function, int) string
	visit = func(function *ssa.Function, depth int) string {
		if function == nil {
			return "<nil>"
		}
		name := function.String()
		if depth >= 32 {
			return name + " -> <depth-limit>"
		}
		if seen[function] {
			return name + " -> <cycle>"
		}
		seen[function] = true
		defer delete(seen, function)

		// Prefer the local open boundary over propagated target flags. Otherwise
		// an initializer SCC can hide the actual unresolved call behind a cycle.
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				callPlan, planned := plan.CallPlan(call)
				if !planned || callPlan.Kind == coro.CallSpawn || callPlan.Kind == coro.CallUnwind {
					continue
				}
				if callPlan.Open && callPlan.Unresolved == coro.UnknownManaged {
					return fmt.Sprintf("%s -> open call %q (kind=%d targets=%d)", name, call.String(), callPlan.Kind, len(callPlan.Targets))
				}
			}
		}
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				callPlan, planned := plan.CallPlan(call)
				if !planned || callPlan.Kind == coro.CallSpawn || callPlan.Kind == coro.CallUnwind {
					continue
				}
				for _, targetID := range callPlan.Targets {
					target, found := plan.Function(targetID)
					if !found || target == nil {
						continue
					}
					targetPlan, found := plan.FunctionPlan(target)
					if found && targetPlan.Exec.Contains(coro.OpaqueExec) && !seen[target] {
						return name + " -> " + visit(target, depth+1)
					}
				}
			}
		}
		for _, lowered := range plan.LoweredCalls(function) {
			targetPlan, found := plan.FunctionPlan(lowered.Target)
			if found && !lowered.UnwindOnly && targetPlan.Exec.Contains(coro.OpaqueExec) {
				return name + " -> lowered " + lowered.LogicalName + " -> " + visit(lowered.Target, depth+1)
			}
		}
		functionPlan, _ := plan.FunctionPlan(function)
		return fmt.Sprintf("%s (local=%s declared=%s)", name, functionPlan.LocalExec, functionPlan.DeclaredExec)
	}
	return visit(root, 0)
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
	for _, root := range plan.RootFactoryRoots() {
		if root.Function == nil || root.Function.Pkg != target.Pkg {
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
	if bootstrap == nil {
		return nil, nil
	}
	if bootstrap.Version != coroProgramBootstrapVersionV2 {
		return nil, fmt.Errorf("coroutine program bootstrap version %d is not the unique V2 startup model", bootstrap.Version)
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

func typeParamLen(list *types.TypeParamList) int {
	if list == nil {
		return 0
	}
	return list.Len()
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
	metadata.LoweringFactsSchema = ctx.coroPlanMetadata.LoweringFactsSchema
	metadata.LoweringFactsDigest = ctx.coroPlanMetadata.LoweringFactsDigest
	target := ctx.prog.TargetSpec()
	h := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		h.Write(length[:])
		h.Write([]byte(value))
	}
	if version != coroProgramBootstrapVersionV2 {
		return [16]byte{}, fmt.Errorf("coroutine program bootstrap hash has unsupported version %d", version)
	}
	write("llgo.coro.program-bootstrap.v" + strconv.FormatUint(uint64(version), 10))
	write(strconv.FormatUint(uint64(version), 10))
	write("flags=0")
	write("step={kind:u32,flags:u32,target:ptr,aux:uintptr}")
	write("bootstrap={version:u32,flags:u32,hash-lo:u64,hash-hi:u64,step-count:uintptr,steps:ptr,factory:ptr}")
	write("direct-plain=" + strconv.FormatUint(uint64(coroProgramStepDirectPlainV1), 10))
	write("coro-root=" + strconv.FormatUint(uint64(coroProgramStepCoroRootV1), 10))
	write("factory=compiler-static-mixed-v2:" + coroProgramBootstrapFactorySymbolV2)
	if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
		write("driver=runtime-static-single-p-native-v2:" +
			coroProgramBeginSymbolV1 + ":" +
			coroProgramRunSliceSymbolV2 + "(g:ptr,handle:ptr,budget:u32,out:*run-result-v2)->u32:" +
			coroProgramContinueSliceSymbolV2 + "(executor-slot:u32,executor-generation:u32,epoch:u32,budget:u32,out:*run-result-v2)->u32:" +
			coroProgramReportPanicSymbolV1 + "(g:ptr)->noreturn:" +
			"budget=" + strconv.FormatUint(uint64(coroProgramNativeRunBudgetV2), 10) + ":" +
			"run-result-v2={flags:u32,used:u32,executor-slot:u32,executor-generation:u32,epoch:u32,deadline-lo:u32,deadline-hi:u32,reserved:u32}:" +
			"complete=" + strconv.FormatUint(uint64(coroProgramDriveCompleteV2), 10) + ":" +
			"yielded=" + strconv.FormatUint(uint64(coroProgramDriveYieldedV2), 10) + ":" +
			"panic=" + strconv.FormatUint(uint64(coroProgramDrivePanicV2), 10) + ":" +
			"inline-flags=" + strconv.FormatUint(uint64(coroProgramRunMoreV2|coroProgramRunRequestInlineV2), 10))
	} else if hostCoroPullRuntimeABI(ctx.buildConf) {
		write("driver=runtime-static-single-p-host-pull-v1:" +
			coroProgramBeginSymbolV1 + ":" +
			coroProgramRunSliceSymbolV2 + "(g:ptr,handle:ptr,budget:u32,out:*run-result-v2)->u32:" +
			coroProgramContinueSliceSymbolV2 + "(executor-slot:u32,executor-generation:u32,epoch:u32,budget:u32,out:*run-result-v2)->u32:" +
			"budget=" + strconv.FormatUint(uint64(coroProgramNativeRunBudgetV2), 10) + ":" +
			"run-result-v2={flags:u32,used:u32,executor-slot:u32,executor-generation:u32,epoch:u32,deadline-lo:u32,deadline-hi:u32,reserved:u32}:" +
			"complete=" + strconv.FormatUint(uint64(coroProgramDriveCompleteV2), 10) + ":" +
			"suspended=" + strconv.FormatUint(uint64(coroProgramDriveSuspendedV2), 10) + ":" +
			"yielded=" + strconv.FormatUint(uint64(coroProgramDriveYieldedV2), 10) + ":" +
			"queued-flags=" + strconv.FormatUint(uint64(coroProgramRunMoreV2|coroProgramRunRequestQueuedV2), 10) + ":" +
			"blocked-flags=" + strconv.FormatUint(uint64(coroProgramRunBlockedV2|coroProgramRunHasDeadlineV2), 10) + ":" +
			"pull=" + coroHostNextActionSymbolV1 + ":" + coroHostProfileSymbolV1 + ":" +
			coroHostNextDeadlineSymbolV1 + ":" + coroHostPublishTimeSymbolV1 + ":" +
			coroHostPublishWallTimeSymbolV1 + ":" +
			coroHostAckCancelSymbolV1 + ":" + coroHostContinueSliceSymbolV1 + ":" +
			coroHostNextOperationSymbolV1 + ":" + coroHostCompleteOperationSymbolV1)
	} else {
		write("driver=runtime-static-single-p-v1:" + coroProgramBeginSymbolV1 + ":" + coroProgramRunSymbolV1 + ":" + coroProgramContinueSymbolV1 + ":continue(epoch:u32)->void")
	}
	write("resume-decision-v1=" + coroRunDecisionTakeSymbolV1 + "(g:ptr,expected-epoch:u32,expected-generation:u32,outcome:*u32,case:*u32,task-kind:*u32,operation-source-slot:*u32,operation-generation:*u32)->void")
	write("resume-decision-zero-v1=" + coroRunDecisionTakeZeroSymbolV1 + "(g:ptr)->u32")
	write("parameterized-fault-v2=__llgo_coro_fault_prepare_v2(g:ptr,handle:ptr,header:ptr,kind:u32,arg0:u64,arg1:uintptr)->void;" +
		"__llgo_coro_fault_payload_v2(kind:u32,arg0:u64,arg1:uintptr,type-out:ptr,data-out:ptr)->void")
	if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
		write("native-doorbell=pipe-poll-operation-id-v2")
	}
	if coroTimerRuntimeABI(ctx.buildConf) {
		write("timer=source-aware-park-v2:" +
			coroTimerParkSymbolV2 + "(g:ptr,handle:ptr,header:ptr,state:ptr,delay-ns:i64)->void;" +
			coroTimerParkControlledSymbolV2 + "(g:ptr,handle:ptr,header:ptr,state:ptr,controller:ptr,control:*u32,owner-route:*u32,expected:u32,deadline-ns:i64)->void;" +
			coroTimerResumeSymbolV2 + "(g:ptr,state:ptr)->u32;" +
			coroTimerRequestControlledSymbolV2 + "(route:u32)->u32")
		write("keyed-park-v2=" +
			coroKeyedParkSymbolV2 + "(g:ptr,handle:ptr,header:ptr,state:ptr)->void;" +
			coroKeyedResumeSymbolV2 + "(g:ptr,state:ptr)->u32")
		write("semaphore-owner-v2=" +
			coroSemaphorePrepareOrAbortSymbolV2 + "(state:ptr,addr:ptr)->void;" +
			coroSemaphoreReleaseOrAbortSymbolV2 + "(addr:ptr)->void")
		write("notify-owner-v2=" +
			coroNotifyPrepareOrAbortSymbolV2 + "(state:ptr,notify-addr:ptr,target:u32)->void;" +
			coroNotifyOneOrAbortSymbolV2 + "(notify-addr:ptr,wait-snapshot:u32)->void;" +
			coroNotifyAllOrAbortSymbolV2 + "(notify-addr:ptr,wait-snapshot:u32)->void")
	}
	if nativeCoroTimerRuntimeABI(ctx.buildConf) {
		write("native-poll=source-aware-park-v2:" +
			coroPollParkSymbolV2 + "(g:ptr,handle:ptr,header:ptr,state:ptr,context:uintptr,fd:i32,interest:u32,deadline-ns:i64)->void;" +
			coroPollResumeSymbolV2 + "(g:ptr,state:ptr)->u32;" +
			coroPollUpdateDeadlineOrAbortSymbolV1 + "(context:uintptr,interest:u32,deadline-ns:i64)->void;" +
			coroPollPostClosingOrAbortSymbolV1 + "(context:uintptr,interest:u32)->void")
	}
	write("channel-v1=" +
		coroChanSendParkSymbolV1 + "(g:ptr,handle:ptr,header:ptr,channel:ptr,elem:ptr,state:ptr,size:uintptr)->void;" +
		coroChanRecvParkSymbolV1 + "(g:ptr,handle:ptr,header:ptr,channel:ptr,elem:ptr,state:ptr,size:uintptr)->void;" +
		coroChanResumeSymbolV1 + "(g:ptr,state:ptr)->u32;" +
		"send-closed-fault=__llgo_coro_fault_prepare_v1:kind=3")
	if ctx.buildConf.coroWorkerSupported() {
		write("worker-v1=" +
			coroWorkerParkSymbolV1 + "(g:ptr,handle:ptr,header:ptr,state:ptr,fn:uintptr,argc:u32,a0:uintptr,a1:uintptr,a2:uintptr,a3:uintptr,a4:uintptr,a5:uintptr,a6:uintptr,a7:uintptr,a8:uintptr)->void;" +
			coroWorkerResumeSymbolV1 + "(g:ptr,state:ptr,r1:*uintptr,r2:*uintptr,errno:*uintptr)->u32")
	}
	if ctx.buildConf.coroHostOperationSupported() {
		write("host-operation-v1=" +
			coroHostOperationParkSymbolV1 + "(g:ptr,handle:ptr,header:ptr,state:ptr,opcode:u32,argc:u32,a0:uintptr,a1:uintptr,a2:uintptr,a3:uintptr,a4:uintptr,a5:uintptr,a6:uintptr,a7:uintptr,a8:uintptr)->void;" +
			coroHostOperationResumeSymbolV1 + "(g:ptr,state:ptr,r1:*uintptr,r2:*uintptr,errno:*uintptr)->u32;" +
			coroHostNextOperationSymbolV1 + "(out:*host-operation-v1)->u32;" +
			coroHostCompleteOperationSymbolV1 + "(source-slot:u32,generation:u32,flags:u32,count:u32,r1-lo:u32,r1-hi:u32,r2-lo:u32,r2-hi:u32,errno-lo:u32,errno-hi:u32)->u32")
	}
	write("header=physical-abi-v1-line-u32;descriptor=trace-v1")
	write(ctx.coroPlanDigest)
	write(metadata.CoroABI)
	write(metadata.SchedulerABI)
	write(metadata.PanicABI)
	write(metadata.FuncRepABI)
	write(metadata.FrameRetentionABI)
	write(metadata.LoweringFactsSchema)
	write(metadata.LoweringFactsDigest)
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
