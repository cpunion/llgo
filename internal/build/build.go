/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/buildenv"
	"github.com/goplus/llgo/internal/cabi"
	"github.com/goplus/llgo/internal/clang"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/crosscompile"
	"github.com/goplus/llgo/internal/env"
	"github.com/goplus/llgo/internal/firmware"
	"github.com/goplus/llgo/internal/flash"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/goplus/llgo/internal/header"
	"github.com/goplus/llgo/internal/lto"
	"github.com/goplus/llgo/internal/mockable"
	"github.com/goplus/llgo/internal/monitor"
	"github.com/goplus/llgo/internal/optlevel"
	"github.com/goplus/llgo/internal/packages"
	"github.com/goplus/llgo/internal/pclnpost"
	"github.com/goplus/llgo/internal/typepatch"
	"github.com/goplus/llgo/ssa/abi"
	xenv "github.com/goplus/llgo/xtool/env"
	"github.com/goplus/llgo/xtool/env/llvm"
	gllvm "github.com/xgo-dev/llvm"

	llruntime "github.com/goplus/llgo/runtime"
	llssa "github.com/goplus/llgo/ssa"
)

type Mode int

const (
	ModeBuild Mode = iota
	ModeInstall
	ModeRun
	ModeTest
	ModeCmpTest
	ModeGen
)

type BuildMode string

const (
	BuildModeExe      BuildMode = "exe"
	BuildModeCArchive BuildMode = "c-archive"
	BuildModeCShared  BuildMode = "c-shared"
)

// ValidateBuildMode checks if the build mode is valid
func ValidateBuildMode(mode string) error {
	switch BuildMode(mode) {
	case BuildModeExe, BuildModeCArchive, BuildModeCShared:
		return nil
	default:
		return fmt.Errorf("invalid build mode %q, must be one of: exe, c-archive, c-shared", mode)
	}
}

type AbiMode = cabi.Mode

const (
	debugBuild = packages.DebugPackagesLoad
)

// OutFmts contains output format specifications for embedded targets
type OutFmts struct {
	Bin bool // Generate binary output (.bin)
	Hex bool // Generate Intel hex output (.hex)
	Img bool // Generate image output (.img)
	Uf2 bool // Generate UF2 output (.uf2)
	Zip bool // Generate ZIP/DFU output (.zip)
}

// OutFmtDetails contains detailed output file paths for each format
type OutFmtDetails struct {
	Out string // Base output file path
	Bin string // Binary output file path (.bin)
	Hex string // Intel hex output file path (.hex)
	Img string // Image output file path (.img)
	Uf2 string // UF2 output file path (.uf2)
	Zip string // ZIP/DFU output file path (.zip)
}

// ModuleHook observes a package module immediately after it is generated and
// before TransformModule mutates it. The callback runs synchronously and
// receives the live llvm.Module, so callers that need a stable snapshot should
// consume it immediately (for example, by calling mod.String() inside the
// hook).
type ModuleHook func(pkg Package)

// CoroPlanInput is the immutable whole-build input supplied to a
// CoroPlanBuilder. EmissionUniverse contains the exact SSA function objects
// selected after patch/skip resolution and lazy frontend materialization.
type CoroPlanInput struct {
	Program          *ssa.Program
	EmissionUniverse *coro.SSAEmissionUniverse

	resolveFunction                func(*ssa.Function) (*ssa.Function, bool)
	augmentFunctionIDs             func(coro.FunctionIDConfig) coro.FunctionIDConfig
	functionBackground             func(*ssa.Function) (llssa.Background, bool, error)
	foreignNoBlock                 func(*ssa.Function) (cl.CoroForeignNoBlockCertificate, bool, error)
	intrinsicCallSemantics         func(ssa.CallInstruction) (cl.CoroIntrinsicCallSemantics, bool, error)
	rawFunctionAddressCallArgument func(ssa.CallInstruction, int) (bool, error)
	demandReferences               func(*ssa.Function) ([]*ssa.Function, error)
	loweredCalls                   func(*ssa.Function) ([]coro.SSALoweredCall, error)
	requiredRoots                  coro.Roots
	requiredPlain                  map[*ssa.Function]struct{}
	requiredDirectPlain            []requiredCoroDirectPlainCallArgument
	requiredClosedDynamic          map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate
	enableClosedStaticSpawn        bool
	recordAnalysis                 func(*coro.SSAPlan)
}

type coroCallArgumentKey struct {
	call     ssa.CallInstruction
	argument int
}

type requiredCoroDirectPlainCallArgument struct {
	call     ssa.CallInstruction
	argument int
	target   *ssa.Function
}

// ResolveFunction maps a function that may be reached through an original
// patched declaration to the exact canonical pointer in EmissionUniverse.
// Builders may use it while selecting roots or attaching frontend policy.
func (in CoroPlanInput) ResolveFunction(fn *ssa.Function) (*ssa.Function, bool) {
	if fn == nil {
		return nil, false
	}
	if in.resolveFunction != nil {
		return in.resolveFunction(fn)
	}
	if in.EmissionUniverse == nil {
		return fn, true
	}
	return fn, in.EmissionUniverse.Contains(fn)
}

// Analyze applies the frozen emission universe to config before running the
// coroutine analysis. The frozen frontend patch-alias resolver is
// authoritative, so roots, body callees, function values, and later code
// generation all use the same exact *ssa.Function objects. The frontend's
// structural identity resolver is composed with builder identity policy.
// Builders use this helper instead of calling AnalyzeSSA directly.
func (in CoroPlanInput) Analyze(roots coro.Roots, config coro.SSAConfig) (*coro.SSAPlan, error) {
	// Compiler/runtime ABI roots are added only by the build driver. Copy both
	// slices so a builder retains ownership of its input and cannot mutate the
	// production root set after analysis begins.
	allRoots := make(coro.Roots, 0, len(roots)+len(in.requiredRoots))
	allRoots = append(allRoots, roots...)
	allRoots = append(allRoots, in.requiredRoots...)
	// Frozen InC is a physical lowering fact: cl never emits the fallback Go
	// SSA body. It does not by itself prove that the foreign operation is
	// nonblocking. Preserve an explicit known/unknown-foreign effect summary;
	// otherwise use the conservative unknown-foreign boundary.
	if in.functionBackground != nil || in.foreignNoBlock != nil || config.ClassifyFunction != nil {
		classify := config.ClassifyFunction
		config.ClassifyFunction = func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			var policy coro.SSAFunctionPolicy
			var err error
			if classify != nil {
				policy, err = classify(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, err
				}
			}
			frontendC := false
			if in.functionBackground != nil {
				background, classified, err := in.functionBackground(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend ABI for %q: %w", fn.Name(), err)
				}
				frontendC = classified && background == llssa.InC
			}
			var certificate cl.CoroForeignNoBlockCertificate
			certified := false
			if in.foreignNoBlock != nil {
				certificate, certified, err = in.foreignNoBlock(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend foreign noblock certificate for %q: %w", fn.Name(), err)
				}
			}
			if requested := policy.ForeignNoBlockCertificate; requested != "" {
				if !certified {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot certify foreign function %q without exact frozen frontend noblock metadata", fn.Name())
				}
				if requested != certificate.ID {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder foreign noblock certificate for %q conflicts with the frozen frontend proof", fn.Name())
				}
			}
			if certified {
				if !frontendC {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen foreign noblock certificate for %q does not name a frontend C declaration", fn.Name())
				}
				if policy.Effect != coro.NoSuspend || policy.Exec != 0 || policy.NeedsDispatch ||
					policy.OverrideExternal && policy.External != coro.ExternalKnown {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frontend C declaration %q conflicts with its frozen foreign noblock certificate", fn.Name())
				}
				policy.IgnoreBody = true
				policy.External = coro.ExternalKnown
				policy.OverrideExternal = true
				// A noblock proof is not an async-signal-safety proof. Preserve
				// IRQUnsafe in the plan/digest while removing only the opaque
				// BlockForeign/WaitForeign boundary.
				policy.Exec = coro.IRQUnsafe
				policy.ForeignNoBlockCertificate = certificate.ID
			}
			if policy.IgnoreBody && !frontendC {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot ignore the SSA body of non-C function %q", fn.Name())
			}
			if !frontendC {
				return policy, nil
			}
			if policy.OverrideExternal && policy.External != coro.ExternalUnknownForeign && policy.External != coro.ExternalKnown {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("frontend C declaration %q conflicts with external classification %s", fn.Name(), policy.External)
			}
			policy.IgnoreBody = true
			if !policy.OverrideExternal {
				policy.External = coro.ExternalUnknownForeign
				policy.OverrideExternal = true
			}
			return policy, nil
		}
	}
	// A structured coroutine intrinsic has no managed callee edge: cl replaces
	// its declaration call with a suspend in the owner's exact frame. Seed that
	// physical effect from the same frozen call-site semantics used to elide the
	// declaration, so synchronous source callers are transparently coroutine
	// primary bodies and the plan digest records both the owner effect and site.
	if in.intrinsicCallSemantics != nil {
		classify := config.ClassifyFunction
		config.ClassifyFunction = func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			var policy coro.SSAFunctionPolicy
			var err error
			if classify != nil {
				policy, err = classify(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, err
				}
			}
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(*ssa.Call)
					if !ok {
						continue
					}
					rawCallee := call.Common().StaticCallee()
					if rawCallee == nil {
						continue
					}
					if _, frozen := in.ResolveFunction(rawCallee); !frozen {
						// Frontend-elided noinit declarations (notably unsafe.init)
						// are intentionally outside the frozen emission universe and
						// carry no structured intrinsic effect.
						continue
					}
					semantics, intrinsic, err := in.intrinsicCallSemantics(call)
					if err != nil {
						return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen intrinsic effect in %q: %w", fn.Name(), err)
					}
					if intrinsic && semantics.SuspendsCurrentFrame() {
						policy.Effect = policy.Effect.Join(coro.MayPark)
					}
				}
			}
			return policy, nil
		}
	}
	// A source `go f(args)` is a scheduler boundary even though CallSpawn
	// deliberately does not taint its owner in the generic effect graph. The
	// no-TLS lowering must retain the owner's exact G explicitly. The spawned
	// target is also a coroutine primary even when its source body is currently
	// bounded: otherwise a future CPU-heavy/looping version could run forever in
	// a synchronous plain adapter with no preemption cut. Static sync callers are
	// then tainted through the ordinary effect graph and await this same unique
	// target body.
	if in.enableClosedStaticSpawn {
		seeded := make(map[*ssa.Function]struct{})
		var functions []*ssa.Function
		if in.EmissionUniverse != nil {
			functions = in.EmissionUniverse.Functions()
		} else {
			for fn := range ssautil.AllFunctions(in.Program) {
				functions = append(functions, fn)
			}
			slices.SortFunc(functions, func(left, right *ssa.Function) int {
				if left == nil {
					if right == nil {
						return 0
					}
					return -1
				}
				if right == nil {
					return 1
				}
				return strings.Compare(left.String(), right.String())
			})
		}
		for _, fn := range functions {
			if fn == nil {
				continue
			}
			if in.functionBackground != nil {
				background, classified, err := in.functionBackground(fn)
				if err != nil {
					return nil, fmt.Errorf("classify closed static spawn owner %q frontend ABI: %w", fn.Name(), err)
				}
				if classified && background != llssa.InGo {
					continue
				}
			}
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					spawn, ok := instruction.(*ssa.Go)
					if !ok {
						continue
					}
					target, err := in.closedStaticSpawnTarget(fn, spawn)
					if err != nil {
						return nil, fmt.Errorf("closed static spawn in %q: %w", fn.Name(), err)
					}
					seeded[fn] = struct{}{}
					seeded[target] = struct{}{}
				}
			}
		}
		classify := config.ClassifyFunction
		config.ClassifyFunction = func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			var policy coro.SSAFunctionPolicy
			var err error
			if classify != nil {
				policy, err = classify(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, err
				}
			}
			if _, required := seeded[fn]; required {
				if policy.IgnoreBody {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("closed static spawn function %q is not a Go-emitted body", fn.Name())
				}
				policy.Effect = policy.Effect.Join(coro.YieldOnly)
			}
			return policy, nil
		}
	}
	if len(in.requiredPlain) != 0 {
		classify := config.ClassifyFunction
		config.ClassifyFunction = func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			var policy coro.SSAFunctionPolicy
			var err error
			if classify != nil {
				policy, err = classify(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, err
				}
			}
			if _, required := in.requiredPlain[fn]; !required {
				return policy, nil
			}
			if policy.Effect != coro.NoSuspend {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("compiler runtime ABI function %q conflicts with required no-suspend policy: %s", fn.Name(), policy.Effect)
			}
			// IRQUnsafe is an entry-context restriction, not a requirement for a
			// second physical body. Compiler/runtime ABI helpers execute on the
			// ordinary scheduler/executor stack, never as an IRQ root, so retain
			// the bit in the frozen plan while keeping the exact required-plain
			// implementation. ThreadAffine and opaque/blocking execution remain
			// rejected until their scheduler protocols exist.
			const supportedExec = coro.MayUnwind | coro.NeedsCleanupFrame | coro.IRQUnsafe
			if unsupported := policy.Exec &^ supportedExec; unsupported != 0 {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("compiler runtime ABI function %q conflicts with required plain execution policy: %s", fn.Name(), unsupported)
			}
			if policy.NeedsDispatch {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("compiler runtime ABI function %q conflicts with required direct representation", fn.Name())
			}
			background := llssa.Background(0)
			classified := false
			if in.functionBackground != nil {
				background, classified, err = in.functionBackground(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify required runtime ABI function %q: %w", fn.Name(), err)
				}
			}
			policy.TrustedNoPreempt = true
			if classified && background == llssa.InC {
				if !policy.IgnoreBody || !policy.OverrideExternal || (policy.External != coro.ExternalUnknownForeign && policy.External != coro.ExternalKnown) {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("compiler runtime ABI C declaration %q conflicts with frozen foreign classification: %s", fn.Name(), policy.External)
				}
				policy.External = coro.ExternalKnown
				policy.OverrideExternal = true
			} else if classified && background == llssa.InGo && len(fn.Blocks) != 0 {
				if policy.OverrideExternal && policy.External != coro.Defined {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("compiler runtime ABI function %q conflicts with required defined classification: %s", fn.Name(), policy.External)
				}
			} else {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("compiler runtime ABI declaration %q has no frozen frontend C ABI proof", fn.Name())
			}
			return policy, nil
		}
	}
	classifyElided := config.ClassifyElidedCall
	config.ClassifyElidedCall = func(caller *ssa.Function, call ssa.CallInstruction) (bool, error) {
		frontendElided := frontendElidesNoInitCall(call)
		if !frontendElided && in.intrinsicCallSemantics != nil {
			semantics, intrinsic, err := in.intrinsicCallSemantics(call)
			if err != nil {
				return false, fmt.Errorf("classify frozen intrinsic call in %q: %w", caller.Name(), err)
			}
			frontendElided = intrinsic && semantics.ElidesManagedCall()
		}
		if classifyElided != nil {
			requested, err := classifyElided(caller, call)
			if err != nil {
				return false, err
			}
			if requested && !frontendElided {
				return false, fmt.Errorf("builder cannot elide ordinary call in %q; only calls omitted by the build frontend may be elided", caller.Name())
			}
		}
		return frontendElided, nil
	}
	if in.rawFunctionAddressCallArgument != nil || config.ClassifyRawFunctionAddressCallArgument != nil {
		classifyRawAddress := config.ClassifyRawFunctionAddressCallArgument
		config.ClassifyRawFunctionAddressCallArgument = func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			compilerRequired := false
			var err error
			if in.rawFunctionAddressCallArgument != nil {
				compilerRequired, err = in.rawFunctionAddressCallArgument(call, argument)
				if err != nil {
					return false, fmt.Errorf("classify frozen raw function-address argument %d in %q: %w", argument, caller.Name(), err)
				}
			}
			if classifyRawAddress != nil {
				requested, err := classifyRawAddress(caller, call, argument)
				if err != nil {
					return false, err
				}
				if requested && !compilerRequired {
					return false, fmt.Errorf("builder cannot authorize raw function-address lowering for non-compiler call argument %d in %q", argument, caller.Name())
				}
			}
			return compilerRequired, nil
		}
	}
	if len(in.requiredDirectPlain) != 0 || config.ClassifyDirectPlainCallArgument != nil {
		required := make(map[coroCallArgumentKey]struct{}, len(in.requiredDirectPlain))
		for _, use := range in.requiredDirectPlain {
			required[coroCallArgumentKey{call: use.call, argument: use.argument}] = struct{}{}
		}
		classifyDirectPlain := config.ClassifyDirectPlainCallArgument
		config.ClassifyDirectPlainCallArgument = func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			key := coroCallArgumentKey{call: call, argument: argument}
			_, compilerRequired := required[key]
			if classifyDirectPlain != nil {
				requested, err := classifyDirectPlain(caller, call, argument)
				if err != nil {
					return false, err
				}
				if requested && !compilerRequired {
					return false, fmt.Errorf("builder cannot authorize direct-plain ABI for non-compiler call argument %d in %q", argument, caller.Name())
				}
			}
			return compilerRequired, nil
		}
	}
	if len(in.requiredClosedDynamic) != 0 || config.ClassifyClosedDynamicCall != nil {
		classifyClosed := config.ClassifyClosedDynamicCall
		config.ClassifyClosedDynamicCall = func(caller *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
			compilerCertificate, compilerRequired := in.requiredClosedDynamic[call]
			if classifyClosed != nil {
				requested, classified, err := classifyClosed(caller, call)
				if err != nil {
					return coro.SSAClosedDynamicCallCertificate{}, false, err
				}
				if !classified && (requested.MayBeNil || len(requested.Targets) != 0) {
					return coro.SSAClosedDynamicCallCertificate{}, false, fmt.Errorf("builder returned closed dynamic call facts without classifying the call in %q", caller.Name())
				}
				if classified && !compilerRequired {
					return coro.SSAClosedDynamicCallCertificate{}, false, fmt.Errorf("builder cannot close ordinary dynamic call in %q without a frozen compiler field-flow proof", caller.Name())
				}
				if classified && !sameCoroClosedDynamicCallCertificate(requested, compilerCertificate) {
					return coro.SSAClosedDynamicCallCertificate{}, false, fmt.Errorf("builder closed dynamic call certificate in %q conflicts with the frozen compiler proof", caller.Name())
				}
			}
			if !compilerRequired {
				return coro.SSAClosedDynamicCallCertificate{}, false, nil
			}
			return cloneCoroClosedDynamicCallCertificate(compilerCertificate), true, nil
		}
	}
	if in.demandReferences != nil || config.ClassifyDemandReferences != nil {
		classifyDemandReferences := config.ClassifyDemandReferences
		config.ClassifyDemandReferences = func(owner *ssa.Function) ([]*ssa.Function, error) {
			var compilerTargets []*ssa.Function
			var err error
			if in.demandReferences != nil {
				compilerTargets, err = in.demandReferences(owner)
				if err != nil {
					return nil, fmt.Errorf("classify frozen frontend demand references for %q: %w", owner.Name(), err)
				}
			}
			compilerTargets = append([]*ssa.Function(nil), compilerTargets...)
			if classifyDemandReferences != nil {
				requested, err := classifyDemandReferences(owner)
				if err != nil {
					return nil, err
				}
				if !sameExactCoroFunctionReferences(requested, compilerTargets) {
					return nil, fmt.Errorf("builder demand references in %q conflict with the frozen frontend method-table references", owner.Name())
				}
			}
			return compilerTargets, nil
		}
	}
	if in.loweredCalls != nil || config.ClassifyLoweredCalls != nil {
		classifyLoweredCalls := config.ClassifyLoweredCalls
		config.ClassifyLoweredCalls = func(owner *ssa.Function) ([]coro.SSALoweredCall, error) {
			var compilerCalls []coro.SSALoweredCall
			var err error
			if in.loweredCalls != nil {
				compilerCalls, err = in.loweredCalls(owner)
				if err != nil {
					return nil, fmt.Errorf("classify frozen frontend lowered calls for %q: %w", owner.Name(), err)
				}
			}
			compilerCalls = append([]coro.SSALoweredCall(nil), compilerCalls...)
			if classifyLoweredCalls != nil {
				requested, err := classifyLoweredCalls(owner)
				if err != nil {
					return nil, err
				}
				if !sameExactCoroLoweredCalls(requested, compilerCalls) {
					return nil, fmt.Errorf("builder lowered calls in %q conflict with the frozen frontend helper calls", owner.Name())
				}
			}
			return compilerCalls, nil
		}
	}
	if in.augmentFunctionIDs != nil {
		config.FunctionIDs = in.augmentFunctionIDs(config.FunctionIDs)
	}
	config.ResolveFunction = func(fn *ssa.Function) (*ssa.Function, bool, error) {
		canonical, ok := in.ResolveFunction(fn)
		return canonical, ok, nil
	}
	config.EmissionUniverse = in.EmissionUniverse
	plan, err := coro.AnalyzeSSA(in.Program, allRoots, config)
	if err == nil {
		err = validateRequiredCoroDirectPlainCallArguments(plan, in.requiredDirectPlain)
	}
	if err == nil {
		err = validateRequiredCoroClosedDynamicCalls(plan, in.requiredClosedDynamic)
	}
	if err == nil && in.enableClosedStaticSpawn {
		err = validateClosedStaticSpawnPlan(plan)
	}
	if err == nil && in.recordAnalysis != nil {
		in.recordAnalysis(plan)
	}
	return plan, err
}

