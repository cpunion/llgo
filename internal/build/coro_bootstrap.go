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
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
)

const (
	coroProgramBootstrapVersionV1               uint32 = 1
	coroProgramBootstrapFactorySymbolV1                = "__llgo_coro_program_bootstrap_factory_v1"
	coroProgramBootstrapFrameDescriptorPrefixV1        = "__llgo_coro_program_bootstrap_frame_descriptor_v1."
	coroProgramBeginSymbolV1                           = "__llgo_coro_program_begin_v1"
	coroProgramRunSymbolV1                             = "__llgo_coro_program_run_v1"

	// Step kinds and semantic roles are part of the cross-target bootstrap ABI.
	// Keep these numeric values synchronized with ssa and runtime/internal/coro.
	coroProgramStepDirectPlainV1 uint32 = 1
	coroProgramStepCoroRootV1    uint32 = 2
	coroProgramStepRoleInitV1    uint32 = 1
	coroProgramStepRoleMainV1    uint32 = 2
)

type coroProgramBootstrapStepV1 struct {
	Kind       uint32
	Role       uint32
	FunctionID coro.FunctionID
	Target     string
	Aux        uint64
}

type coroProgramBootstrapV1 struct {
	StepHash [16]byte
	Steps    []coroProgramBootstrapStepV1
}

func validateCoroProgramBootstrapConfig(conf *Config) error {
	if conf == nil {
		return nil
	}
	if conf.EnableCoroProgramBootstrapRun && !conf.EnableCoroProgramBootstrapABI {
		return fmt.Errorf("enable coroutine program bootstrap runtime: program bootstrap ABI is required")
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
		bootstrap, err := selectCoroProgramBootstrapV1(ctx, pkg)
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
	target := ctx.prog.TargetSpec()
	h := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		h.Write(length[:])
		h.Write([]byte(value))
	}
	write("llgo.coro.program-bootstrap.v1")
	write(strconv.FormatUint(uint64(coroProgramBootstrapVersionV1), 10))
	write("flags=0")
	write("step={kind:u32,flags:u32,target:ptr,aux:uintptr}")
	write("bootstrap={version:u32,flags:u32,hash-lo:u64,hash-hi:u64,step-count:uintptr,steps:ptr,factory:ptr}")
	write("direct-plain=" + strconv.FormatUint(uint64(coroProgramStepDirectPlainV1), 10))
	write("coro-root=" + strconv.FormatUint(uint64(coroProgramStepCoroRootV1), 10))
	if ctx.buildConf.EnableCoroProgramBootstrapRun {
		write("factory=compiler-direct-plain-v1:" + coroProgramBootstrapFactorySymbolV1)
		write("driver=runtime-static-single-p-v1:" + coroProgramBeginSymbolV1 + ":" + coroProgramRunSymbolV1)
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
		write(strconv.FormatUint(step.Aux, 10))
	}
	sum := h.Sum(nil)
	var hash [16]byte
	copy(hash[:], sum[:len(hash)])
	return hash, nil
}