func (in CoroPlanInput) closedStaticSpawnTarget(owner *ssa.Function, spawn *ssa.Go) (*ssa.Function, error) {
	if owner == nil || spawn == nil || spawn.Common() == nil || spawn.Parent() != owner {
		return nil, fmt.Errorf("requires an exact owner and call site")
	}
	common := spawn.Common()
	raw, direct := common.Value.(*ssa.Function)
	if !direct || raw == nil || common.IsInvoke() || common.Method != nil || common.StaticCallee() != raw {
		return nil, fmt.Errorf("requires a direct static function operand; closures, methods, interfaces, and function values are unsupported")
	}
	target, ok := in.ResolveFunction(raw)
	if !ok || target == nil {
		return nil, fmt.Errorf("target %q is outside the frozen emission universe", raw.Name())
	}
	if target.Parent() != nil || len(target.FreeVars) != 0 || target.Synthetic != "" || target.Origin() != nil || len(target.TypeArgs()) != 0 {
		return nil, fmt.Errorf("target %q is not an exact non-capturing top-level function", target.Name())
	}
	if params := target.TypeParams(); params != nil && params.Len() != 0 {
		return nil, fmt.Errorf("target %q is a generic declaration", target.Name())
	}
	sig := target.Signature
	if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Results().Len() != 0 ||
		typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 {
		return nil, fmt.Errorf("target %q must have a non-method, non-variadic, zero-result signature", target.Name())
	}
	if len(target.Blocks) == 0 {
		return nil, fmt.Errorf("target %q has no defined Go body", target.Name())
	}
	if in.functionBackground != nil {
		background, classified, err := in.functionBackground(target)
		if err != nil {
			return nil, fmt.Errorf("classify target %q frontend ABI: %w", target.Name(), err)
		}
		if !classified || background != llssa.InGo {
			return nil, fmt.Errorf("target %q is not one frozen Go-emitted body", target.Name())
		}
	}
	return target, nil
}

func validateClosedStaticSpawnPlan(plan *coro.SSAPlan) error {
	if plan == nil {
		return fmt.Errorf("closed static spawn validation requires a coroutine plan")
	}
	for _, owner := range plan.Functions() {
		if owner.Function == nil || owner.Plan.Emission == coro.EmitNone || plan.IgnoresBody(owner.Function) {
			continue
		}
		for _, block := range owner.Function.Blocks {
			for _, instruction := range block.Instrs {
				spawn, ok := instruction.(*ssa.Go)
				if !ok {
					continue
				}
				if _, _, err := plan.ResolveClosedStaticSpawn(spawn); err != nil {
					return fmt.Errorf("closed static spawn in %q: %w", owner.Plan.ID, err)
				}
			}
		}
	}
	return nil
}

func coroPlanContainsSpawn(plan *coro.SSAPlan) bool {
	if plan == nil {
		return false
	}
	for _, owner := range plan.Functions() {
		if owner.Function == nil || owner.Plan.Emission == coro.EmitNone || plan.IgnoresBody(owner.Function) {
			continue
		}
		for _, block := range owner.Function.Blocks {
			for _, instruction := range block.Instrs {
				if _, spawn := instruction.(*ssa.Go); spawn {
					return true
				}
			}
		}
	}
	return false
}

func validateCoroClosedStaticSpawnRunGate(conf *Config, plan *coro.SSAPlan) error {
	if conf == nil || !conf.EnableCoroClosedStaticSpawn {
		return nil
	}
	if !conf.EnableCoroProgramBootstrapRun {
		return fmt.Errorf("validate coroutine closed static spawn: runnable program bootstrap v2 is required")
	}
	if plan == nil {
		return fmt.Errorf("validate coroutine closed static spawn: runnable capability requires a coroutine plan")
	}
	// Main-return cancellation can safely retire ready/yielded children and a
	// structured await tree. Platform, host, foreign, channel/select and opaque
	// waits need separate producer quiescence/cancellation protocols, so keep
	// those targets outside this first production slice.
	allowed := coro.YieldOnly | coro.AwaitStructured
	for _, owner := range plan.Functions() {
		if owner.Function == nil || owner.Plan.Emission == coro.EmitNone || plan.IgnoresBody(owner.Function) {
			continue
		}
		for _, block := range owner.Function.Blocks {
			for _, instruction := range block.Instrs {
				spawn, ok := instruction.(*ssa.Go)
				if !ok {
					continue
				}
				_, target, err := plan.ResolveClosedStaticSpawn(spawn)
				if err != nil {
					return fmt.Errorf("validate coroutine closed static spawn in %q: %w", owner.Plan.ID, err)
				}
				effect := target.Effect.Normalize()
				if !effect.Contains(coro.YieldOnly) || effect&^allowed != 0 {
					return fmt.Errorf(
						"validate coroutine closed static spawn in %q: target %q effect %s is outside the production main-return cancellation subset %s",
						owner.Plan.ID, target.ID, effect, allowed,
					)
				}
			}
		}
	}
	return nil
}

func sameExactCoroFunctionReferences(left, right []*ssa.Function) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[*ssa.Function]int, len(left))
	for _, fn := range left {
		if fn == nil || counts[fn] != 0 {
			return false
		}
		counts[fn] = 1
	}
	for _, fn := range right {
		if fn == nil || counts[fn] != 1 {
			return false
		}
		counts[fn] = 0
	}
	return true
}

func sameExactCoroLoweredCalls(left, right []coro.SSALoweredCall) bool {
	if len(left) != len(right) {
		return false
	}
	type exactLoweredCall struct {
		target     *ssa.Function
		unwindOnly bool
	}
	byName := make(map[string]exactLoweredCall, len(left))
	for _, call := range left {
		if call.LogicalName == "" || call.Target == nil {
			return false
		}
		if _, duplicate := byName[call.LogicalName]; duplicate {
			return false
		}
		byName[call.LogicalName] = exactLoweredCall{target: call.Target, unwindOnly: call.UnwindOnly}
	}
	for _, call := range right {
		frozen, ok := byName[call.LogicalName]
		if call.LogicalName == "" || call.Target == nil || !ok || frozen.target != call.Target || frozen.unwindOnly != call.UnwindOnly {
			return false
		}
		delete(byName, call.LogicalName)
	}
	return len(byName) == 0
}

// frontendElidesNoInitCall mirrors cl.context.funcKind: the frontend emits no
// call for the synthetic zero-argument init of a noinit/decl package. Treating
// this as an unresolved managed call would invent an OpaqueSuspend edge that
// cannot exist in the generated program.
func frontendElidesNoInitCall(call ssa.CallInstruction) bool {
	return cl.FrontendElidesNoInitCall(call)
}

func validateRequiredCoroDirectPlainCallArguments(plan *coro.SSAPlan, uses []requiredCoroDirectPlainCallArgument) error {
	if len(uses) == 0 {
		return nil
	}
	if plan == nil {
		return fmt.Errorf("compiler runtime direct-plain callback validation requires a coroutine plan")
	}
	for index, use := range uses {
		if use.call == nil || use.call.Common() == nil || use.argument < 0 || use.argument >= len(use.call.Common().Args) || use.target == nil {
			return fmt.Errorf("compiler runtime direct-plain callback %d is malformed", index)
		}
		function, ok := plan.FunctionPlan(use.target)
		if !ok {
			return fmt.Errorf("compiler runtime direct-plain callback %q has no function plan", use.target.Name())
		}
		if function.External != coro.Defined || function.Effect != coro.NoSuspend || function.Exec.Contains(coro.NeedsPreempt) ||
			function.FuncRep != coro.DirectPlain || function.Primary != coro.PrimaryPlain || function.Emission != coro.EmitPlain {
			return fmt.Errorf("compiler runtime direct-plain callback %q is not a defined closed singleton with one non-suspending plain body (external=%s effect=%s exec=%s representation=%s primary=%s emission=%s)",
				use.target.Name(), function.External, function.Effect, function.Exec, function.FuncRep, function.Primary, function.Emission)
		}
		targetID, ok := plan.FunctionID(use.target)
		if !ok {
			return fmt.Errorf("compiler runtime direct-plain callback %q has no FunctionID", use.target.Name())
		}
		argument := use.call.Common().Args[use.argument]
		value, ok := plan.ValuePlan(argument)
		if !ok || len(value.Funcs) != 1 || len(value.Funcs[0].Path) != 0 || value.Funcs[0].Rep != coro.DirectPlain ||
			value.Funcs[0].MayBeNil || len(value.Funcs[0].Targets) != 1 || value.Funcs[0].Targets[0] != targetID {
			return fmt.Errorf("compiler runtime direct-plain callback argument %d for %q is not an exact non-nil direct-plain singleton", use.argument, use.target.Name())
		}
	}
	return nil
}

// CoroPlanBuilder builds one compilation-scoped coroutine plan after every SSA
// package is available and the effective emission universe is frozen, but
// before fingerprinting, cache lookup, or LLVM codegen. The builder owns root
// and policy selection because directive and ABI classification are not build
// defaults yet. By default the build pipeline only stores the returned
// report-only plan; EnableCoroEntryResolution must be set explicitly before cl
// may consume its primary-symbol decisions. An active builder must return a
// plan created by input.Analyze so patch aliases and frontend structural
// identities cannot be bypassed. Active entry resolution uses archive-ready
// identities and fingerprints its canonical CoroPlanDigest into every package.
type CoroPlanBuilder func(input CoroPlanInput) (*coro.SSAPlan, error)

// CoroPlanObserver observes the same compilation-scoped plan from each cl
// package that is actually processed from source. Cached package registration
// does not invoke it.
type CoroPlanObserver = cl.CoroPlanObserver

type Config struct {
	Goos          string
	Goarch        string
	Target        string // target name (e.g., "rp2040", "wasi") - takes precedence over Goos/Goarch
	OptLevel      optlevel.Level
	LTO           lto.Mode
	LTOPlugin     lto.PassPlugin
	BinPath       string
	AppExt        string  // ".exe" on Windows, empty on Unix
	OutFile       string  // only valid for ModeBuild when len(pkgs) == 1
	OutFmts       OutFmts // Output format specifications (only for Target != "")
	CompileOnly   bool    // compile test binary but do not run it (only valid for ModeTest)
	Emulator      bool    // run in emulator mode
	Port          string  // target port for flashing
	BaudRate      int     // baudrate for serial communication
	RunArgs       []string
	Mode          Mode
	BuildMode     BuildMode // Build mode: exe, c-archive, c-shared
	AbiMode       AbiMode
	GenExpect     bool // only valid for ModeCmpTest
	Verbose       bool
	PrintCommands bool
	GenLL         bool // generate pkg .ll files
	CheckLLFiles  bool // check .ll files valid
	CheckLinkArgs bool // check linkargs valid
	ForceEspClang bool // force to use esp-clang
	ForceRebuild  bool // force rebuilding of packages that are already up-to-date
	Tags          string
	SizeReport    bool   // print size report after successful build
	SizeFormat    string // size report format: text,json (default text)
	SizeLevel     string // size aggregation level: full,module,package (default module)
	CompilerHash  string // metadata hash for the running compiler (development builds only)
	GoVersion     string // Go language version accepted by the frontend (for example, "go1.22")
	NoErrorColumn bool   // omit source columns from frontend diagnostics
	GoBuildFlags  []string
	AllowNoBody   bool // allow declarations without bodies, as go tool compile does

	// PthreadStackSize sets a custom stack size, in bytes, for pthread-backed
	// goroutines. A zero value keeps the platform pthread default.
	PthreadStackSize int64

	// DisableGoGlobalDCE disables Go-specific global DCE metadata emission
	// when it would otherwise be enabled by full LTO.
	DisableGoGlobalDCE bool

	// GlobalRewrites specifies compile-time overrides for global string variables.
	// Keys are fully qualified package paths (e.g. "main" or "github.com/user/pkg").
	// Each Rewrites entry maps variable names to replacement string values. Only
	// string-typed globals are supported and "main" applies to all root main
	// packages in the current build.
	GlobalRewrites map[string]Rewrites
	ModuleHook     ModuleHook

	// EnableCoroEntryResolution explicitly allows cl to consume the
	// compilation-scoped plan for primary-symbol validation. It does not enable
	// physical coroutine ABI or scheduler lowering. It requires CoroPlanBuilder;
	// leaving it false preserves report-only behavior. Package archives are
	// reused only when their complete plan/ABI/target fingerprint matches.
	EnableCoroEntryResolution bool
	// EnableCoroExplicitStatusPanicABI selects the reserved target-wide
	// explicit-status panic identity. Hidden outcomes, cleanup edges, and the
	// runtime protocol are not implemented by this slice; active builds select
	// the identity for validation and then fail closed before code generation.
	EnableCoroExplicitStatusPanicABI bool
	// EnableCoroPhysicalABI enables the experimental LLVM coroutine physical ABI.
	// It requires EnableCoroEntryResolution and remains leaf-only unless a more
	// specific lowering capability is enabled.
	EnableCoroPhysicalABI bool
	// EnableCoroChildAwait enables the first scheduler handoff slice: a physical
	// coroutine may await a statically resolved coroutine child, and an explicit
	// async root receives a typed factory descriptor. It requires the physical
	// ABI and does not enable a runtime scheduler, spawn, park, or preemption.
	EnableCoroChildAwait bool
	// EnableCoroPlainDispatch enables the v1 descriptor/context ABI for the
	// narrowly supported ordinary call of a no-capture, non-suspending plain Go
	// function value. It requires entry resolution and does not authorize
	// coroutine, interface, reflect, method, go/defer, aggregate, or captured
	// closure dispatch.
	EnableCoroPlainDispatch bool
	// EnableCoroClosedStaticSpawn enables only an exact source `go f(args)`
	// whose operand is one closed, top-level static function. This first
	// capability accepts only zero-result targets; that is a lowering gate, not
	// a Go language restriction. It requires the runnable program-bootstrap v2
	// scheduler (including the v1 physical/child-await ABI) and never gives the
	// runtime a user callback.
	EnableCoroClosedStaticSpawn bool
	// EnableCoroProgramBootstrapABI emits the target-neutral v1 startup table
	// for an executable after the exact init/main entries have been validated
	// against the frozen whole-program plan. It does not replace the legacy
	// direct calls from the platform entry yet. This capability is deliberately
	// gated separately and requires entry resolution, the physical ABI, and
	// child-await lowering.
	EnableCoroProgramBootstrapABI bool
	// EnableCoroProgramBootstrapRun activates the production v1 bootstrap
	// driver. It requires EnableCoroProgramBootstrapABI, emits a compiler-owned
	// LLVM coroutine factory, and replaces only the legacy init/main calls in
	// the platform entry. This is also the first scheduler ABI that accepts
	// NeedsPreempt and emits conditional poll/yield handoffs. Keeping it separate
	// preserves the descriptor-only ABI gate as an independently testable and
	// reversible boundary.
	EnableCoroProgramBootstrapRun bool
	CoroPlanBuilder               CoroPlanBuilder
	CoroPlanObserver              CoroPlanObserver

	// compilerBuildTags is a compiler-owned channel for isolated runtime-island
	// builds that deliberately do not enable the complete program-bootstrap
	// configuration. It is not a target capability declaration and production
	// target selection must never derive from it. Keeping it unexported prevents
	// users and named-target BuildTags from forging compiler/runtime ABI choices.
	compilerBuildTags []string
}

type Rewrites map[string]string

func NewDefaultConf(mode Mode) *Config {
	bin := os.Getenv("GOBIN")
	if bin == "" {
		gopath, err := envGOPATH()
		if err != nil {
			panic(fmt.Errorf("cannot get GOPATH: %v", err))
		}
		bin = filepath.Join(gopath, "bin")
	}
	if err := os.MkdirAll(bin, 0755); err != nil {
		panic(fmt.Errorf("cannot create bin directory: %v", err))
	}
	goos, goarch := os.Getenv("GOOS"), os.Getenv("GOARCH")
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	conf := &Config{
		Goos:      goos,
		Goarch:    goarch,
		BinPath:   bin,
		Mode:      mode,
		BuildMode: BuildModeExe,
		AbiMode:   cabi.ModeAllFunc,
	}
	return conf
}

func envGOPATH() (string, error) {
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return gopath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "go"), nil
}

func (c *Config) ltoMode() lto.Mode {
	if c == nil {
		return lto.Off
	}
	return c.LTO
}

func (c *Config) ltoEnabled() bool {
	return c.ltoMode().Enabled()
}

func (c *Config) goGlobalDCEEnabled() bool {
	if c == nil {
		return false
	}
	return buildenv.Dev && c.ltoMode() == lto.Full && !c.DisableGoGlobalDCE
}

// -----------------------------------------------------------------------------

const (
	loadFiles   = packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles
	loadImports = loadFiles | packages.NeedImports
	loadTypes   = loadImports | packages.NeedTypes | packages.NeedTypesSizes
	loadSyntax  = loadTypes | packages.NeedSyntax | packages.NeedTypesInfo
)

func Do(args []string, conf *Config) ([]Package, error) {
	if conf.Goos == "" {
		conf.Goos = runtime.GOOS
	}
	if conf.Goarch == "" {
		conf.Goarch = runtime.GOARCH
	}
	if conf.AppExt == "" {
		conf.AppExt = defaultAppExt(conf)
	}
	if conf.BuildMode == "" {
		conf.BuildMode = BuildModeExe
	}
	if conf.SizeReport && conf.SizeFormat == "" {
		conf.SizeFormat = "text"
	}
	if conf.SizeReport && conf.SizeLevel == "" {
		conf.SizeLevel = "module"
	}
	if err := ensureSizeReporting(conf); err != nil {
		return nil, err
	}
	applyFrontendGCFlags(conf)
	conf.OptLevel = effectiveOptLevel(conf)
	// Handle crosscompile configuration first to set correct GOOS/GOARCH
	forceEspClang := conf.ForceEspClang || conf.Target != ""
	export, err := crosscompile.Use(conf.Goos, conf.Goarch, conf.Target, IsWasiThreadsEnabled(), forceEspClang, conf.OptLevel, conf.ltoMode(), conf.goGlobalDCEEnabled())
	if err != nil {
		return nil, fmt.Errorf("failed to setup crosscompile: %w", err)
	}
	// Update GOOS/GOARCH from export if target was used
	if conf.Target != "" && export.GOOS != "" {
		conf.Goos = export.GOOS
	}
	if conf.Target != "" && export.GOARCH != "" {
		conf.Goarch = export.GOARCH
	}

	// Enable different export names for TinyGo compatibility when using -target
	if conf.Target != "" {
		cl.EnableExportRename(true)
	}

	verbose := conf.Verbose
	patterns := args
	tags, err := effectiveBuildTags(conf, export)
	if err != nil {
		return nil, err
	}
	goBuildFlags := []string{"-tags=" + tags}
	_, otherGoBuildFlags := partitionGoBuildFlags(conf.GoBuildFlags)
	goBuildFlags = append(goBuildFlags, otherGoBuildFlags...)
	cfg := &packages.Config{
		Mode:       loadSyntax | packages.NeedDeps | packages.NeedModule | packages.NeedExportFile,
		BuildFlags: goBuildFlags,
		Fset:       token.NewFileSet(),
		Tests:      conf.Mode == ModeTest,
		Env:        append(slices.Clone(os.Environ()), "GOOS="+conf.Goos, "GOARCH="+conf.Goarch),
	}
	if conf.Mode == ModeTest {
		cfg.Mode |= packages.NeedForTest
	}

	cl.EnableDebug(IsDbgEnabled())
	cl.EnableDbgSyms(IsDbgSymsEnabled())
	cl.EnableTrace(IsTraceEnabled())
	llssa.Initialize(llssa.InitAll)

	target := newLLSSATarget(conf, export)

	prog := llssa.NewProgram(target)
	programOwnershipTransferred := false
	defer func() {
		if !programOwnershipTransferred {
			prog.Dispose()
		}
	}()
	prog.EnableGoGlobalDCE(conf.goGlobalDCEEnabled())
	if conf.PthreadStackSize > 0 {
		prog.SetPthreadStackSize(uint64(conf.PthreadStackSize))
	}
	prog.EnableLTOPluginMarkers(conf.LTOPlugin.Enabled())
	funcInfo := conf.Mode != ModeGen && IsFuncInfoEnabled()
	prog.EnableFuncInfoMetadata(funcInfo)
	// Site records are inline-asm fragments inside function bodies; their
	// anchors shift instruction/scope layout enough to confuse debuggers
	// (LLDB reported variables from an inner lexical block as in scope before
	// the block began). Debug builds keep the metadata tables — FuncForPC
	// name/FileLine fidelity survives via the dlsym path — but drop the
	// sites. Caller-frame instrumentation is independent of both switches,
	// so runtime.Caller keeps working in debug builds.
	prog.EnableFuncInfoSites(funcInfo && !IsDbgEnabled() && IsFuncInfoSitesEnabled())
	sizes := func(sizes types.Sizes, compiler, arch string) types.Sizes {
		if arch == "wasm" {
			sizes = &types.StdSizes{WordSize: 4, MaxAlign: 4}
		}
		return prog.TypeSizes(sizes)
	}
	dedup := packages.NewDeduper()
	dedup.SetPreload(func(pkg *types.Package, files []*ast.File) {
		if llruntime.SkipToBuild(pkg.Path()) {
			return
		}
		cl.ParsePkgSyntax(prog, pkg, files)
	})

	if patterns == nil {
		patterns = []string{"."}
	}
	sourcePatchGOROOT, sourcePatchGoVersion, err := env.GOROOTAndGOVERSIONWithEnv(cfg.Env)
	if err != nil {
		return nil, err
	}
	cfg.Overlay, err = buildSourcePatchOverlayForGOROOT(cfg.Overlay, env.LLGoRuntimeDir(), sourcePatchGOROOT, sourcePatchBuildContext{
		goos:       conf.Goos,
		goarch:     conf.Goarch,
		goversion:  sourcePatchGoVersion,
		buildFlags: cfg.BuildFlags,
	})
	if err != nil {
		return nil, err
	}
	initial, err := packages.LoadExWithGoVersion(dedup, sizes, cfg, conf.GoVersion, patterns...)
	if err != nil {
		return nil, err
	}
	if conf.AllowNoBody {
		allowMissingFunctionBodies(initial)
	}
	mode := conf.Mode
	if mode == ModeTest {
		initial, err = filterTestPackages(initial, conf.OutFile)
		if err != nil {
			return nil, err
		}
		if len(initial) == 0 {
			return nil, nil
		}
	} else if len(initial) > 1 {
		switch mode {
		case ModeBuild:
			if conf.OutFile != "" {
				return nil, fmt.Errorf("cannot build multiple packages with -o")
			}
		case ModeInstall:
			if conf.Target != "" {
				return nil, fmt.Errorf("cannot install multiple packages to embedded target")
			}
		case ModeRun:
			return nil, fmt.Errorf("cannot run multiple packages")
		}
	}

	altPkgPaths := altPkgs(initial, conf, llssa.PkgRuntime)
	altCfg := *cfg
	altCfg.Dir = env.LLGoRuntimeDir()
	altPkgs, err := packages.LoadEx(dedup, sizes, &altCfg, altPkgPaths...)
	if err != nil {
		return nil, err
	}

	prog.SetRuntime(func() *types.Package {
		return altPkgs[0].Types
	})
	prog.SetPython(func() *types.Package {
		return dedup.Check(llssa.PkgPython).Types
	})
	preCollectRuntimeLinknames(prog, altPkgs)

	buildMode := ssaBuildMode
	cabiOptimize := true
	passOpt := true
	if IsDbgEnabled() || mode == ModeGen {
		passOpt = false
	}
	if IsDbgEnabled() {
		buildMode |= ssa.GlobalDebug
		cabiOptimize = false
	}
	if !IsOptimizeEnabled() {
		buildMode |= ssa.NaiveForm
	}
	progSSA := ssa.NewProgram(initial[0].Fset, buildMode)
	patches := make(cl.Patches, len(altPkgPaths))
	altSSAPkgs(progSSA, patches, altPkgs[1:], conf, verbose)

	env := llvm.New("")
	os.Setenv("PATH", env.BinDir()+":"+os.Getenv("PATH")) // TODO(xsw): check windows

	output := conf.OutFile != ""
	ctx := &context{env: env, conf: cfg, progSSA: progSSA, prog: prog, dedup: dedup,
		patches: patches, callerTracking: cl.NewCallerTracking(),
		built: make(map[string]none), initial: initial, mode: mode,
		fingerprinting: make(map[string]bool),
		pkgs:           map[*packages.Package]Package{},
		pkgByID:        map[string]Package{},
		output:         output,
		passOpt:        passOpt,
		buildConf:      conf,
		crossCompile:   export,
		cTransformer:   cabi.NewTransformer(prog, export.LLVMTarget, export.TargetABI, conf.AbiMode, cabiOptimize),
	}

	// default runtime globals must be registered before packages are built
	addGlobalString(conf, "runtime.defaultGOROOT="+runtime.GOROOT(), nil)
	addGlobalString(conf, "runtime.buildVersion="+runtime.Version(), nil)
	pkgs, err := buildSSAPkgs(ctx, initial, verbose)
	if err != nil {
		return nil, err
	}
	depPkgs, err := buildSSAPkgs(ctx, altPkgs, verbose)
	if err != nil {
		return nil, err
	}

	allPkgs := append([]*aPackage{}, pkgs...)
	allPkgs = append(allPkgs, depPkgs...)
	if err := buildCoroPlan(ctx, allPkgs...); err != nil {
		return nil, err
	}
	allPkgs, err = buildAllPkgs(ctx, allPkgs, verbose)
	if err != nil {
		return nil, err
	}

	if mode == ModeGen {
		for _, pkg := range allPkgs {
			if pkg.Package == initial[0] {
				if pkg.LPkg == nil || pkg.LPkg.Prog != prog {
					return nil, fmt.Errorf("generated package has no owned LLVM program")
				}
				// ModeGen callers (llgen and the golden suites) read LPkg.String()
				// after Do returns and dispose the shared program themselves. Error
				// paths retain ownership here so early analysis failures do not leak
				// the LLVM context, target machine, or target data.
				programOwnershipTransferred = true
				return []*aPackage{pkg}, nil
			}
		}
		return nil, fmt.Errorf("initial package not found")
	}

	for _, pkg := range initial {
		if needLink(pkg, mode) {
			name := path.Base(pkg.PkgPath)

			// Create output format details
			outFmts, err := buildOutFmts(name, conf, len(ctx.initial) > 1, &ctx.crossCompile)
			if err != nil {
				return nil, err
			}

			// Link main package using the output path from buildOutFmts
			err = linkMainPkg(ctx, pkg, allPkgs, outFmts.Out, verbose)
			if err != nil {
				return nil, err
			}
			rewritePrebuiltFuncTab(ctx, outFmts.Out, verbose)
			if conf.Mode == ModeBuild && conf.SizeReport {
				if err := reportBinarySize(outFmts.Out, conf.SizeFormat, conf.SizeLevel, allPkgs); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: size report failed: %v\n", err)
				}
			}

			// Generate C headers for c-archive and c-shared modes before linking
			if ctx.buildConf.BuildMode == BuildModeCArchive || ctx.buildConf.BuildMode == BuildModeCShared {
				libname := strings.TrimSuffix(filepath.Base(outFmts.Out), conf.AppExt)
				headerPath := filepath.Join(filepath.Dir(outFmts.Out), libname) + ".h"
				pkgs := make([]llssa.Package, 0, len(allPkgs))
				for _, p := range allPkgs {
					if p.LPkg != nil {
						pkgs = append(pkgs, p.LPkg)
					}
				}
				headerErr := header.GenHeaderFile(prog, pkgs, libname, headerPath, verbose)
				if headerErr != nil {
					return nil, headerErr
				}
				continue
			}

			envMap := outFmts.ToEnvMap()

			// Only convert formats when Target is specified
			if conf.Target != "" {
				// Process format conversions for embedded targets
				err = firmware.ConvertFormats(ctx.crossCompile.BinaryFormat, ctx.crossCompile.FormatDetail, envMap)
				if err != nil {
					return nil, err
				}
			}

			switch mode {
			case ModeBuild:
				// Do nothing

			case ModeInstall:
				// Native already installed in linkMainPkg
				if conf.Target != "" {
					err = flash.FlashDevice(ctx.crossCompile.Device, envMap, ctx.buildConf.Port, verbose)
					if err != nil {
						return nil, err
					}
				}

			case ModeRun, ModeTest, ModeCmpTest:
				if conf.Target == "" {
					err = runNative(ctx, outFmts.Out, pkg.Dir, pkg.PkgPath, conf, mode)
				} else if conf.Emulator {
					err = runInEmulator(ctx.crossCompile.Emulator, envMap, pkg.Dir, pkg.PkgPath, conf, mode, verbose)
				} else {
					err = flash.FlashDevice(ctx.crossCompile.Device, envMap, ctx.buildConf.Port, verbose)
					if err != nil {
						return nil, err
					}
					monitorConfig := monitor.MonitorConfig{
						Port:       ctx.buildConf.Port,
						Target:     conf.Target,
						Executable: outFmts.Out,
						BaudRate:   conf.BaudRate,
						SerialPort: ctx.crossCompile.Device.SerialPort,
					}
					err = monitor.Monitor(monitorConfig, verbose)
				}
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if mode == ModeTest && ctx.testFail {
		mockable.Exit(1)
	}

	return allPkgs, nil
}

func targetGCBuildTags(gc string) ([]string, error) {
	switch gc {
	case "", "precise", "conservative":
		return nil, nil
	case "leaking", "none":
		return []string{"nogc"}, nil
	default:
		return nil, fmt.Errorf("unsupported target GC capability %q", gc)
	}
}

const (
	coroNativePipeBuildTag        = "llgo_coro_native_pipe"
	coroNativeTimerBuildTag       = "llgo_coro_native_timer"
	coroNativeIngressTestBuildTag = "llgo_coro_native_ingress_test"
)

// effectiveBuildTags is the single build-tag assembly boundary used by Do.
// Native coroutine capability tags are compiler/runtime ABI choices, not user
// or target customizations: accepting one from an external tag source could
// select a runtime body that disagrees with the planner roots, bootstrap hash,
// entry relocation anchor, or focused test harness.
func effectiveBuildTags(conf *Config, export crosscompile.Export) (string, error) {
	if conf == nil {
		return "", fmt.Errorf("assemble build tags: missing build configuration")
	}
	if err := rejectCompilerReservedBuildTags("Config.Tags", splitSourcePatchBuildTags(conf.Tags)); err != nil {
		return "", err
	}
	goFlagTags := parseSourcePatchBuildTags(conf.GoBuildFlags)
	if err := rejectCompilerReservedBuildTags("Config.GoBuildFlags", goFlagTags); err != nil {
		return "", err
	}
	var targetTags []string
	for _, value := range export.BuildTags {
		targetTags = append(targetTags, splitSourcePatchBuildTags(value)...)
	}
	if err := rejectCompilerReservedBuildTags("named-target BuildTags", targetTags); err != nil {
		return "", err
	}

	tags := []string{"llgo", "math_big_pure_go", "purego"}
	if conf.AbiMode == cabi.ModeAllFunc {
		tags = append(tags, "llgo_abi_2")
	}
	if conf.EnableCoroProgramBootstrapRun {
		// The stackless runtime does not yet have a RawCritical bridge that can
		// turn a synchronous hardware fault into a G-owned panic completion.
		// Exclude the legacy pthread-TLS/SJLJ SIGSEGV recovery hook instead of
		// admitting a signal callback that can allocate, block, or retain the
		// native signal stack. Language-level nil/bounds/divide checks remain
		// explicit compiler operations.
		tags = append(tags, "llgo_coro")
		if nativeCoroDoorbellRuntimeABI(conf) {
			// Do not infer POSIX capability from GOOS alone. Several embedded
			// named targets reuse linux source selection without providing a
			// process pipe/poll environment.
			tags = append(tags, coroNativePipeBuildTag)
		}
		if nativeCoroTimerRuntimeABI(conf) {
			// The first clock ABI is intentionally restricted to native
			// 64-bit POSIX targets. A separate compiler-owned tag keeps a
			// 32-bit pipe backend from silently selecting an unverified libc
			// timespec/time64 layout.
			tags = append(tags, coroNativeTimerBuildTag)
		}
	}
	tags = append(tags, conf.compilerBuildTags...)
	gcTags, err := targetGCBuildTags(export.GC)
	if err != nil {
		return "", err
	}
	tags = append(tags, gcTags...)
	tags = append(tags, splitSourcePatchBuildTags(conf.Tags)...)
	tags = append(tags, goFlagTags...)
	tags = append(tags, targetTags...)
	return strings.Join(tags, ","), nil
}

func rejectCompilerReservedBuildTags(source string, tags []string) error {
	for _, tag := range tags {
		switch tag {
		case coroNativePipeBuildTag, coroNativeTimerBuildTag, coroNativeIngressTestBuildTag:
			return fmt.Errorf("build tag %q from %s is a compiler-reserved capability and cannot be supplied externally", tag, source)
		}
	}
	return nil
}

func buildCoroPlan(ctx *context, packages ...*aPackage) error {
	if ctx == nil || ctx.buildConf == nil {
		return nil
	}
	if ctx.buildConf.EnableCoroClosedStaticSpawn {
		if !ctx.buildConf.EnableCoroProgramBootstrapRun {
			return fmt.Errorf("enable coroutine closed static spawn: runnable program bootstrap v2 is required")
		}
		if !ctx.buildConf.EnableCoroChildAwait {
			return fmt.Errorf("enable coroutine closed static spawn: coroutine child await is required")
		}
	}
	if err := validateCoroProgramBootstrapConfig(ctx.buildConf); err != nil {
		return err
	}
	ctx.coroProgramBootstraps = nil
	if ctx.buildConf.EnableCoroPhysicalABI && !ctx.buildConf.EnableCoroEntryResolution {
		return fmt.Errorf("enable coroutine physical ABI: coroutine entry resolution is required")
	}
	if ctx.buildConf.EnableCoroChildAwait && !ctx.buildConf.EnableCoroPhysicalABI {
		return fmt.Errorf("enable coroutine child await: coroutine physical ABI is required")
	}
	if ctx.buildConf.EnableCoroPlainDispatch && !ctx.buildConf.EnableCoroEntryResolution {
		return fmt.Errorf("enable coroutine plain dispatch: coroutine entry resolution is required")
	}
	if ctx.buildConf.EnableCoroExplicitStatusPanicABI && !ctx.buildConf.EnableCoroEntryResolution {
		return fmt.Errorf("enable coroutine explicit-status panic ABI: coroutine entry resolution is required")
	}
	if ctx.buildConf.EnableCoroChildAwait && ctx.buildConf.BuildMode == BuildModeCArchive {
		return fmt.Errorf("enable coroutine child await: c-archive requires flattened package members and an explicit host bootstrap extraction contract")
	}
	builder := ctx.buildConf.CoroPlanBuilder
	if builder == nil {
		if ctx.buildConf.EnableCoroEntryResolution {
			return fmt.Errorf("enable coroutine entry resolution: CoroPlanBuilder is required")
		}
		return nil
	}
	if len(packages) != 0 {
		if err := prepareCoroEmissionUniverse(ctx, packages); err != nil {
			return fmt.Errorf("prepare coroutine emission universe: %w", err)
		}
	}
	if ctx.buildConf.EnableCoroEntryResolution && ctx.coroEmission == nil {
		return fmt.Errorf("enable coroutine entry resolution: prepared emission universe is required")
	}
	analyzedPlans := make(map[*coro.SSAPlan]struct{})
	var analyzedPlansMu sync.Mutex
	var requiredRoots coro.Roots
	var requiredPlain map[*ssa.Function]struct{}
	var requiredDirectPlain []requiredCoroDirectPlainCallArgument
	var requiredClosedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate
	if ctx.coroEmission != nil && ctx.coroEmission.CompleteRuntimeABI() {
		// Compiler-owned runtime edges belong only to a frozen whole-program
		// universe containing the exact LLGo runtime package. Isolated frontend
		// fixtures and report universes intentionally remain incomplete; making
		// them resolve production runtime roots would guess bodies outside their
		// declared emission universe. Real Do builds pass all packages above and
		// therefore retain the fail-closed complete-runtime path.
		var err error
		requiredRoots, requiredPlain, requiredDirectPlain, requiredClosedDynamic, err = requiredCoroProgramRuntimePlan(ctx)
		if err != nil {
			return err
		}
	}
	managedEntryRoots, err := requiredCoroProgramManagedEntryRoots(ctx)
	if err != nil {
		return err
	}
	requiredRoots = append(requiredRoots, managedEntryRoots...)
	input := CoroPlanInput{
		Program:                 ctx.progSSA,
		requiredRoots:           requiredRoots,
		requiredPlain:           requiredPlain,
		requiredDirectPlain:     requiredDirectPlain,
		requiredClosedDynamic:   requiredClosedDynamic,
		enableClosedStaticSpawn: ctx.buildConf.EnableCoroClosedStaticSpawn,
		recordAnalysis: func(plan *coro.SSAPlan) {
			if plan != nil {
				analyzedPlansMu.Lock()
				analyzedPlans[plan] = struct{}{}
				analyzedPlansMu.Unlock()
			}
		},
	}
	if ctx.coroEmission != nil {
		input.EmissionUniverse = ctx.coroSSAEmission
		input.resolveFunction = ctx.coroEmission.Resolve
		input.functionBackground = ctx.coroEmission.FunctionBackground
		input.foreignNoBlock = ctx.coroEmission.CoroForeignNoBlockCertificate
		input.intrinsicCallSemantics = ctx.coroEmission.CoroIntrinsicCallSiteSemantics
		input.rawFunctionAddressCallArgument = ctx.coroEmission.CoroRawFunctionAddressCallArgument
		input.demandReferences = ctx.coroEmission.CoroDemandReferences
		input.loweredCalls = ctx.coroEmission.CoroLoweredCalls
		input.augmentFunctionIDs = func(config coro.FunctionIDConfig) coro.FunctionIDConfig {
			if ctx.buildConf.EnableCoroEntryResolution {
				if config.CoroABI == "" {
					config.CoroABI = activeCoroABIVersion(ctx.buildConf)
				}
				if config.SchedulerABI == "" {
					config.SchedulerABI = activeCoroSchedulerABIVersion(ctx.buildConf)
				}
				config.ArchiveReady = true
			}
			return ctx.coroEmission.AugmentFunctionIDConfig(config)
		}
	}
	plan, err := builder(input)
	if err != nil {
		return fmt.Errorf("build coroutine plan: %w", err)
	}
	if plan == nil {
		return fmt.Errorf("build coroutine plan: builder returned nil plan")
	}
	if ctx.buildConf.EnableCoroEntryResolution {
		analyzedPlansMu.Lock()
		if _, ok := analyzedPlans[plan]; !ok {
			analyzedPlansMu.Unlock()
			return fmt.Errorf("validate coroutine plan: active entry resolution requires the builder to return a plan created by CoroPlanInput.Analyze")
		}
		analyzedPlansMu.Unlock()
		if err := ctx.coroEmission.ValidateCoroPlan(plan); err != nil {
			return fmt.Errorf("validate coroutine plan coverage: %w", err)
		}
	}
	if err := validateCoroClosedStaticSpawnRunGate(ctx.buildConf, plan); err != nil {
		return err
	}
	var metadata coro.PlanDigestMetadata
	var digest string
	if ctx.buildConf.EnableCoroEntryResolution {
		metadata, err = buildCoroPlanDigestMetadata(ctx)
		if err != nil {
			return fmt.Errorf("build coroutine plan digest metadata: %w", err)
		}
		digest, err = plan.CoroPlanDigest(metadata)
		if err != nil {
			return fmt.Errorf("build coroutine plan digest: %w", err)
		}
	}
	ctx.coroPlan = plan
	ctx.coroPlanDigest = digest
	ctx.coroPlanMetadata = metadata
	ctx.clCompilation = &cl.Compilation{
		CoroPlan:                         plan,
		CoroPlanObserver:                 ctx.buildConf.CoroPlanObserver,
		EnableCoroEntryResolution:        ctx.buildConf.EnableCoroEntryResolution,
		EnableCoroExplicitStatusPanicABI: ctx.buildConf.EnableCoroExplicitStatusPanicABI,
		EnableCoroPhysicalABI:            ctx.buildConf.EnableCoroPhysicalABI,
		EnableCoroChildAwait:             ctx.buildConf.EnableCoroChildAwait,
		EnableCoroPlainDispatch:          ctx.buildConf.EnableCoroPlainDispatch,
		EnableCoroClosedStaticSpawn:      ctx.buildConf.EnableCoroClosedStaticSpawn,
		EnableCoroProgramBootstrapRun:    ctx.buildConf.EnableCoroProgramBootstrapRun,
		CoroPlanDigest:                   digest,
		CoroABI:                          metadata.CoroABI,
		SchedulerABI:                     metadata.SchedulerABI,
		PanicABI:                         metadata.PanicABI,
		FuncRepABI:                       metadata.FuncRepABI,
		EmissionUniverse:                 ctx.coroEmission,
	}
	if ctx.buildConf.EnableCoroExplicitStatusPanicABI {
		ctx.coroPlan = nil
		ctx.coroPlanDigest = ""
		ctx.coroPlanMetadata = coro.PlanDigestMetadata{}
		ctx.clCompilation = nil
		return fmt.Errorf("enable coroutine explicit-status panic ABI %q: identity-only capability; lowering and runtime semantics are not implemented", metadata.PanicABI)
	}
	if ctx.buildConf.EnableCoroProgramBootstrapABI {
		bootstraps, err := prepareCoroProgramBootstrapsV1(ctx)
		if err != nil {
			ctx.coroPlan = nil
			ctx.coroPlanDigest = ""
			ctx.coroPlanMetadata = coro.PlanDigestMetadata{}
			ctx.clCompilation = nil
			ctx.coroProgramBootstraps = nil
			return fmt.Errorf("prepare coroutine program bootstrap before codegen: %w", err)
		}
		ctx.coroProgramBootstraps = bootstraps
	}
	if ctx.buildConf.EnableCoroEntryResolution {
		if err := validateCoroUnwindOnlyLoweredCalls(plan, metadata.PanicABI); err != nil {
			ctx.coroPlan = nil
			ctx.coroPlanDigest = ""
			ctx.coroPlanMetadata = coro.PlanDigestMetadata{}
			ctx.clCompilation = nil
			ctx.coroProgramBootstraps = nil
			return fmt.Errorf("validate coroutine unwind-only lowered calls before codegen: %w", err)
		}
	}
	return nil
}

// validateCoroUnwindOnlyLoweredCalls preserves the legacy panic boundary's
// fail-closed physical contract. Unwind-only edges do not taint an owner's
// normal-return plan, but PanicLegacyABIV0 still emits a direct synchronous
// helper call. Until a panic ABI defines coroutine child unwind propagation,
// every physically emitted unwind-only target must therefore have one bounded
// plain primary body.
func validateCoroUnwindOnlyLoweredCalls(plan *coro.SSAPlan, panicABI string) error {
	if plan == nil {
		return fmt.Errorf("unwind-only lowered-call validation requires a coroutine plan")
	}
	for _, owner := range plan.Functions() {
		if owner.Function == nil || owner.Plan.Emission == coro.EmitNone {
			continue
		}
		for _, lowered := range plan.LoweredCalls(owner.Function) {
			if !lowered.UnwindOnly {
				continue
			}
			if panicABI != coro.PanicLegacyABIV0 {
				return fmt.Errorf("lowered call %q in %q is unwind-only, but panic ABI %q has no certified unwind-helper call contract", lowered.LogicalName, owner.Plan.ID, panicABI)
			}
			certificate := coroLegacyPanicPlainCertificate{
				owner:       owner.Function,
				logicalName: lowered.LogicalName,
				target:      lowered.Target,
			}
			if err := certificate.validate(plan); err != nil {
				return fmt.Errorf("unwind-only lowered call %q in %q cannot use its exact %s plain certificate: %w",
					lowered.LogicalName, owner.Plan.ID, panicABI, err)
			}
		}
	}
	return nil
}

// coroLegacyPanicPlainCertificate is deliberately an object-identity
// certificate, not a symbol-name exception. owner, logicalName, and target are
// copied from the immutable lowered-call table in SSAPlan. That table is frozen
// by the frontend which physically emits the helper call.
//
// CallUnwind prevents the panic episode from tainting the normal-return effect
// of owner, but it cannot make a coroutine target synchronously callable. The
// legacy ABI therefore also requires the exact target's physically reachable
// managed closure to contain only bounded DirectPlain calls. In particular, a
// terminal panic printer must not turn error.Error, Stringer.String, or another
// user callback into a trusted plain function merely because it is reachable
// only while panicking.
type coroLegacyPanicPlainCertificate struct {
	owner       *ssa.Function
	logicalName string
	target      *ssa.Function
}

func (certificate coroLegacyPanicPlainCertificate) validate(plan *coro.SSAPlan) error {
	if plan == nil || certificate.owner == nil || certificate.target == nil || certificate.logicalName == "" {
		return fmt.Errorf("legacy panic plain certificate is incomplete")
	}
	matched := false
	for _, lowered := range plan.LoweredCalls(certificate.owner) {
		if lowered.LogicalName == certificate.logicalName && lowered.Target == certificate.target && lowered.UnwindOnly {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("legacy panic plain certificate is not bound to an exact frozen unwind-only target")
	}
	targetPlan, planned := plan.FunctionPlan(certificate.target)
	if !planned {
		return fmt.Errorf("legacy panic plain certificate targets an unplanned function")
	}
	if targetPlan.External != coro.Defined {
		return fmt.Errorf("legacy panic plain certificate target %q is not a defined Go body (external=%s)", targetPlan.ID, targetPlan.External)
	}

	validator := coroLegacyPanicPlainClosureValidator{
		plan:      plan,
		validated: make(map[*ssa.Function]bool),
		active:    make(map[*ssa.Function]bool),
	}
	return validator.validateFunction(certificate.target, nil)
}

type coroLegacyPanicPlainClosureValidator struct {
	plan      *coro.SSAPlan
	validated map[*ssa.Function]bool
	active    map[*ssa.Function]bool
}

func (validator *coroLegacyPanicPlainClosureValidator) validateFunction(function *ssa.Function, path []string) error {
	functionPlan, ok := validator.plan.FunctionPlan(function)
	if !ok {
		return fmt.Errorf("legacy panic target closure contains an unplanned function")
	}
	path = append(path, fmt.Sprintf("%s[%s]", function.String(), functionPlan.ID))
	if validator.validated[function] {
		return nil
	}
	if validator.active[function] {
		// Recursion is diagnosed below by the fixed-point plan (YieldOnly /
		// NeedsPreempt). Avoid hiding that deterministic plan error behind a DFS
		// cycle diagnostic.
		return nil
	}
	validator.active[function] = true
	defer delete(validator.active, function)

	// Inspect the exact physical managed-call closure before reporting the
	// aggregate Effect on this function. This turns an opaque-suspend symptom on
	// runtime.Panic into the actionable dynamic edge which caused it. Foreign
	// leaves remain governed by their ordinary plan; this code never grants or
	// manufactures a foreign-noblock certificate.
	if !validator.plan.IgnoresBody(function) {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
					continue
				}
				if validator.plan.ElidesCall(call) {
					continue
				}
				callPlan, planned := validator.plan.CallPlan(call)
				if !planned {
					return coroLegacyPanicPlainPathError(path, "physical call %q has no coroutine call plan", call.String())
				}
				if callPlan.Kind == coro.CallSpawn {
					return coroLegacyPanicPlainPathError(path, "spawns asynchronous work at %q", call.String())
				}
				if callPlan.Open || callPlan.Rep == coro.Dispatch || call.Common().StaticCallee() == nil || len(callPlan.Targets) != 1 {
					kind := "dynamic call"
					if call.Common().IsInvoke() {
						kind = "dynamic invoke"
						if method := call.Common().Method; method != nil {
							kind += " " + method.Name()
						}
					}
					return coroLegacyPanicPlainPathError(path, "%s is not a bounded DirectPlain edge at %q", kind, call.String())
				}
				target, found := validator.plan.Function(callPlan.Targets[0])
				if !found || target == nil {
					return coroLegacyPanicPlainPathError(path, "physical call %q has an unresolved planned target", call.String())
				}
				if err := validator.validateFunction(target, path); err != nil {
					return err
				}
				if callPlan.Rep != coro.DirectPlain {
					return coroLegacyPanicPlainPathError(path, "physical call %q requires %s", call.String(), callPlan.Rep)
				}
			}
		}
		for _, lowered := range validator.plan.LoweredCalls(function) {
			if lowered.Target == nil {
				return coroLegacyPanicPlainPathError(path, "lowered call %q has no exact target", lowered.LogicalName)
			}
			if err := validator.validateFunction(lowered.Target, path); err != nil {
				return err
			}
		}
	}

	if functionPlan.External != coro.Defined {
		// A bodyless foreign declaration is a structural leaf, not a managed
		// callback to be pulled into this certificate. Its CallForeign edge still
		// contributes WaitForeign to the containing Go function in the ordinary
		// fixed point, so accepting it for DFS purposes cannot make that Go body
		// pass the bounded-plain check below. This merely lets the diagnostic reach
		// a more specific managed/dynamic blocker later in the same panic path.
		if functionPlan.Demand != coro.NoDemand && functionPlan.FuncRep == coro.DirectPlain &&
			functionPlan.Primary == coro.PrimaryExternal && functionPlan.Emission == coro.EmitExternal {
			validator.validated[function] = true
			return nil
		}
		return coroLegacyPanicPlainPathError(path,
			"foreign leaf has no direct physical entry (external=%s demand=%s effect=%s exec=%s representation=%s primary=%s emission=%s)",
			functionPlan.External, functionPlan.Demand, functionPlan.Effect, functionPlan.Exec, functionPlan.FuncRep, functionPlan.Primary, functionPlan.Emission)
	}
	if functionPlan.Demand == coro.NoDemand || functionPlan.Effect != coro.NoSuspend ||
		functionPlan.Emission != coro.EmitPlain || functionPlan.FuncRep != coro.DirectPlain || functionPlan.Primary != coro.PrimaryPlain {
		return coroLegacyPanicPlainPathError(path,
			"target is not one bounded plain Go body (external=%s demand=%s effect=%s exec=%s representation=%s primary=%s emission=%s)",
			functionPlan.External, functionPlan.Demand, functionPlan.Effect, functionPlan.Exec, functionPlan.FuncRep, functionPlan.Primary, functionPlan.Emission)
	}
	validator.validated[function] = true
	return nil
}

func coroLegacyPanicPlainPathError(path []string, format string, args ...any) error {
	return fmt.Errorf("legacy panic plain closure %s: %s", strings.Join(path, " -> "), fmt.Sprintf(format, args...))
}

func activeCoroABIVersion(conf *Config) string {
	if conf != nil && conf.EnableCoroChildAwait {
		return coro.PhysicalABIV1
	}
	if conf != nil && conf.EnableCoroPhysicalABI {
		return coro.PhysicalABIV0
	}
	return coro.EntryResolutionABIV0
}

// requiredCoroProgramManagedEntryRoots injects the exact main-package
// initializer and main body as managed async-capable roots for the runnable
// startup program. Duplicate builder roots are harmless: AnalyzeSSA joins
// demand by canonical function. Descriptor-only builds keep their historical
// explicit-root contract and legacy native entry.
func requiredCoroProgramManagedEntryRoots(ctx *context) (coro.Roots, error) {
	if ctx == nil || ctx.buildConf == nil || !ctx.buildConf.EnableCoroProgramBootstrapRun {
		return nil, nil
	}
	if ctx.coroEmission == nil {
		return nil, fmt.Errorf("coroutine managed program roots require a frozen emission universe")
	}
	publicRuntimeInit, hasPublicRuntimeInit, err := findCoroProgramFunction(ctx, "runtime", "init", "public runtime init")
	if err != nil {
		return nil, err
	}
	var roots coro.Roots
	if hasPublicRuntimeInit {
		roots = append(roots, coro.Root{Function: publicRuntimeInit, Demand: coro.AsyncDemand})
	}
	seenPackages := make(map[string]struct{})
	for _, pkg := range ctx.initial {
		if pkg == nil || !needLink(pkg, ctx.mode) {
			continue
		}
		if _, duplicate := seenPackages[pkg.ID]; duplicate {
			return nil, fmt.Errorf("coroutine managed program roots contain duplicate linked package ID %q", pkg.ID)
		}
		seenPackages[pkg.ID] = struct{}{}
		aPkg := ctx.pkgs[pkg]
		if aPkg == nil {
			aPkg = ctx.pkgByID[pkg.ID]
		}
		if aPkg == nil || aPkg.SSA == nil || aPkg.SSA.Pkg == nil || llssa.PathOf(aPkg.SSA.Pkg) != pkg.PkgPath {
			return nil, fmt.Errorf("coroutine managed program roots: linked main package %q has no exact SSA package", pkg.ID)
		}
		for _, name := range []string{"init", "main"} {
			original := aPkg.SSA.Func(name)
			if original == nil {
				return nil, fmt.Errorf("coroutine managed program root %s: exact SSA function is missing", name)
			}
			fn, ok := ctx.coroEmission.Resolve(original)
			if !ok || fn == nil || fn != original {
				return nil, fmt.Errorf("coroutine managed program root %s: exact function is absent from the frozen emission universe", name)
			}
			goBody, err := frozenGoEmittedBody(ctx.coroEmission, fn)
			if err != nil {
				return nil, fmt.Errorf("classify coroutine managed program root %s: %w", name, err)
			}
			if !goBody {
				return nil, fmt.Errorf("coroutine managed program root %s has no emitted Go body", name)
			}
			roots = append(roots, coro.Root{Function: fn, Demand: coro.AsyncDemand})
		}
	}
	return roots, nil
}

func activeCoroSchedulerABIVersion(conf *Config) string {
	if conf != nil && conf.EnableCoroClosedStaticSpawn {
		return coro.SchedulerProgramBootstrapClosedStaticSpawnABIV0
	}
	if conf != nil && conf.EnableCoroProgramBootstrapRun {
		return coro.SchedulerProgramBootstrapABIV2
	}
	if conf != nil && conf.EnableCoroChildAwait {
		return coro.SchedulerChildAwaitABIV0
	}
	return coro.SchedulerNoneABIV0
}

func activeCoroPanicABIVersion(conf *Config) string {
	if conf != nil && conf.EnableCoroExplicitStatusPanicABI {
		return coro.PanicExplicitStatusABIV0
	}
	return coro.PanicLegacyABIV0
}

func activeCoroFuncRepABIVersion(conf *Config) string {
	if conf != nil && conf.EnableCoroPlainDispatch {
		return coro.FuncRepABIV1
	}
	return coro.FuncRepABIV0
}

// nativeCoroDoorbellRuntimeABI mirrors the production target file selection
// for the compiler-owned callback root, bootstrap hash, and entry relocation.
// Named targets remain excluded until their OS/runtime contract explicitly
// opts into the POSIX pipe backend.
func nativeCoroDoorbellRuntimeABI(conf *Config) bool {
	if conf == nil || !conf.EnableCoroProgramBootstrapRun || conf.Target != "" ||
		(conf.Goos != "darwin" && conf.Goos != "linux") {
		return false
	}
	for _, tag := range []string{"baremetal", "tinygo.wasm", "wasip2", "wasm_unknown", "coro_runtime_adapter_test"} {
		if configHasBuildTag(conf, tag) {
			return false
		}
	}
	return true
}

// nativeCoroTimerRuntimeABI is narrower than the retained pipe capability.
// The current Linux clock_gettime declaration and Darwin uptime clock have a
// verified 64-bit timespec domain; 32-bit libc time32/time64 variants require
// a target-specific declaration or C wrapper before this capability can be
// widened. Named and embedded targets remain excluded by the doorbell gate.
func nativeCoroTimerRuntimeABI(conf *Config) bool {
	if !nativeCoroDoorbellRuntimeABI(conf) {
		return false
	}
	switch conf.Goos {
	case "darwin":
		return conf.Goarch == "amd64" || conf.Goarch == "arm64"
	case "linux":
		switch conf.Goarch {
		case "amd64", "arm64", "loong64", "ppc64", "ppc64le", "riscv64", "s390x":
			return true
		}
	}
	return false
}

func configHasBuildTag(conf *Config, want string) bool {
	if conf == nil || want == "" {
		return false
	}
	for _, tag := range splitSourcePatchBuildTags(conf.Tags) {
		if tag == want {
			return true
		}
	}
	for _, tag := range parseSourcePatchBuildTags(conf.GoBuildFlags) {
		if tag == want {
			return true
		}
	}
	return false
}

// requiredCoroProgramRuntimePlan returns the Go bodies referenced only by
// compiler-generated entry/coroutine IR and their exact static call closure.
// They are not visible from the application's source roots. The closure is a
// trusted scheduler-stack island: CFG loops do not turn its fixed C ABI into a
// coroutine, and exact frozen C leaves receive a temporary compatible-known
// summary. Their fallback SSA stubs remain ignored; ordinary C declarations
// outside this compiler-owned closure stay unknown foreign.
func requiredCoroProgramRuntimePlan(ctx *context) (coro.Roots, map[*ssa.Function]struct{}, []requiredCoroDirectPlainCallArgument, map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate, error) {
	if ctx == nil || ctx.buildConf == nil {
		return nil, nil, nil, nil, nil
	}
	if ctx.buildConf.EnableCoroClosedStaticSpawn && !ctx.buildConf.EnableCoroProgramBootstrapRun {
		return nil, nil, nil, nil, fmt.Errorf("coroutine closed static spawn runtime roots require runnable program bootstrap v2")
	}
	if !ctx.buildConf.EnableCoroChildAwait {
		return nil, nil, nil, nil, nil
	}
	if ctx.coroSSAEmission == nil || ctx.coroEmission == nil {
		return nil, nil, nil, nil, fmt.Errorf("coroutine program bootstrap runtime roots require a frozen emission universe")
	}
	closedDynamic, err := proveCoroTLSDestructorClosedDynamicCalls(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// runtimeLinkRequirements makes the real LLGo runtime package init an
	// entry-module call for every active child-await executable. That edge is
	// compiler-generated LLVM IR and therefore invisible to the source SSA call
	// graph; keep it as an explicit synchronous root even when the runnable
	// program-bootstrap gate is disabled. The scheduler driver/hooks below are
	// referenced only by the runnable bootstrap path and must not leak into the
	// descriptor-only plan.
	names := []string{"init"}
	demandByName := map[string]coro.Demand{"init": coro.SyncDemand}
	plainRootByName := map[string]bool{"init": true}
	if ctx.buildConf.EnableCoroProgramBootstrapRun {
		// The managed startup program owns runtime.init. Its synchronous Go source
		// style is preserved by AsyncDemand propagation: a non-suspending body
		// remains one DirectPlain body, while an async-tainted body has one
		// DirectCoro primary and is awaited by the compiler bootstrap.
		demandByName["init"] = coro.AsyncDemand
		plainRootByName["init"] = false
		names = append(names,
			coroFrameAllocatorBootstrapSymbolV1,
			coroProgramBeginSymbolV1,
			coroProgramRunSymbolV1,
			coroProgramContinueSymbolV1,
			coroWaitPrepareSymbolV1,
			coroWaitRollbackSymbolV1,
			coroWaitRetireCompletedSymbolV1,
		)
	}
	if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
		names = append(names, coroNativePostWaitSymbolV1)
	}
	if nativeCoroTimerRuntimeABI(ctx.buildConf) {
		names = append(names,
			coroTimerPrepareAfterSymbolV1,
			coroTimerRetireCompletedSymbolV1,
		)
	}
	if ctx.buildConf.EnableCoroProgramBootstrapRun {
		names = append(names,
			"__llgo_coro_frame_alloc_v1",
			"__llgo_coro_frame_publish_v1",
			"__llgo_coro_await_prepare_v1",
			"__llgo_coro_preempt_poll_v1",
			"__llgo_coro_yield_prepare_v1",
			"__llgo_coro_park_prepare_v1",
			"__llgo_coro_complete_prepare_v1",
			"__llgo_coro_frame_free_v1",
		)
	}
	if ctx.buildConf.EnableCoroExplicitStatusPanicABI {
		// Physical coroutine bodies reference this hook from compiler-generated
		// IR, so the source SSA graph has no edge that could retain it. Keep the
		// exact runtime body as a synchronous direct-plain root only while the
		// target-wide ExplicitStatus panic identity is selected.
		names = append(names, "__llgo_coro_panic_prepare_v1")
	}
	if ctx.buildConf.EnableCoroClosedStaticSpawn {
		names = append(names,
			"__llgo_coro_spawn_begin_v1",
			"__llgo_coro_spawn_commit_v1",
			coroProgramMainReturnSymbolV1,
		)
	}
	for _, name := range names[1:] {
		demandByName[name] = coro.SyncDemand
		plainRootByName[name] = true
	}
	byName := make(map[string]*ssa.Function, len(names))
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	for _, fn := range ctx.coroSSAEmission.Functions() {
		if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil || llssa.PathOf(fn.Pkg.Pkg) != llssa.PkgRuntime {
			continue
		}
		if _, ok := wanted[fn.Name()]; !ok {
			continue
		}
		if previous := byName[fn.Name()]; previous != nil && previous != fn {
			return nil, nil, nil, nil, fmt.Errorf("coroutine program bootstrap runtime ABI %q has multiple canonical SSA bodies", fn.Name())
		}
		byName[fn.Name()] = fn
	}
	roots := make(coro.Roots, 0, len(names))
	for _, name := range names {
		fn := byName[name]
		if fn == nil {
			return nil, nil, nil, nil, fmt.Errorf("coroutine program bootstrap runtime ABI %q has no emitted Go body in %q", name, llssa.PkgRuntime)
		}
		if name == coroProgramContinueSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) || sig.Results().Len() != 0 ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine program bootstrap runtime ABI %q must have exact func(uint32) signature", name)
			}
		}
		if name == coroNativePostWaitSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 4 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine native post-wait ABI %q must have exact func(uint32, uint32, uint32, uint32) uint32 signature", name)
			}
			for parameter := 0; parameter < sig.Params().Len(); parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.Uint32]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine native post-wait ABI %q must have exact func(uint32, uint32, uint32, uint32) uint32 signature", name)
				}
			}
		}
		if name == coroWaitPrepareSymbolV1 {
			sig := fn.Signature
			uint32Pointer := types.NewPointer(types.Typ[types.Uint32])
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 6 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine wait prepare ABI %q must have exact func(unsafe.Pointer, *uint32, *uint32, *uint32, *uint32, *uint32) bool signature", name)
			}
			for parameter := 1; parameter < sig.Params().Len(); parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), uint32Pointer) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine wait prepare ABI %q must have exact func(unsafe.Pointer, *uint32, *uint32, *uint32, *uint32, *uint32) bool signature", name)
				}
			}
		}
		if name == coroWaitRollbackSymbolV1 || name == coroWaitRetireCompletedSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 4 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine wait owner ABI %q must have exact func(unsafe.Pointer, uint32, uint32, uint32) bool signature", name)
			}
			for parameter := 1; parameter < sig.Params().Len(); parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.Uint32]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine wait owner ABI %q must have exact func(unsafe.Pointer, uint32, uint32, uint32) bool signature", name)
				}
			}
		}
		if name == coroTimerPrepareAfterSymbolV1 {
			sig := fn.Signature
			uint32Pointer := types.NewPointer(types.Typ[types.Uint32])
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 5 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.Int64]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine timer prepare ABI %q must have exact func(unsafe.Pointer, int64, *uint32, *uint32, *uint32) bool signature", name)
			}
			for parameter := 2; parameter < sig.Params().Len(); parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), uint32Pointer) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine timer prepare ABI %q must have exact func(unsafe.Pointer, int64, *uint32, *uint32, *uint32) bool signature", name)
				}
			}
		}
		if name == coroTimerRetireCompletedSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 4 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine timer owner ABI %q must have exact func(unsafe.Pointer, uint32, uint32, uint32) bool signature", name)
			}
			for parameter := 1; parameter < sig.Params().Len(); parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.Uint32]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine timer owner ABI %q must have exact func(unsafe.Pointer, uint32, uint32, uint32) bool signature", name)
				}
			}
		}
		goBody, err := frozenGoEmittedBody(ctx.coroEmission, fn)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("classify coroutine program bootstrap runtime ABI %q: %w", name, err)
		}
		if !goBody {
			return nil, nil, nil, nil, fmt.Errorf("coroutine program bootstrap runtime ABI %q has no emitted Go body in %q", name, llssa.PkgRuntime)
		}
		roots = append(roots, coro.Root{Function: fn, Demand: demandByName[name]})
	}

	plain := make(map[*ssa.Function]struct{})
	var directPlain []requiredCoroDirectPlainCallArgument
	queue := make([]*ssa.Function, 0, len(roots))
	for _, root := range roots {
		if plainRootByName[root.Function.Name()] {
			queue = append(queue, root.Function)
		}
	}
	for head := 0; head < len(queue); head++ {
		fn := queue[head]
		if _, seen := plain[fn]; seen {
			continue
		}
		plain[fn] = struct{}{}
		goBody, err := frozenGoEmittedBody(ctx.coroEmission, fn)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("classify compiler runtime ABI function %q: %w", fn.Name(), err)
		}
		if !goBody {
			// Exact C declarations remain required plain leaves, but their Go
			// fallback SSA body is not part of the emitted program. Other kinds
			// are retained here and rejected by requiredPlain classification.
			continue
		}
		loweredCalls, err := ctx.coroEmission.CoroLoweredCalls(fn)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("classify compiler runtime ABI lowered calls in %q: %w", fn.Name(), err)
		}
		for _, lowered := range loweredCalls {
			if lowered.Target == nil {
				return nil, nil, nil, nil, fmt.Errorf("compiler runtime ABI function %q has a nil lowered helper target for %q", fn.Name(), lowered.LogicalName)
			}
			if _, seen := plain[lowered.Target]; !seen {
				queue = append(queue, lowered.Target)
			}
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				raw := call.Common().StaticCallee()
				if raw == nil {
					if _, certified := closedDynamic[call]; certified {
						// The certified descriptor call is part of this exact plain
						// callback body, but its target remains outside the trusted
						// scheduler-stack island. Fixed-point analysis must prove the
						// target NoSuspend/!NeedsPreempt without suppressing either.
						continue
					}
					continue
				}
				callee, ok := ctx.coroEmission.Resolve(raw)
				if !ok || callee == nil {
					continue
				}
				semantics, intrinsic, err := ctx.coroEmission.CoroIntrinsicCallSiteSemantics(call)
				if err != nil {
					return nil, nil, nil, nil, fmt.Errorf("classify compiler runtime ABI intrinsic %q in %q: %w", callee.Name(), fn.Name(), err)
				}
				if intrinsic && semantics.ElidesManagedCall() {
					// cl emits no call to the intrinsic declaration itself. Any
					// managed calls inserted by the operation were queued above
					// from its exact frozen lowered-call set.
					continue
				}
				if _, seen := plain[callee]; !seen {
					queue = append(queue, callee)
				}
				for argument, value := range call.Common().Args {
					parameter, ok := staticCallArgumentParameterType(call, argument)
					if !ok || ctx.prog.TypeBackground(parameter) != llssa.InC {
						continue
					}
					if _, signature := types.Unalias(parameter).Underlying().(*types.Signature); !signature {
						continue
					}
					target, ok := exactCoroStaticFunctionValue(ctx, value)
					if !ok {
						continue
					}
					closure, ok, err := provenCoroDirectPlainStaticClosure(ctx, target, closedDynamic)
					if err != nil {
						return nil, nil, nil, nil, fmt.Errorf("prove direct-plain callback target %q in %q: %w", target.Name(), fn.Name(), err)
					}
					if !ok {
						continue
					}
					directPlain = append(directPlain, requiredCoroDirectPlainCallArgument{
						call: call, argument: argument, target: target,
					})
					for _, member := range closure {
						if _, seen := plain[member]; !seen {
							queue = append(queue, member)
						}
					}
				}
			}
		}
	}
	return roots, plain, directPlain, closedDynamic, nil
}

func frozenGoEmittedBody(universe *cl.EmissionUniverse, fn *ssa.Function) (bool, error) {
	if universe == nil || fn == nil || len(fn.Blocks) == 0 {
		return false, nil
	}
	background, classified, err := universe.FunctionBackground(fn)
	if err != nil {
		return false, err
	}
	return classified && background == llssa.InGo, nil
}

func staticCallArgumentParameterType(call ssa.CallInstruction, argument int) (types.Type, bool) {
	if call == nil || call.Common() == nil || call.Common().StaticCallee() == nil || argument < 0 || argument >= len(call.Common().Args) {
		return nil, false
	}
	signature := call.Common().StaticCallee().Signature
	if signature == nil {
		return nil, false
	}
	if receiver := signature.Recv(); receiver != nil {
		if argument == 0 {
			return receiver.Type(), true
		}
		argument--
	}
	parameters := signature.Params()
	if parameters == nil || parameters.Len() == 0 {
		return nil, false
	}
	if signature.Variadic() && argument >= parameters.Len()-1 {
		slice, ok := types.Unalias(parameters.At(parameters.Len() - 1).Type()).Underlying().(*types.Slice)
		if !ok {
			return nil, false
		}
		return slice.Elem(), true
	}
	if argument >= parameters.Len() {
		return nil, false
	}
	return parameters.At(argument).Type(), true
}

func exactCoroStaticFunctionValue(ctx *context, value ssa.Value) (*ssa.Function, bool) {
	for value != nil {
		switch current := value.(type) {
		case *ssa.Function:
			if len(current.FreeVars) != 0 {
				return nil, false
			}
			target, ok := ctx.coroEmission.Resolve(current)
			return target, ok && target != nil && len(target.FreeVars) == 0
		case *ssa.MakeClosure:
			if len(current.Bindings) != 0 {
				return nil, false
			}
			function, ok := current.Fn.(*ssa.Function)
			if !ok {
				return nil, false
			}
			value = function
		case *ssa.ChangeType:
			value = current.X
		case *ssa.Convert:
			value = current.X
		default:
			return nil, false
		}
	}
	return nil, false
}

// provenCoroDirectPlainStaticClosure accepts only a closed Go body whose calls
// are direct, statically resolved emitted bodies (or builtins). An exact frozen
// C declaration may terminate the closure only for the compiler-owned TLS
// callback whose field-flow proof supplied one of closedDynamic's calls. The
// declaration then enters requiredPlain and is classified through the same
// frozen IgnoreBody/ExternalKnown path as the compiler runtime ABI. Dynamic
// calls, go/defer, other bodyless leaves, captured closures, and unresolved
// aliases remain on the ordinary Dispatch path. Effect and representation are
// independently checked after fixed-point analysis; this prefilter only
// establishes that it is sound to seed the candidate's bounded scheduler-stack
// island.
func provenCoroDirectPlainStaticClosure(ctx *context, target *ssa.Function, closedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate) ([]*ssa.Function, bool, error) {
	if ctx == nil || ctx.coroEmission == nil || target == nil || len(target.FreeVars) != 0 {
		return nil, false, nil
	}
	goBody, err := frozenGoEmittedBody(ctx.coroEmission, target)
	if err != nil {
		return nil, false, err
	}
	if !goBody {
		return nil, false, nil
	}
	seen := make(map[*ssa.Function]struct{})
	seenCLeaves := make(map[*ssa.Function]struct{})
	queue := []*ssa.Function{target}
	closure := make([]*ssa.Function, 0, 4)
	tlsCallback := provenCoroTLSDirectPlainClosureRoot(ctx, target, closedDynamic)
	for head := 0; head < len(queue); head++ {
		function := queue[head]
		if _, ok := seen[function]; ok {
			continue
		}
		goBody, err := frozenGoEmittedBody(ctx.coroEmission, function)
		if err != nil {
			return nil, false, err
		}
		if !goBody || len(function.FreeVars) != 0 {
			return nil, false, nil
		}
		seen[function] = struct{}{}
		closure = append(closure, function)
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
					continue
				}
				if _, direct := call.(*ssa.Call); !direct {
					return nil, false, nil
				}
				raw := call.Common().StaticCallee()
				if raw == nil {
					if _, certified := closedDynamic[call]; certified && !call.Common().IsInvoke() {
						// The exact descriptor target is deliberately not added to
						// closure: unlike the raw C callback it is not trusted to run
						// without preemption. Post-plan validation checks its real
						// fixed-point Effect/Exec instead.
						continue
					}
					return nil, false, nil
				}
				callee, ok := ctx.coroEmission.Resolve(raw)
				if !ok || callee == nil || len(callee.FreeVars) != 0 {
					return nil, false, nil
				}
				calleeGoBody, err := frozenGoEmittedBody(ctx.coroEmission, callee)
				if err != nil {
					return nil, false, err
				}
				if !calleeGoBody {
					background, classified, err := ctx.coroEmission.FunctionBackground(callee)
					if err != nil {
						return nil, false, err
					}
					if !tlsCallback || !classified || background != llssa.InC {
						return nil, false, nil
					}
					if _, ok := seenCLeaves[callee]; !ok {
						seenCLeaves[callee] = struct{}{}
						closure = append(closure, callee)
					}
					continue
				}
				if _, ok := seen[callee]; !ok {
					queue = append(queue, callee)
				}
			}
		}
	}
	return closure, true, nil
}

// provenCoroTLSDirectPlainClosureRoot binds the frozen-C-leaf exception to the
// exact callback body audited by proveCoroTLSDestructorClosedDynamicCalls. A
// certificate reachable only through a helper is insufficient: otherwise an
// unrelated user callback could call that helper and inherit the exception.
func provenCoroTLSDirectPlainClosureRoot(ctx *context, target *ssa.Function, closedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate) bool {
	if !coroTLSFunctionInOwnedPackage(ctx, target) {
		return false
	}
	for call := range closedDynamic {
		if call == nil || call.Parent() != target || call.Common() == nil || call.Common().IsInvoke() {
			continue
		}
		if _, ordinary := call.(*ssa.Call); ordinary {
			return true
		}
	}
	return false
}

func buildCoroPlanDigestMetadata(ctx *context) (coro.PlanDigestMetadata, error) {
	if ctx == nil || ctx.buildConf == nil {
		return coro.PlanDigestMetadata{}, fmt.Errorf("missing build context")
	}
	target := ctx.prog.TargetSpec()
	endianness := ""
	switch ctx.prog.TargetData().ByteOrder() {
	case gllvm.LittleEndian:
		endianness = "little"
	case gllvm.BigEndian:
		endianness = "big"
	default:
		return coro.PlanDigestMetadata{}, fmt.Errorf("unsupported LLVM byte order")
	}
	return coro.PlanDigestMetadata{
		CoroABI:        activeCoroABIVersion(ctx.buildConf),
		SchedulerABI:   activeCoroSchedulerABIVersion(ctx.buildConf),
		PanicABI:       activeCoroPanicABIVersion(ctx.buildConf),
		FuncRepABI:     activeCoroFuncRepABIVersion(ctx.buildConf),
		TargetTriple:   target.Triple,
		TargetCPU:      target.CPU,
		TargetFeatures: target.Features,
		TargetABI:      target.TargetABI,
		PointerBits:    ctx.prog.PointerSize() * 8,
		Endianness:     endianness,
		DataLayout:     ctx.prog.DataLayout(),
	}, nil
}

func prepareCoroEmissionUniverse(ctx *context, packages []*aPackage) error {
	inputs := make([]cl.EmissionPackage, 0, len(packages))
	hasRuntimeABI := false
	for _, aPkg := range packages {
		if aPkg == nil || aPkg.Package == nil || aPkg.SSA == nil || llruntime.SkipToBuild(aPkg.PkgPath) {
			continue
		}
		metadataOnly := false
		kind, _ := cl.PkgKindOf(aPkg.Types)
		switch kind {
		case cl.PkgDeclOnly:
			// Declaration-only packages do not emit LLVM definitions, but their
			// exact syntax owns the C/Python link directives used to classify
			// declarations reached from emitted Go bodies. Freeze that frontend
			// metadata in the universe instead of rediscovering it from a name or
			// from a fallback SSA body.
			metadataOnly = true
		case cl.PkgLinkIR, cl.PkgLinkExtern, cl.PkgPyModule:
			if len(aPkg.GoFiles) == 0 {
				continue
			}
		}
		files := append([]*ast.File(nil), aPkg.Syntax...)
		if aPkg.AltPkg != nil {
			files = append(files, aPkg.AltPkg.Syntax...)
		}
		inputs = append(inputs, cl.EmissionPackage{
			SSA:          aPkg.SSA,
			Files:        files,
			Identity:     aPkg.ID,
			MetadataOnly: metadataOnly,
		})
		hasRuntimeABI = hasRuntimeABI || aPkg.PkgPath == llssa.PkgRuntime
	}
	emission, err := cl.PrepareEmissionUniverseWithOptions(ctx.prog, ctx.patches, inputs, cl.EmissionUniverseOptions{
		// Active archive-producing entry resolution with the real runtime input
		// must freeze every hidden compiler/runtime ABI edge. Isolated plan tests
		// and report-only builds preserve the legacy incomplete-package behavior.
		CompleteRuntimeABI: hasRuntimeABI && ctx.buildConf != nil && ctx.buildConf.EnableCoroEntryResolution,
	})
	if err != nil {
		return err
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ctx.progSSA, emission.Functions())
	if err != nil {
		return err
	}
	ctx.coroEmission = emission
	ctx.coroSSAEmission = ssaEmission
	return nil
}

func newLLSSATarget(conf *Config, export crosscompile.Export) *llssa.Target {
	target := &llssa.Target{
		GOOS:     conf.Goos,
		GOARCH:   conf.Goarch,
		Target:   conf.Target,
		OptLevel: conf.OptLevel,
	}
	if export.LLVMTarget != "" {
		target.Resolved = &llssa.TargetSpec{
			Triple:    export.LLVMTarget,
			CPU:       export.CPU,
			Features:  export.Features,
			TargetABI: export.TargetABI,
		}
	}
	return target
}

func applyFrontendGCFlags(conf *Config) {
	for _, buildFlag := range conf.GoBuildFlags {
		value, ok := strings.CutPrefix(buildFlag, "-gcflags=")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) != 0 {
			if _, compilerFlags, hasPattern := strings.Cut(fields[0], "="); hasPattern {
				fields[0] = compilerFlags
			}
		}
		for _, compilerFlag := range fields {
			switch {
			case strings.HasPrefix(compilerFlag, "-lang="):
				conf.GoVersion = strings.TrimPrefix(compilerFlag, "-lang=")
			case compilerFlag == "-N", compilerFlag == "-l", strings.HasPrefix(compilerFlag, "-l="):
				conf.OptLevel = optlevel.O0
			}
		}
	}
}

func allowMissingFunctionBodies(initial []*packages.Package) {
	for _, pkg := range initial {
		hasMissingBody := false
		hasOtherError := false
		for _, pkgErr := range pkg.Errors {
			switch {
			case strings.Contains(pkgErr.Msg, "missing function body"):
				hasMissingBody = true
			case strings.HasPrefix(pkgErr.Msg, "# "):
				// go list prefixes compiler diagnostics with the package name.
			default:
				hasOtherError = true
			}
		}
		if hasMissingBody && !hasOtherError {
			pkg.Errors = nil
			pkg.TypeErrors = nil
			pkg.IllTyped = false
		}
	}
}

func needLink(pkg *packages.Package, mode Mode) bool {
	if mode == ModeTest {
		return strings.HasSuffix(pkg.ID, ".test")
	}
	return pkg.Name == "main"
}

func filterTestPackages(initial []*packages.Package, outFile string) ([]*packages.Package, error) {
	filtered := initial[:0]
	for _, pkg := range initial {
		if needLink(pkg, ModeTest) {
			filtered = append(filtered, pkg)
		}
	}
	if len(filtered) > 1 && outFile != "" {
		return nil, fmt.Errorf("cannot use -o flag with multiple packages")
	}
	return filtered, nil
}

func (p Package) setNeedRuntimeOrPyInit(needRuntime, needPyInit bool) {
	p.NeedRt = needRuntime
	p.NeedPyInit = needPyInit
}

func (p Package) isNeedRuntimeOrPyInit() (needRuntime, needPyInit bool) {
	needRuntime = p.NeedRt
	needPyInit = p.NeedPyInit
	return
}

const (
	ssaBuildMode = ssa.SanityCheckFunctions | ssa.InstantiateGenerics
)

type context struct {
	env            *llvm.Env
	conf           *packages.Config
	progSSA        *ssa.Program
	prog           llssa.Program
	dedup          packages.Deduper
	patches        cl.Patches
	callerTracking *cl.CallerTracking
	built          map[string]none
	fingerprinting map[string]bool
	initial        []*packages.Package
	pkgs           map[*packages.Package]Package // cache for lookup
	pkgByID        map[string]Package            // cache for lookup by pkg.ID
	mode           Mode
	nLibdir        int32
	output         bool
	passOpt        bool

	buildConf    *Config
	crossCompile crosscompile.Export

	cTransformer *cabi.Transformer

	testFail bool

	// Cache related fields
	cacheManager *cacheManager
	llvmVersion  string

	// go list derived file lists (SFiles, etc.)
	sfilesCache map[string][]string // pkg.ID -> absolute .s/.S file paths

	// plan9asm package policy parsed from env.
	plan9asmOnce sync.Once
	plan9asmMode plan9asmPkgsEnvMode
	plan9asmPkgs map[string]bool

	// coroPlan is compilation-scoped. It remains report-only unless
	// EnableCoroEntryResolution is set explicitly.
	coroPlan        *coro.SSAPlan
	coroEmission    *cl.EmissionUniverse
	coroSSAEmission *coro.SSAEmissionUniverse
	// coroTLSDestructorFixturePkg is an internal test-only identity override.
	// Production builds leave it empty and accept only runtime/internal/clite/tls.
	coroTLSDestructorFixturePkg string
	coroPlanDigest              string
	coroPlanMetadata            coro.PlanDigestMetadata
	// Frozen immediately after whole-program analysis, before package codegen.
	// linkMainPkg only consumes these exact per-entry-package tables.
	coroProgramBootstraps map[string]*coroProgramBootstrapV1

	// clCompilation is shared by all source packages in this build. Active
	// cache registration is enabled only after coroPlanDigest and its complete
	// ABI/target record have been frozen into archive fingerprints.
	clCompilation *cl.Compilation
}

func (c *context) compiler() *clang.Cmd {
	config := clang.NewConfig(
		c.crossCompile.CC,
		c.crossCompile.CCFLAGS,
		c.crossCompile.CFLAGS,
		c.crossCompile.LDFLAGS,
		c.crossCompile.Linker,
	)
	cmd := clang.NewCompiler(config)
	cmd.Verbose = c.shouldPrintCommands(false)
	return cmd
}

func (c *context) linker() *clang.Cmd {
	config := clang.NewConfig(
		c.crossCompile.CC,
		c.crossCompile.CCFLAGS,
		c.crossCompile.CFLAGS,
		c.crossCompile.LDFLAGS,
		c.crossCompile.Linker,
	)
	cmd := clang.NewLinker(config)
	cmd.Verbose = c.shouldPrintCommands(false)
	return cmd
}

// shouldPrintCommands reports whether command tracing should be enabled.
func (c *context) shouldPrintCommands(verbose bool) bool {
	return c.buildConf.PrintCommands || c.buildConf.Verbose || verbose
}

func (c *context) hasAltPkg(pkgPath string) bool {
	return hasAltPkgForTarget(c.buildConf, pkgPath)
}

// normalizeToArchive creates an archive from object files and sets ArchiveFile.
// This ensures the link step always consumes .a archives regardless of cache state.
func normalizeToArchive(ctx *context, aPkg *aPackage, verbose bool) error {
	if len(aPkg.ObjFiles) == 0 {
		return nil
	}

	archiveFile, err := os.CreateTemp("", "pkg-*.a")
	if err != nil {
		return fmt.Errorf("create temp archive: %w", err)
	}
	archiveFile.Close()
	archivePath := archiveFile.Name()

	if err := ctx.createArchiveFile(archivePath, aPkg.ObjFiles, verbose); err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("create archive for %s: %w", aPkg.PkgPath, err)
	}

	aPkg.ObjFiles = nil
	aPkg.ArchiveFile = archivePath
	return nil
}

func buildAllPkgs(ctx *context, pkgs []*aPackage, verbose bool) ([]*aPackage, error) {
	built := ctx.built
	usePackageCache := ctx.canUsePackageCache()

	// Split packages into runtime tree vs others so we can defer runtime build.
	var runtimePkgs []*aPackage
	var normalPkgs []*aPackage
	for _, p := range pkgs {
		if isRuntimePkg(p.PkgPath) {
			runtimePkgs = append(runtimePkgs, p)
		} else {
			normalPkgs = append(normalPkgs, p)
		}
	}

	var needRuntime, needPyInit bool

	buildOne := func(aPkg *aPackage) error {
		pkg := aPkg.Package
		if _, ok := built[pkg.ID]; ok {
			// Already built, skip but keep ExportFile for linking
			return nil
		}
		built[pkg.ID] = none{}

		switch kind, param := cl.PkgKindOf(pkg.Types); kind {
		case cl.PkgDeclOnly:
			pkg.ExportFile = ""
		case cl.PkgLinkIR, cl.PkgLinkExtern, cl.PkgPyModule:
			if len(pkg.GoFiles) > 0 {
				if err := ctx.collectFingerprint(aPkg); err != nil {
					return err
				}
				if usePackageCache {
					ctx.tryLoadFromCache(aPkg)
				}
				if verbose {
					if !usePackageCache {
						fmt.Fprintf(os.Stderr, "CACHE DISABLED (coroutine entry resolution): %s\n", pkg.PkgPath)
					} else if aPkg.CacheHit {
						fmt.Fprintf(os.Stderr, "CACHE HIT: %s\n", pkg.PkgPath)
					} else {
						fmt.Fprintf(os.Stderr, "CACHE MISS: %s\n", pkg.PkgPath)
					}
				}
				if err := buildPkg(ctx, aPkg, verbose); err != nil {
					return err
				}
				if !aPkg.CacheHit {
					if err := normalizeToArchive(ctx, aPkg, verbose); err != nil {
						return err
					}
					if kind == cl.PkgLinkExtern {
						appendExternalLinkArgs(ctx, aPkg, param)
					}
					if usePackageCache {
						if err := ctx.saveToCache(aPkg); err != nil && verbose {
							fmt.Fprintf(os.Stderr, "warning: failed to save cache for %s: %v\n", pkg.PkgPath, err)
						}
					}
				}
			} else {
				pkg.ExportFile = ""
				if kind == cl.PkgLinkExtern {
					appendExternalLinkArgs(ctx, aPkg, param)
				}
			}
		default:
			if err := ctx.collectFingerprint(aPkg); err != nil {
				return err
			}
			if usePackageCache {
				ctx.tryLoadFromCache(aPkg)
			}
			if verbose {
				if !usePackageCache {
					fmt.Fprintf(os.Stderr, "CACHE DISABLED (coroutine entry resolution): %s\n", pkg.PkgPath)
				} else if aPkg.CacheHit {
					fmt.Fprintf(os.Stderr, "CACHE HIT: %s\n", pkg.PkgPath)
				} else {
					fmt.Fprintf(os.Stderr, "CACHE MISS: %s\n", pkg.PkgPath)
				}
			}
			if err := buildPkg(ctx, aPkg, verbose); err != nil {
				return err
			}
			if !aPkg.CacheHit {
				aPkg.setNeedRuntimeOrPyInit(aPkg.LPkg.NeedRuntime, aPkg.LPkg.NeedPyInit)
			}
			needRuntime = needRuntime || aPkg.NeedRt
			needPyInit = needPyInit || aPkg.NeedPyInit
			if !aPkg.CacheHit {
				if err := normalizeToArchive(ctx, aPkg, verbose); err != nil {
					return err
				}
				if usePackageCache {
					if err := ctx.saveToCache(aPkg); err != nil && verbose {
						fmt.Fprintf(os.Stderr, "warning: failed to save cache for %s: %v\n", pkg.PkgPath, err)
					}
				}
			}
		}
		return nil
	}

	// Build non-runtime packages first, so we know whether runtime is actually needed.
	for _, p := range normalPkgs {
		if err := buildOne(p); err != nil {
			return nil, err
		}
	}

	// Active coroutine planning freezes and validates one exact compilation-wide
	// universe before LLVM codegen. Its prepared universe includes the runtime
	// tree, so emit that tree as well even when target lowering would otherwise
	// discover no runtime dependency. Report-only planning preserves the legacy
	// lazy-runtime behavior and package-cache/IR output.
	if shouldBuildRuntimePackages(ctx.buildConf, needRuntime, needPyInit) {
		for _, p := range runtimePkgs {
			if err := buildOne(p); err != nil {
				return nil, err
			}
		}
	}

	return pkgs, nil
}

func shouldBuildRuntimePackages(conf *Config, needRuntime, needPyInit bool) bool {
	return needRuntime || needPyInit || conf.Target == "" || conf.EnableCoroEntryResolution
}

// runtimeLinkRequirements keeps active child-await runtime initialization on
// the same path as legacy runtime references without changing the lazy-link
// behavior of entry-resolution-only named targets.
func runtimeLinkRequirements(conf *Config, needRuntime, needPyInit bool) (initRuntime, linkRuntime bool) {
	if conf != nil && conf.EnableCoroChildAwait {
		needRuntime = true
	}
	host := conf != nil && conf.Target == ""
	return needRuntime, needRuntime || needPyInit || host
}

func appendExternalLinkArgs(ctx *context, aPkg *aPackage, spec string) {
	// need to be linked with external library
	// format: ';' separated alternative link methods. e.g.
	//   link: $LLGO_LIB_PYTHON; $(pkg-config --libs python3-embed); -lpython3
	altParts := strings.Split(spec, ";")
	expdArgs := make([]string, 0, len(altParts))
	for _, alt := range altParts {
		alt = strings.TrimSpace(alt)
		if strings.ContainsRune(alt, '$') {
			expdArgs = append(expdArgs, xenv.ExpandEnvToArgs(alt)...)
			atomic.AddInt32(&ctx.nLibdir, 1)
		} else {
			fields := strings.Fields(alt)
			expdArgs = append(expdArgs, fields...)
		}
		if len(expdArgs) > 0 {
			break
		}
	}
	if len(expdArgs) == 0 {
		panic(fmt.Sprintf("'%s' cannot locate the external library", spec))
	}

	pkgLinkArgs := make([]string, 0, 3)
	if expdArgs[0][0] == '-' {
		pkgLinkArgs = append(pkgLinkArgs, expdArgs...)
	} else {
		linkFile := expdArgs[0]
		dir, lib := filepath.Split(linkFile)
		pkgLinkArgs = append(pkgLinkArgs, "-l"+lib)
		if dir != "" {
			pkgLinkArgs = append(pkgLinkArgs, "-L"+dir)
			atomic.AddInt32(&ctx.nLibdir, 1)
		}
	}
	if ctx.buildConf.CheckLinkArgs {
		if err := ctx.compiler().CheckLinkArgs(pkgLinkArgs, isWasmTarget(ctx.buildConf.Goos)); err != nil {
			panic(fmt.Sprintf("test link args '%s' failed\n\texpanded to: %v\n\tresolved to: %v\n\terror: %v", spec, expdArgs, pkgLinkArgs, err))
		}
	}
	aPkg.LinkArgs = append(aPkg.LinkArgs, pkgLinkArgs...)
}

var (
	errXflags = errors.New("-X flag requires argument of the form importpath.name=value")
)

// maxRewriteValueLength limits the size of rewrite values to prevent
// excessive memory usage and potential resource exhaustion during compilation.
const maxRewriteValueLength = 1 << 20 // 1 MiB cap per rewrite value

func addGlobalString(conf *Config, arg string, mainPkgs []string) {
	addGlobalStringWith(conf, arg, mainPkgs, true)
}

func addGlobalStringWith(conf *Config, arg string, mainPkgs []string, skipIfExists bool) {
	eq := strings.Index(arg, "=")
	dot := strings.LastIndex(arg[:eq+1], ".")
	if eq < 0 || dot < 0 {
		panic(errXflags)
	}
	pkg := arg[:dot]
	varName := arg[dot+1 : eq]
	value := arg[eq+1:]
	validateRewriteInput(pkg, varName, value)
	pkgs := []string{pkg}
	if pkg == "main" {
		pkgs = mainPkgs
	}
	if len(pkgs) == 0 {
		return
	}
	if conf.GlobalRewrites == nil {
		conf.GlobalRewrites = make(map[string]Rewrites)
	}
	for _, realPkg := range pkgs {
		vars := conf.GlobalRewrites[realPkg]
		if vars == nil {
			vars = make(Rewrites)
			conf.GlobalRewrites[realPkg] = vars
		}
		if skipIfExists {
			if _, exists := vars[varName]; exists {
				continue
			}
		}
		vars[varName] = value
	}
}

func validateRewriteInput(pkg, varName, value string) {
	if pkg == "" || strings.ContainsAny(pkg, " \t\r\n") {
		panic(fmt.Errorf("invalid package path for rewrite: %q", pkg))
	}
	if !token.IsIdentifier(varName) {
		panic(fmt.Errorf("invalid variable name for rewrite: %q", varName))
	}
	if len(value) > maxRewriteValueLength {
		panic(fmt.Errorf("rewrite value too large: %d bytes", len(value)))
	}
}

// compileExtraFiles compiles extra files (.s/.c) from target configuration and returns object files
func compileExtraFiles(ctx *context, verbose bool) ([]string, error) {
	if len(ctx.crossCompile.ExtraFiles) == 0 {
		return nil, nil
	}

	printCmds := ctx.shouldPrintCommands(verbose)
	var objFiles []string
	llgoRoot := env.LLGoROOT()

	for _, extraFile := range ctx.crossCompile.ExtraFiles {
		// Resolve the file path relative to llgo root
		srcFile := filepath.Join(llgoRoot, extraFile)

		// Check if file exists
		if _, err := os.Stat(srcFile); os.IsNotExist(err) {
			return nil, fmt.Errorf("extra file not found: %s", srcFile)
		}

		// Generate output file name
		tmpFile, err := os.CreateTemp("", "extra-*"+filepath.Base(extraFile))
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file for %s: %w", extraFile, err)
		}
		tmpFile.Close()
		baseName := tmpFile.Name()

		ext := filepath.Ext(srcFile)

		// Prepare base compilation arguments
		var baseArgs []string

		// Handle different file types
		switch ext {
		case ".c":
			baseArgs = append(baseArgs, "-x", "c")
		case ".S", ".s":
			baseArgs = append(baseArgs, "-x", "assembler-with-cpp")
		}

		// If GenLL is enabled, first emit .ll for debugging
		if ctx.buildConf.GenLL {
			llFile := baseName + ".ll"
			llArgs := append(slices.Clone(baseArgs), "-emit-llvm", "-S", "-o", llFile, "-c", srcFile)
			if printCmds {
				fmt.Fprintf(os.Stderr, "Compiling extra file (ll): clang %s\n", strings.Join(llArgs, " "))
			}
			cmd := ctx.compiler()
			if err := cmd.Compile(llArgs...); err != nil {
				return nil, fmt.Errorf("failed to compile extra file %s to .ll: %w", srcFile, err)
			}
		}

		// Always compile to .o for linking
		objFile := baseName + ".o"
		objArgs := append(baseArgs, "-o", objFile, "-c", srcFile)
		if printCmds {
			fmt.Fprintf(os.Stderr, "Compiling extra file: clang %s\n", strings.Join(objArgs, " "))
		}
		cmd := ctx.compiler()
		if err := cmd.Compile(objArgs...); err != nil {
			return nil, fmt.Errorf("failed to compile extra file %s: %w", srcFile, err)
		}

		objFiles = append(objFiles, objFile)
		os.Remove(baseName) // Remove the temp file we created for naming
	}

	return objFiles, nil
}

// rewritePrebuiltFuncTab runs the link-phase prebuilt-table rewrite on the
// linked executable: it deduplicates LTO inline copies of the funcinfo entry
// records against the symbol table and replaces the entry section with a
// sorted ftab plus findfunctab that the runtime adopts zero-copy (see
// internal/pclnpost and doc/design/pclntab-linkphase.md). Any failure leaves
// the binary fully functional on the first-use construction fallback.
func rewritePrebuiltFuncTab(ctx *context, out string, verbose bool) {
	if ctx == nil || ctx.prog == nil || !ctx.prog.FuncInfoSitesEnabled() || !shouldEmitRuntimeSites(ctx) {
		return
	}
	if ctx.buildConf.BuildMode != BuildModeExe {
		return
	}
	if os.Getenv("LLGO_PCLNPOST") == "0" { // escape hatch: keep first-use construction
		return
	}
	st, err := pclnpost.Rewrite(out)
	if err != nil {
		if verbose {
			fmt.Fprintf(os.Stderr, "llgo: prebuilt functab rewrite skipped: %v\n", err)
		}
		return
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "llgo: prebuilt functab: %d entries (%d LTO inline copies removed), %d buckets\n",
			st.FtabEntries, st.InlineCopies, st.Buckets)
	}
}

func linkMainPkg(ctx *context, pkg *packages.Package, pkgs []*aPackage, outputPath string, verbose bool) error {
	needRuntime := false
	needPyInit := false
	var needAbiInit int
	methodByIndex := make(map[int]none)
	methodByName := make(map[string]none)
	allPkgs := []*packages.Package{pkg}
	for _, v := range pkgs {
		allPkgs = append(allPkgs, v.Package)
	}
	visitRoots := allPkgs
	if ctx.mode == ModeTest {
		visitRoots = []*packages.Package{pkg}
		for _, p := range allPkgs {
			if isRuntimePkg(p.PkgPath) {
				visitRoots = append(visitRoots, p)
			}
		}
	}
	// archiveInputs contains package .a files. Object files are prepended later so
	// archive extraction can see their undefined references in a single linker pass.
	var archiveInputs []string
	var linkArgs []string
	var rtLinkInputs []string
	var rtLinkArgs []string
	linkedPkgs := make(map[string]bool) // Track linked packages by ID to avoid duplicates
	var linkedOrder []Package
	packages.Visit(visitRoots, nil, func(p *packages.Package) {
		// Skip if already linked this package (by ID)
		if linkedPkgs[p.ID] {
			return
		}
		aPkg := ctx.pkgs[p]
		if aPkg == nil {
			// Fallback: lookup by pkg.ID for packages that may be different instances
			aPkg = ctx.pkgByID[p.ID]
		}
		if p.ExportFile != "" && aPkg != nil { // skip packages that only contain declarations
			linkedPkgs[p.ID] = true
			linkedOrder = append(linkedOrder, aPkg)
		}
	})

	// packages.Visit with a post callback yields dependencies before importers.
	// Reverse that order so static archives are linked after the objects that use them.
	for i := len(linkedOrder) - 1; i >= 0; i-- {
		aPkg := linkedOrder[i]
		p := aPkg.Package
		// Defer linking runtime packages unless we actually need the runtime.
		if isRuntimePkg(p.PkgPath) {
			rtLinkArgs = append(rtLinkArgs, aPkg.LinkArgs...)
			if aPkg.ArchiveFile != "" {
				rtLinkInputs = append(rtLinkInputs, aPkg.ArchiveFile)
			}
			continue
		}
		// Only let non-runtime packages influence whether runtime is needed.
		need1, need2 := aPkg.isNeedRuntimeOrPyInit()
		needRuntime = needRuntime || need1
		needPyInit = needPyInit || need2
		needAbiInit |= aPkg.LPkg.NeedAbiInit
		for k, _ := range aPkg.LPkg.MethodByIndex {
			methodByIndex[k] = none{}
		}
		for k, _ := range aPkg.LPkg.MethodByName {
			methodByName[k] = none{}
		}

		linkArgs = append(linkArgs, aPkg.LinkArgs...)
		if aPkg.ArchiveFile != "" {
			archiveInputs = append(archiveInputs, aPkg.ArchiveFile)
		}
	}

	// The v1 frame hooks and scheduler adapter live in the runtime tree. Their
	// references originate in compiler-generated coroutine ramps, so they do
	// not pass through the ordinary runtimeFunc path that sets NeedRuntime.
	// Force runtime initialization before any root factory can allocate a frame.
	var linkRuntime bool
	needRuntime, linkRuntime = runtimeLinkRequirements(ctx.buildConf, needRuntime, needPyInit)

	// Only link runtime objects when needed (or for host builds where runtime is
	// always required). The child-await requirement above participates through
	// the same NeedRuntime path as ordinary runtime calls.
	if linkRuntime {
		linkArgs = append(linkArgs, rtLinkArgs...)
		archiveInputs = append(archiveInputs, rtLinkInputs...)
	}

	// Generate main module file (needed for global variables even in library modes)
	// This is compiled directly to .o and added to linkInputs (not cached)
	// Use a stable synthetic name to avoid confusing it with the real main package in traces/logs.
	funcInfo := prepareFuncInfoTableRecords(collectFuncInfo(linkedOrder), nil)
	pcLineInfo := collectPCLineInfo(linkedOrder)
	funcInfoStubs := collectFuncInfoStubRecords(linkedOrder, funcInfo)
	var coroRootAnchors []string
	var coroManifestHash [16]byte
	var coroBootstrap *coroProgramBootstrapV1
	if ctx.buildConf.EnableCoroChildAwait {
		var err error
		coroRootAnchors, err = collectLinkedCoroRootAnchors(linkedOrder)
		if err != nil {
			return err
		}
		if ctx.buildConf.EnableCoroProgramBootstrapABI {
			coroBootstrap = ctx.coroProgramBootstraps[pkg.ID]
			if coroBootstrap == nil {
				return fmt.Errorf("coroutine program bootstrap: no pre-codegen table was frozen for linked package %q", pkg.ID)
			}
			coroBootstrap, err = bindCoroProgramBootstrapV2(coroBootstrap, linkedOrder)
			if err != nil {
				return fmt.Errorf("bind coroutine program bootstrap: %w", err)
			}
		}
		coroManifestHash, err = coroProgramManifestHashV1(ctx, coroRootAnchors, coroBootstrap)
		if err != nil {
			return err
		}
	}
	entryPkg := genMainModule(ctx, llssa.PkgRuntime, pkg, &genConfig{
		rtInit:           needRuntime,
		pyInit:           needPyInit,
		abiInit:          needAbiInit,
		coroRootAnchors:  coroRootAnchors,
		coroManifestHash: coroManifestHash,
		coroBootstrap:    coroBootstrap,
		methodByIndex:    methodByIndex,
		methodByName:     methodByName,
		abiSymbols:       linkedModuleGlobals(linkedOrder),
		funcInfo:         funcInfo,
		pcLineInfo:       pcLineInfo,
		funcInfoStubs:    funcInfoStubs,
	})
	if err := lowerCoroControlWrappers(ctx, entryPkg.LPkg); err != nil {
		return err
	}
	entryObjFile, err := exportObject(ctx, "entry_main", entryPkg.ExportFile, entryPkg.LPkg)
	if err != nil {
		return err
	}
	linkInputs := []string{entryObjFile}

	// Compile extra files from target configuration
	extraObjFiles, err := compileExtraFiles(ctx, verbose)
	if err != nil {
		return err
	}
	linkInputs = append(linkInputs, extraObjFiles...)
	linkInputs = append(linkInputs, archiveInputs...)

	if IsFullRpathEnabled() {
		// Treat every link-time library search path, specified by the -L parameter, as a runtime search path as well.
		// This is to ensure the final executable can locate libraries with a relocatable install_name
		// (e.g., "@rpath/libfoo.dylib") at runtime.
		rpaths := make(map[string]none)
		for _, arg := range linkArgs {
			if strings.HasPrefix(arg, "-L") {
				path := arg[2:]
				if _, ok := rpaths[path]; ok {
					continue
				}
				rpaths[path] = none{}
				linkArgs = append(linkArgs, "-rpath", path)
			}
		}
	}

	err = linkObjFiles(ctx, outputPath, linkInputs, linkArgs, verbose)
	if err != nil {
		return err
	}

	return nil
}

func linkedModuleGlobals(pkgs []Package) map[string]none {
	if len(pkgs) == 0 {
		return nil
	}
	seen := make(map[string]none)
	for _, pkg := range pkgs {
		if pkg == nil || pkg.LPkg == nil {
			continue
		}
		for g := pkg.LPkg.Module().FirstGlobal(); !g.IsNil(); g = gllvm.NextGlobal(g) {
			if g.IsDeclaration() {
				continue
			}
			seen[g.Name()] = none{}
		}
	}
	return seen
}

// isRuntimePkg reports whether the package path belongs to the llgo runtime tree.
func isRuntimePkg(pkgPath string) bool {
	rtRoot := env.LLGoRuntimePkg
	return pkgPath == rtRoot || strings.HasPrefix(pkgPath, rtRoot+"/")
}

func linkObjFiles(ctx *context, app string, objFiles, linkArgs []string, verbose bool) error {
	printCmds := ctx.shouldPrintCommands(verbose)
	// Handle c-archive mode differently - use ar tool instead of linker
	if ctx.buildConf.BuildMode == BuildModeCArchive {
		return ctx.createArchiveFile(app, objFiles, printCmds)
	}

	buildArgs := []string{"-o", app}
	buildArgs = append(buildArgs, linkArgs...)
	ltoPluginFlags, err := ctx.buildConf.LTOPlugin.LinkerFlags(ctx.buildConf.Goos)
	if err != nil {
		return err
	}
	buildArgs = append(buildArgs, ltoPluginFlags...)

	// Add build mode specific linker arguments
	switch ctx.buildConf.BuildMode {
	case BuildModeCShared:
		buildArgs = append(buildArgs, "-shared", "-fPIC")
	case BuildModeExe:
		if needsLinuxNoPIE(ctx, linkArgs) {
			buildArgs = append(buildArgs, "-no-pie")
		}
		buildArgs = append(buildArgs, linuxExportDynamicArgs(ctx)...)
	}

	if IsDbgSymsEnabled() {
		buildArgs = append(buildArgs, "-gdwarf-4")
	}

	if ctx.buildConf.GenLL {
		var compiledObjFiles []string
		for _, objFile := range objFiles {
			if strings.HasSuffix(objFile, ".ll") {
				oFile := strings.TrimSuffix(objFile, ".ll") + ".o"
				args := []string{"-o", oFile, "-c", objFile, "-Wno-override-module"}
				if printCmds {
					fmt.Fprintln(os.Stderr, "clang", args)
				}
				if err := ctx.compiler().Compile(args...); err != nil {
					return fmt.Errorf("failed to compile %s: %v", objFile, err)
				}
				compiledObjFiles = append(compiledObjFiles, oFile)
			} else {
				compiledObjFiles = append(compiledObjFiles, objFile)
			}
		}
		objFiles = compiledObjFiles
	}

	buildArgs = append(buildArgs, objFiles...)

	cmd := ctx.linker()
	cmd.Verbose = printCmds
	return cmd.Link(buildArgs...)
}

func needsLinuxNoPIE(ctx *context, linkArgs []string) bool {
	if ctx.buildConf.Target != "" || ctx.buildConf.Goos != "linux" {
		return false
	}
	// Host Linux toolchains commonly default to PIE executables, which can
	// break runtime assumptions unless the user explicitly requested a PIE mode.
	for _, arg := range linkArgs {
		if arg == "-pie" || arg == "-static-pie" || arg == "-no-pie" || arg == "-nopie" {
			return false
		}
	}
	return true
}

func needsLinuxExportDynamic(ctx *context) bool {
	return ctx.buildConf.Target == "" && ctx.buildConf.Goos == "linux" && IsFuncInfoEnabled()
}

func linuxExportDynamicArgs(ctx *context) []string {
	if !needsLinuxExportDynamic(ctx) {
		return nil
	}
	return []string{
		"-Wl,--export-dynamic-symbol=main.*",
		"-Wl,--export-dynamic-symbol=command-line-arguments.*",
	}
}

// archiver returns the archiving tool to use for the current context.
// For wasm targets and LTO builds, it prefers llvm-ar because linkers need
// LLVM-aware archive indexes for wasm objects and bitcode members.
func (c *context) archiver() string {
	// First check toolchain directory (for cross-compilation)
	if c.crossCompile.CC != "" {
		clangDir := filepath.Dir(c.crossCompile.CC)
		if clangDir != "" {
			llvmAr := filepath.Join(clangDir, "llvm-ar")
			if _, err := os.Stat(llvmAr); err == nil {
				return llvmAr
			}
		}
	}
	// Allow user override
	if ar := os.Getenv("LLGO_AR"); ar != "" {
		return ar
	}
	if c.buildConf.ltoEnabled() || c.buildConf.Goarch == "wasm" || strings.Contains(c.crossCompile.LLVMTarget, "wasm") {
		if llvmAr, err := exec.LookPath("llvm-ar"); err == nil {
			return llvmAr
		}
	}
	return "ar"
}

// createArchiveFile builds an archive at archivePath atomically to avoid races when
// multiple builds target the same output concurrently.
func (c *context) createArchiveFile(archivePath string, objFiles []string, verbose ...bool) error {
	if len(objFiles) == 0 {
		return fmt.Errorf("no object files provided for archive %s", archivePath)
	}

	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(archivePath), filepath.Base(archivePath)+".tmp-*")
	if err != nil {
		return err
	}
	tmp.Close()
	tmpName := tmp.Name()
	// Remove the placeholder so ar can create the archive fresh.
	_ = os.Remove(tmpName)

	args := append([]string{"rcs", tmpName}, objFiles...)
	arCmd := c.archiver()
	cmd := exec.Command(arCmd, args...)
	printCmds := c.shouldPrintCommands(len(verbose) > 0 && verbose[0])
	if printCmds {
		fmt.Fprintf(os.Stderr, "%s %s\n", filepath.Base(arCmd), strings.Join(args, " "))
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("create archive %s: %w\n%s", archivePath, err, output)
	}

	if err := os.Rename(tmpName, archivePath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("publish archive %s: %w", archivePath, err)
	}
	return nil
}

func isWasmTarget(goos string) bool {
	return slices.Contains([]string{"wasi", "js", "wasip1"}, goos)
}

func needStart(ctx *context) bool {
	if ctx.buildConf.Target == "" {
		return !isWasmTarget(ctx.buildConf.Goos)
	}
	switch ctx.buildConf.Target {
	case "wasip2":
		return false
	default:
		// since newlib-esp32 provides _start, we don't need to provide a fake _start function
		return ctx.crossCompile.Libc != "newlib-esp32"
	}
}

func is32Bits(goarch string) bool {
	return goarch == "386" || goarch == "arm" || goarch == "mips" || goarch == "wasm"
}

func buildPkg(ctx *context, aPkg *aPackage, verbose bool) error {
	pkg := aPkg.Package
	pkgPath := pkg.PkgPath
	if debugBuild || verbose {
		fmt.Fprintln(os.Stderr, pkgPath)
	}
	if llruntime.SkipToBuild(pkgPath) {
		pkg.ExportFile = ""
		return nil
	}
	var syntax = pkg.Syntax
	if altPkg := aPkg.AltPkg; altPkg != nil {
		syntax = append(syntax, altPkg.Syntax...)
	}
	showDetail := verbose && pkgExists(ctx.initial, pkg)
	if showDetail {
		llssa.SetDebug(llssa.DbgFlagAll)
		cl.SetDebug(cl.DbgFlagAll)
		defer func() {
			llssa.SetDebug(0)
			cl.SetDebug(0)
		}()
	}

	embedMap, err := goembed.LoadDirectives(ctx.conf.Fset, syntax)
	if err != nil {
		return fmt.Errorf("load go:embed directives for %s failed: %w", pkgPath, err)
	}

	ret, externs, err := cl.NewPackageExWithEmbedOptions(ctx.prog, ctx.callerTracking, ctx.patches, aPkg.rewriteVars, aPkg.SSA, syntax, embedMap, cl.PackageOptions{
		Compilation: ctx.clCompilation,
		CacheHit:    aPkg.CacheHit,
	})
	if err != nil {
		return fmt.Errorf("compile package %s: %w", pkgPath, err)
	}

	aPkg.LPkg = ret
	emittedCoroRootAnchor := ret.CoroRootPackageAnchor()
	if aPkg.CacheHit {
		if aPkg.CoroRootAnchorV1 != emittedCoroRootAnchor {
			return fmt.Errorf(
				"cached package %s coroutine root anchor %q does not match frontend registration %q",
				pkgPath, aPkg.CoroRootAnchorV1, emittedCoroRootAnchor,
			)
		}
	} else {
		aPkg.CoroRootAnchorV1 = emittedCoroRootAnchor
	}
	if hook := ctx.buildConf.ModuleHook; hook != nil {
		hook(aPkg)
	}

	// A cache hit reconstructed frontend registrations and link-time metadata;
	// the archived module already owns C ABI transformation, optimization, and
	// object emission, so discard this transient frontend module here.
	if aPkg.CacheHit {
		return nil
	}

	ctx.cTransformer.SetSkipFuncs(cabiSkipFuncsForPlan9Asm(ctx, pkgPath, ret.Module()))
	ctx.cTransformer.TransformModule(ret.Path(), ret.Module())
	ctx.cTransformer.SetSkipFuncs(nil)

	// Run the default LLVM optimization pipeline selected by the requested -O level.
	if ctx.passOpt {
		mod := ret.Module()
		mod.SetDataLayout(ctx.prog.DataLayout())
		mod.SetTarget(ctx.prog.TargetSpec().Triple)
		pbo := gllvm.NewPassBuilderOptions()
		defer pbo.Dispose()
		if err = gllvm.VerifyModule(mod, gllvm.ReturnStatusAction); err != nil {
			return err
		}
		if err := mod.RunPasses(llvmPassPipeline(ctx.buildConf.OptLevel, ctx.buildConf.ltoMode()), ctx.prog.TargetMachine(), pbo); err != nil {
			return fmt.Errorf("run LLVM passes failed for %v: %v", pkgPath, err)
		}
	}
	emitFuncInfoEntrySites(ctx, ret)
	emitFuncInfoStubSites(ctx, ret)

	printCmds := ctx.shouldPrintCommands(verbose)
	cgoLLFiles, cgoLdflags, err := buildCgo(ctx, aPkg, aPkg.Package.Syntax, externs, printCmds)
	if err != nil {
		return fmt.Errorf("build cgo of %v failed: %v", pkgPath, err)
	}
	aPkg.ObjFiles = append(aPkg.ObjFiles, cgoLLFiles...)
	aPkg.ObjFiles = append(aPkg.ObjFiles, concatPkgLinkFiles(ctx, pkg, printCmds)...)
	if aPkg.AltPkg == nil || llruntime.HasAdditiveAltPkg(pkgPath) {
		if asmObjFiles, err := compilePkgSFiles(ctx, aPkg, pkg, printCmds); err != nil {
			return err
		} else {
			aPkg.ObjFiles = append(aPkg.ObjFiles, asmObjFiles...)
		}
	}
	if aliasObjs, err := buildGoCgoAliasObjects(ctx, pkgPath, aPkg.Package.Syntax, printCmds); err != nil {
		return err
	} else {
		aPkg.ObjFiles = append(aPkg.ObjFiles, aliasObjs...)
	}
	aPkg.LinkArgs = append(aPkg.LinkArgs, cgoLdflags...)
	aPkg.LinkArgs = append(aPkg.LinkArgs, goCgoLinkArgs(ctx.buildConf.Goos, aPkg.Package.Syntax)...)
	if aPkg.AltPkg != nil {
		altLLFiles, altLdflags, e := buildCgo(ctx, aPkg, aPkg.AltPkg.Syntax, externs, printCmds)
		if e != nil {
			return fmt.Errorf("build cgo of %v failed: %v", pkgPath, e)
		}
		aPkg.ObjFiles = append(aPkg.ObjFiles, altLLFiles...)
		aPkg.ObjFiles = append(aPkg.ObjFiles, concatPkgLinkFiles(ctx, aPkg.AltPkg.Package, printCmds)...)
		if asmObjFiles, err := compilePkgSFiles(ctx, aPkg, aPkg.AltPkg.Package, printCmds); err != nil {
			return err
		} else {
			aPkg.ObjFiles = append(aPkg.ObjFiles, asmObjFiles...)
		}
		if aliasObjs, err := buildGoCgoAliasObjects(ctx, pkgPath, aPkg.AltPkg.Syntax, printCmds); err != nil {
			return err
		} else {
			aPkg.ObjFiles = append(aPkg.ObjFiles, aliasObjs...)
		}
		aPkg.LinkArgs = append(aPkg.LinkArgs, altLdflags...)
		aPkg.LinkArgs = append(aPkg.LinkArgs, goCgoLinkArgs(ctx.buildConf.Goos, aPkg.AltPkg.Syntax)...)
	}
	if pkg.ExportFile != "" {
		exportFile, err := exportObject(ctx, pkg.PkgPath, pkg.ExportFile, ret)
		if err != nil {
			return fmt.Errorf("export object of %v failed: %v", pkgPath, err)
		}
		aPkg.ObjFiles = append(aPkg.ObjFiles, exportFile)
		if debugBuild || verbose {
			fmt.Fprintf(os.Stderr, "==> Export %s: %s\n", aPkg.PkgPath, pkg.ExportFile)
		}
	}
	return nil
}

func exportObject(ctx *context, pkgPath string, exportFile string, pkg llssa.Package) (string, error) {
	if useInMemoryNativeCodegen(ctx) {
		return exportObjectInMemory(ctx, pkgPath, exportFile, pkg)
	}
	return exportObjectWithClang(ctx, pkgPath, exportFile, []byte(pkg.String()))
}

func useInMemoryNativeCodegen(ctx *context) bool {
	return useInMemoryNativeCodegenConf(ctx.buildConf)
}

func useInMemoryNativeCodegenConf(conf *Config) bool {
	return conf != nil && !conf.GenLL &&
		conf.Target == "" &&
		conf.Goos == runtime.GOOS &&
		conf.Goarch == runtime.GOARCH &&
		conf.Goarch != "wasm"
}

func dumpLLVMIRIfNeeded(ctx *context, pkgPath string, exportFile string, data string) error {
	if !ctx.buildConf.CheckLLFiles && !ctx.buildConf.GenLL {
		return nil
	}

	base := filepath.Base(exportFile)
	f, err := os.CreateTemp("", base+"-*.ll")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(data); err != nil {
		f.Close()
		return err
	}
	err = f.Close()
	if err != nil {
		return err
	}
	if ctx.buildConf.CheckLLFiles {
		if msg, err := llcCheck(ctx.env, f.Name()); err != nil {
			fmt.Fprintf(os.Stderr, "==> llc %v: %v\n%v\n", pkgPath, f.Name(), msg)
		}
	}
	// If GenLL is enabled, keep a copy of the .ll file for debugging
	if ctx.buildConf.GenLL {
		llFile := exportFile + ".ll"
		if err := os.Chmod(f.Name(), 0644); err != nil {
			return err
		}
		if err := copyFileAtomic(f.Name(), llFile); err != nil {
			return err
		}
	}
	return nil
}

func exportObjectInMemory(ctx *context, pkgPath string, exportFile string, pkg llssa.Package) (string, error) {
	if ctx.buildConf.CheckLLFiles || ctx.buildConf.GenLL {
		// Avoid formatting large IR unless a debug/check path needs it.
		if err := dumpLLVMIRIfNeeded(ctx, pkgPath, exportFile, pkg.String()); err != nil {
			return "", err
		}
	}
	ltoMode := ctx.buildConf.ltoMode()
	var (
		buf  gllvm.MemoryBuffer
		err  error
		kind = "in-memory LLVM object emission"
	)
	switch ltoMode {
	case lto.Full:
		// reference to https: //github.com/espressif/llvm-project/blob/04a1a3482ce3ee00b5bbec1ce852e58410e4b6ad/clang/lib/CodeGen/BackendUtil.cpp#L197
		// Clang emit SplitLTOUnit for full lto bitcode except on darwin.
		buf = gllvm.WriteFullLTOBitcodeToMemoryBuffer(pkg.Module(), ctx.buildConf.Goos != "darwin")
		kind = "in-memory LLVM full LTO bitcode emission"
	case lto.Thin:
		buf = gllvm.WriteThinLTOBitcodeToMemoryBuffer(pkg.Module())
		kind = "in-memory LLVM ThinLTO bitcode emission"
	default:
		buf, err = ctx.prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), gllvm.ObjectFile)
		if err != nil {
			return "", err
		}
	}
	defer buf.Dispose()

	base := filepath.Base(exportFile)
	objFile, err := os.CreateTemp("", base+"-*.o")
	if err != nil {
		return "", err
	}
	objFileName := objFile.Name()
	if ctx.shouldPrintCommands(false) {
		fmt.Fprintf(os.Stderr, "# compiling %s for pkg: %s\n", objFileName, pkgPath)
		fmt.Fprintf(os.Stderr, "# using %s\n", kind)
	}
	if _, err := objFile.Write(buf.Bytes()); err != nil {
		objFile.Close()
		os.Remove(objFileName)
		return "", err
	}
	if err := objFile.Close(); err != nil {
		os.Remove(objFileName)
		return "", err
	}
	return objFileName, nil
}

func exportObjectWithClang(ctx *context, pkgPath string, exportFile string, data []byte) (string, error) {
	base := filepath.Base(exportFile)
	f, err := os.CreateTemp("", base+"-*.ll")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	err = f.Close()
	if err != nil {
		return exportFile, err
	}
	if ctx.buildConf.CheckLLFiles {
		if msg, err := llcCheck(ctx.env, f.Name()); err != nil {
			fmt.Fprintf(os.Stderr, "==> llc %v: %v\n%v\n", pkgPath, f.Name(), msg)
		}
	}
	if ctx.buildConf.GenLL {
		llFile := exportFile + ".ll"
		if err := os.Chmod(f.Name(), 0644); err != nil {
			return "", err
		}
		// Copy instead of rename so we can still compile to .o
		if err := copyFileAtomic(f.Name(), llFile); err != nil {
			return "", err
		}
	}
	objFile, err := os.CreateTemp("", base+"-*.o")
	if err != nil {
		return "", err
	}
	objFile.Close()
	args := []string{"-o", objFile.Name(), "-c", f.Name(), "-Wno-override-module"}
	if ctx.shouldPrintCommands(false) {
		fmt.Fprintf(os.Stderr, "# compiling %s for pkg: %s\n", f.Name(), pkgPath)
		fmt.Fprintln(os.Stderr, "clang", args)
	}
	cmd := ctx.compiler()
	return objFile.Name(), cmd.Compile(args...)
}

func llcCheck(env *llvm.Env, exportFile string) (msg string, err error) {
	bin := filepath.Join(env.BinDir(), "llc")
	cmd := exec.Command(bin, "-filetype=null", exportFile)
	var buf bytes.Buffer
	cmd.Stderr = &buf
	if err = cmd.Run(); err != nil {
		msg = buf.String()
	}
	return
}

const (
	altPkgPathPrefix = abi.PatchPathPrefix
)

func altPkgs(initial []*packages.Package, conf *Config, alts ...string) []string {
	packages.Visit(initial, nil, func(p *packages.Package) {
		if p.Types != nil && !p.IllTyped {
			if hasAltPkgForTarget(conf, p.PkgPath) {
				alts = append(alts, altPkgPathPrefix+p.PkgPath)
			}
		}
	})
	return alts
}

func preCollectRuntimeLinknames(prog llssa.Program, pkgs []*packages.Package) {
	for _, pkg := range pkgs {
		if pkg != nil && pkg.PkgPath == llssa.PkgRuntime && len(pkg.Syntax) != 0 {
			cl.PreCollectLinknames(prog, pkg.PkgPath, pkg.Syntax)
			return
		}
	}
}

func altSSAPkgs(prog *ssa.Program, patches cl.Patches, alts []*packages.Package, conf *Config, verbose bool) {
	packages.Visit(alts, nil, func(p *packages.Package) {
		if typs := p.Types; typs != nil && !p.IllTyped {
			if debugBuild || verbose {
				log.Println("==> BuildSSA", p.ID)
			}
			pkgSSA := prog.CreatePackage(typs, p.Syntax, p.TypesInfo, true)
			if strings.HasPrefix(p.ID, altPkgPathPrefix) {
				path := p.ID[len(altPkgPathPrefix):]
				// Even if an alt package exists and is pulled in as a dependency of other
				// patches (e.g. runtime/reflect), we should only apply it when it is
				// enabled for the target (and not overridden by Plan9 asm translation).
				if !hasAltPkgForTarget(conf, path) {
					return
				}
				patches[path] = cl.Patch{Alt: pkgSSA, Types: typepatch.Clone(typs)}
				if debugBuild || verbose {
					log.Println("==> Patching", path)
				}
			}
		}
	})
	prog.Build()
}

type aPackage struct {
	*packages.Package
	SSA    *ssa.Package
	AltPkg *packages.Cached
	LPkg   llssa.Package

	NeedRt     bool
	NeedPyInit bool

	LinkArgs    []string
	ObjFiles    []string // object files: .o or .ll (output of compiler, input to archiver)
	ArchiveFile string   // archive file: .a (output of archiver, used for linking)
	rewriteVars map[string]string

	// Cache related fields
	Fingerprint      string // fingerprint digest
	Manifest         string // manifest text content
	CoroRootAnchorV1 string // linker-visible coroutine root package anchor
	CacheHit         bool   // whether cache was hit
}

type Package = *aPackage

func buildSSAPkgs(ctx *context, initial []*packages.Package, verbose bool) ([]*aPackage, error) {
	prog := ctx.progSSA
	var all []*aPackage
	var errs []*packages.Package
	packages.Visit(initial, nil, func(p *packages.Package) {
		if p.Types != nil && !p.IllTyped {
			pkgPath := p.PkgPath
			// Use p.ID to check duplicates since same pkgPath may have different IDs
			if _, ok := ctx.pkgByID[p.ID]; ok || strings.HasPrefix(pkgPath, altPkgPathPrefix) {
				return
			}
			var altPkg *packages.Cached
			var ssaPkg = createSSAPkg(ctx, prog, p, verbose)
			if ctx.hasAltPkg(pkgPath) {
				if altPkg = ctx.dedup.Check(altPkgPathPrefix + pkgPath); altPkg == nil {
					return
				}
			}
			rewrites := collectRewriteVars(ctx, pkgPath)
			aPkg := &aPackage{
				Package:     p,
				SSA:         ssaPkg,
				AltPkg:      altPkg,
				LPkg:        nil,
				NeedRt:      false,
				NeedPyInit:  false,
				LinkArgs:    nil,
				ObjFiles:    nil,
				rewriteVars: rewrites,
			}
			ctx.pkgs[p] = aPkg
			ctx.pkgByID[p.ID] = aPkg
			all = append(all, aPkg)
		} else {
			errs = append(errs, p)
		}
	})
	if len(errs) > 0 {
		for _, errPkg := range errs {
			for _, err := range errPkg.Errors {
				fmt.Fprintln(os.Stderr, formatPackageError(err, ctx.buildConf.NoErrorColumn))
			}
			fmt.Fprintln(os.Stderr, "cannot build SSA for package", errPkg)
		}
		return nil, fmt.Errorf("cannot build SSA for packages")
	}
	return all, nil
}

func formatPackageError(err packages.Error, noColumn bool) string {
	formatted := err.Error()
	if !noColumn {
		return formatted
	}
	if pos, ok := positionWithoutColumn(err.Pos); ok {
		return pos + ": " + err.Msg
	}
	lines := strings.Split(formatted, "\n")
	for i, line := range lines {
		if line, ok := diagnosticWithoutColumn(line); ok {
			lines[i] = line
		}
	}
	return strings.Join(lines, "\n")
}

func positionWithoutColumn(pos string) (string, bool) {
	lastColon := strings.LastIndexByte(pos, ':')
	if lastColon < 0 {
		return "", false
	}
	if _, parseErr := strconv.Atoi(pos[lastColon+1:]); parseErr != nil {
		return "", false
	}
	linePos := pos[:lastColon]
	lineColon := strings.LastIndexByte(linePos, ':')
	if lineColon < 0 {
		return "", false
	}
	if _, parseErr := strconv.Atoi(linePos[lineColon+1:]); parseErr != nil {
		return "", false
	}
	return linePos, true
}

func diagnosticWithoutColumn(line string) (string, bool) {
	separator := strings.Index(line, ": ")
	if separator < 0 {
		return "", false
	}
	pos, ok := positionWithoutColumn(line[:separator])
	if !ok {
		return "", false
	}
	return pos + line[separator:], true
}

func collectRewriteVars(ctx *context, pkgPath string) map[string]string {
	data := ctx.buildConf.GlobalRewrites
	if len(data) == 0 {
		return nil
	}
	basePath := strings.TrimPrefix(pkgPath, altPkgPathPrefix)
	if vars := data[basePath]; vars != nil {
		return cloneRewrites(vars)
	}
	if vars := data[pkgPath]; vars != nil {
		return cloneRewrites(vars)
	}
	return nil
}

func cloneRewrites(src Rewrites) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dup := make(map[string]string, len(src))
	for k, v := range src {
		dup[k] = v
	}
	return dup
}

func toTypeList(args *types.TypeList) []types.Type {
	if args == nil {
		return nil
	}
	result := make([]types.Type, args.Len())
	for i := 0; i < args.Len(); i++ {
		result[i] = args.At(i)
	}
	return result
}

// fixUntypedShiftTypes fixes a bug in go/types where non-constant shift expressions
// with untyped constant left operands have type "untyped int" instead of "int".
//
// According to the Go spec: "If the left operand of a non-constant shift expression
// is an untyped constant, it is first implicitly converted to the type it would assume
// if the shift expression were replaced by its left operand alone."
//
// Parent expressions can inherit that untyped result. This causes go/ssa sanity
// check to fail when a non-constant instruction result remains untyped.
// See: https://github.com/golang/go/issues/77067
func fixUntypedShiftTypes(p *packages.Package) {
	var toFix []ast.Expr
	for expr, tv := range p.TypesInfo.Types {
		if tv.Value != nil {
			continue
		}
		basic, ok := tv.Type.(*types.Basic)
		if !ok || basic.Info()&types.IsUntyped == 0 {
			continue
		}
		toFix = append(toFix, expr)
	}

	for _, expr := range toFix {
		tv := p.TypesInfo.Types[expr]
		p.TypesInfo.Types[expr] = types.TypeAndValue{
			Type:  types.Default(tv.Type),
			Value: tv.Value,
		}
	}
}

func applyPatches(ctx *context, p *packages.Package, verbose bool) {
	// Fix untyped shift types before SSA build
	// See: https://github.com/golang/go/issues/77067
	fixUntypedShiftTypes(p)

	// fix instance patch
	for id, inst := range p.TypesInfo.Instances {
		if obj := p.TypesInfo.Uses[id]; obj != nil && obj.Pkg() != nil && obj.Pkg() != p.Types {
			if pkg := obj.Pkg(); pkg != nil && pkg != p.Types {
				if patch, ok := ctx.patches[pkg.Path()]; ok {
					if robj := patch.Alt.Pkg.Scope().Lookup(obj.Name()); robj != nil {
						typ, err := types.Instantiate(nil, robj.Type(), toTypeList(inst.TypeArgs), true)
						if err != nil {
							if debugBuild || verbose {
								log.Printf("==> Instance patch failed for %q: %v\n", obj.Id(), err)
							}
							continue
						}
						inst.Type = typ
						p.TypesInfo.Instances[id] = inst
						p.TypesInfo.Uses[id] = robj
					}
				}
			}
		}
	}
}

func createSSAPkg(ctx *context, prog *ssa.Program, p *packages.Package, verbose bool) *ssa.Package {
	pkgSSA := prog.ImportedPackage(p.ID)
	if pkgSSA == nil {
		if debugBuild || verbose {
			log.Println("==> BuildSSA", p.ID)
		}
		applyPatches(ctx, p, verbose)
		pkgSSA = prog.CreatePackage(p.Types, p.Syntax, p.TypesInfo, true)
		pkgSSA.Build() // TODO(xsw): build concurrently
		// Apply local SSA fixups once when package SSA is first built.
		fixSSAOrder(pkgSSA, p.Syntax)
	}
	return pkgSSA
}

/*
var (
	// TODO(xsw): complete build flags
	buildFlags = map[string]bool{
		"-C":         true,  // -C dir: Change to dir before running the command
		"-a":         false, // -a: force rebuilding of packages that are already up-to-date
		"-n":         false, // -n: print the commands but do not run them
		"-p":         true,  // -p n: the number of programs to run in parallel
		"-race":      false, // -race: enable data race detection
		"-cover":     false, // -cover: enable coverage analysis
		"-covermode": true,  // -covermode mode: set the mode for coverage analysis
		"-v":         false, // -v: print the names of packages as they are compiled
		"-work":      false, // -work: print the name of the temporary work directory and do not delete it when exiting
		"-x":         false, // -x: print the commands
		"-tags":      true,  // -tags 'tag,list': a space-separated list of build tags to consider satisfied during the build
		"-pkgdir":    true,  // -pkgdir dir: install and load all packages from dir instead of the usual locations
		"-ldflags":   true,  // --ldflags 'flag list': arguments to pass on each go tool link invocation
	}
)
*/

const llgoDebug = "LLGO_DEBUG"
const llgoDbgSyms = "LLGO_DEBUG_SYMBOLS"
const llgoFuncInfo = "LLGO_FUNCINFO"
const llgoFuncInfoSites = "LLGO_FUNCINFO_SITES"
const llgoTrace = "LLGO_TRACE"
const llgoOptimize = "LLGO_OPTIMIZE"
const llgoWasmRuntime = "LLGO_WASM_RUNTIME"
const llgoWasiThreads = "LLGO_WASI_THREADS"
const llgoStdioNobuf = "LLGO_STDIO_NOBUF"
const llgoFullRpath = "LLGO_FULL_RPATH"
const llgoBuildCache = "LLGO_BUILD_CACHE"

// for Plan9 asm translation debug
const llgoPlan9ASMPkgs = "LLGO_PLAN9ASM_PKGS"

const defaultWasmRuntime = "wasmtime"

func defaultEnv(env string, defVal string) string {
	envVal := os.Getenv(env)
	if envVal == "" {
		return defVal
	}
	return envVal
}

func isEnvOn(env string, defVal bool) bool {
	envVal := strings.ToLower(os.Getenv(env))
	if envVal == "" {
		return defVal
	}
	return envVal == "1" || envVal == "true" || envVal == "on"
}

// cacheEnabled checks if build cache is enabled.
// Cache can be disabled by setting LLGO_BUILD_CACHE=off|0
func cacheEnabled() bool {
	return isEnvOn(llgoBuildCache, true)
}

func IsTraceEnabled() bool {
	return isEnvOn(llgoTrace, false)
}

func IsStdioNobuf() bool {
	return isEnvOn(llgoStdioNobuf, false)
}

func IsDbgEnabled() bool {
	return isEnvOn(llgoDebug, false) || isEnvOn(llgoDbgSyms, false)
}

func IsFuncInfoEnabled() bool {
	return isEnvOn(llgoFuncInfo, true)
}

// IsFuncInfoSitesEnabled controls the body-embedded site records
// independently of the funcinfo tables (LLGO_FUNCINFO_SITES=0 keeps the
// metadata but drops entry/stub/pc-line inline-asm sites). Useful for
// isolating codegen perturbation caused by the in-body asm anchors.
func IsFuncInfoSitesEnabled() bool {
	return isEnvOn(llgoFuncInfoSites, true)
}

func IsDbgSymsEnabled() bool {
	return isEnvOn(llgoDbgSyms, false)
}

func IsOptimizeEnabled() bool {
	return isEnvOn(llgoOptimize, true)
}

func effectiveOptLevel(conf *Config) optlevel.Level {
	if conf != nil && conf.OptLevel.IsValid() {
		return conf.OptLevel
	}
	if conf != nil && conf.Target != "" {
		return optlevel.Oz
	}
	return optlevel.O2
}

func llvmPassPipeline(level optlevel.Level, ltoMode lto.Mode) string {
	switch ltoMode {
	case lto.Full:
		return "lto-pre-link<" + level.Name() + ">"
	case lto.Thin:
		return "thinlto-pre-link<" + level.Name() + ">"
	default:
		return "default<" + level.Name() + ">"
	}
}

func IsWasiThreadsEnabled() bool {
	return isEnvOn(llgoWasiThreads, true)
}

func IsFullRpathEnabled() bool {
	return isEnvOn(llgoFullRpath, true)
}

func Plan9ASMPkgs() string {
	return defaultEnv(llgoPlan9ASMPkgs, "")
}

func WasmRuntime() string {
	return defaultEnv(llgoWasmRuntime, defaultWasmRuntime)
}

func concatPkgLinkFiles(ctx *context, pkg *packages.Package, verbose bool) (parts []string) {
	llgoPkgLinkFiles(ctx, pkg, func(linkFile string) {
		parts = append(parts, linkFile)
	}, verbose)
	return
}

// const LLGoFiles = "file1; file2; ..."
func llgoPkgLinkFiles(ctx *context, pkg *packages.Package, procFile func(linkFile string), verbose bool) {
	if o := pkg.Types.Scope().Lookup("LLGoFiles"); o != nil {
		val := o.(*types.Const).Val()
		if val.Kind() == constant.String {
			clFiles(ctx, constant.StringVal(val), pkg, procFile, verbose)
		}
	}
}

// files = "file1; file2; ..."
// files = "$(pkg-config --cflags xxx): file1; file2; ..."
func clFiles(ctx *context, files string, pkg *packages.Package, procFile func(linkFile string), verbose bool) {
	dir := filepath.Dir(pkg.GoFiles[0])
	expFile := pkg.ExportFile
	args := make([]string, 0, 16)
	if strings.HasPrefix(files, "$") { // has cflags
		if pos := strings.IndexByte(files, ':'); pos > 0 {
			cflags := xenv.ExpandEnvToArgs(files[:pos])
			files = files[pos+1:]
			args = append(args, cflags...)
		}
	}
	for _, file := range strings.Split(files, ";") {
		cFile := filepath.Join(dir, strings.TrimSpace(file))
		clFile(ctx, args, cFile, expFile, pkg.PkgPath, procFile, verbose)
	}
}

func clFile(ctx *context, args []string, cFile, expFile, pkgPath string, procFile func(linkFile string), verbose bool) {
	baseName := expFile + filepath.Base(cFile)
	ext := filepath.Ext(cFile)

	// default clang++ will use c++ to compile c file,will cause symbol be mangled
	if ext == ".c" {
		args = append(args, "-x", "c")
	}

	// If GenLL is enabled, first emit .ll for debugging, then compile to .o
	printCmds := ctx.shouldPrintCommands(verbose)
	if ctx.buildConf.GenLL {
		llFile := baseName + ".ll"
		llArgs := append(slices.Clone(args), "-emit-llvm", "-S", "-o", llFile, "-c", cFile)
		if printCmds {
			fmt.Fprintf(os.Stderr, "# compiling %s for pkg: %s\n", llFile, pkgPath)
			fmt.Fprintln(os.Stderr, "clang", llArgs)
		}
		cmd := ctx.compiler()
		err := cmd.Compile(llArgs...)
		check(err)
	}

	// Always compile to .o for linking
	objFile := baseName + ".o"
	objArgs := append(args, "-o", objFile, "-c", cFile)
	if printCmds {
		fmt.Fprintf(os.Stderr, "# compiling %s for pkg: %s\n", objFile, pkgPath)
		fmt.Fprintln(os.Stderr, "clang", objArgs)
	}
	cmd := ctx.compiler()
	err := cmd.Compile(objArgs...)
	check(err)
	procFile(objFile)
}

func pkgExists(initial []*packages.Package, pkg *packages.Package) bool {
	for _, v := range initial {
		if v == pkg {
			return true
		}
	}
	return false
}

type none struct{}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

// -----------------------------------------------------------------------------
