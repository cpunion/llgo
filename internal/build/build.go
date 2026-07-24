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
	"maps"
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
	llabi "github.com/goplus/llgo/internal/abi"
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
	"github.com/goplus/llgo/internal/pclnmap"
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
	Out  string // Base output file path
	PCLN string // PCLN sidecar output file path (.pclntab)
	Bin  string // Binary output file path (.bin)
	Hex  string // Intel hex output file path (.hex)
	Img  string // Image output file path (.img)
	Uf2  string // UF2 output file path (.uf2)
	Zip  string // ZIP/DFU output file path (.zip)
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
	localBodyFacts                 func(*ssa.Function) (coro.SSAFunctionBodyFacts, error)
	functionBackground             func(*ssa.Function) (llssa.Background, bool, error)
	rawCFunctionType               func(types.Type) (bool, error)
	foreignNoBlock                 func(*ssa.Function) (cl.CoroForeignNoBlockCertificate, bool, error)
	foreignSync                    func(*ssa.Function) (cl.CoroForeignSyncCertificate, bool, error)
	foreignSchedulerWait           func(*ssa.Function) (cl.CoroForeignSchedulerWaitCertificate, bool, error)
	foreignWorker                  func(*ssa.Function) (cl.CoroForeignWorkerCertificate, bool, error)
	callableIdentity               func(*ssa.Function) (cl.CoroCallableIdentityCertificate, bool, error)
	callableContract               func(*ssa.Function) (cl.CoroCallableContractCertificate, bool, error)
	noPreempt                      func(*ssa.Function) (string, bool, error)
	noUnwind                       func(*ssa.Function) (string, bool, error)
	trustedInlineCall              func(*ssa.Function, ssa.CallInstruction) (coro.SSATrustedInlineCallCertificate, bool, error)
	assemblyNoSuspend              func(*ssa.Function) (string, bool, error)
	dynamicImplements              func(types.Type, *types.Interface) (bool, error)
	callSitePlan                   func(ssa.CallInstruction) (cl.CoroCallSitePlan, bool, error)
	rawFunctionAddressCallArgument func(ssa.CallInstruction, int) (bool, error)
	staticCodeAddressCallArgument  func(ssa.CallInstruction, int) (bool, error)
	demandReferences               func(*ssa.Function) ([]*ssa.Function, error)
	syncDemandReferences           func(*ssa.Function) ([]*ssa.Function, error)
	loweredCalls                   func(*ssa.Function) ([]coro.SSALoweredCall, error)
	requiredRoots                  coro.Roots
	requiredPlain                  map[*ssa.Function]struct{}
	requiredHostPlain              map[*ssa.Function]struct{}
	requiredDirectPlain            []requiredCoroDirectPlainCallArgument
	requiredClosedDynamic          map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate
	requiredGlobalFunctionSlots    map[ssa.CallInstruction]coroGlobalFunctionSlotProof
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

func applyFrozenCallableContractPolicy(
	fn *ssa.Function,
	frontendC bool,
	policy coro.SSAFunctionPolicy,
	certificate cl.CoroCallableContractCertificate,
) (coro.SSAFunctionPolicy, error) {
	if fn == nil {
		return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen callable contract has no exact function")
	}
	if err := certificate.Validate(); err != nil {
		return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen callable contract for %q is invalid: %w", fn.Name(), err)
	}
	switch certificate.Scope {
	case coro.CallableContractScopeWrapper:
		if frontendC || fn == nil || len(fn.Blocks) == 0 {
			return coro.SSAFunctionPolicy{}, fmt.Errorf("callable wrapper contract for %q does not name one bodyful frontend Go function", fn.Name())
		}
		if policy.IgnoreBody || policy.OverrideExternal && policy.External != coro.Defined {
			return coro.SSAFunctionPolicy{}, fmt.Errorf("Go wrapper %q conflicts with its frozen callable contract", fn.Name())
		}
		// Wrapper scope never replaces body analysis.  It may add constraints
		// that are not recoverable from Go SSA (owner/host affinity, foreign
		// reentry, or retained memory), and the fixed-point verifier below checks
		// that an executor-safe/no-return summary agrees with the analyzed body.
		policy.Exec |= coro.CallableContractExecConstraints(certificate.Contract)
		if certificate.Contract.Progress == coro.ProgressNoReturn {
			policy.Exec |= coro.NoReturn
		}
		policy.CallableContractCertificate = certificate
		return policy, nil
	case coro.CallableContractScopeDeclaration:
		if !frontendC {
			return coro.SSAFunctionPolicy{}, fmt.Errorf("callable declaration contract for %q does not name one frontend C declaration", fn.Name())
		}
		external := coro.ExternalUnknownForeign
		exec := coro.BlockForeign | coro.IRQUnsafe | coro.CallableContractExecConstraints(certificate.Contract)
		switch certificate.Contract.Progress {
		case coro.ProgressExecutorSafe:
			external = coro.ExternalKnown
			exec &^= coro.BlockForeign
		case coro.ProgressMayBlock, coro.ProgressUnknown, coro.ProgressAsyncCompletion:
			// Auto keeps the exact foreign stack cut. Its ordinary managed
			// callers therefore inherit WaitForeign from CallForeign.
		case coro.ProgressNoReturn:
			// NoReturn is a control-flow fact, not proof that the operation is
			// safe to execute while owning an executor. Keep the foreign stack
			// cut until a separate terminal/owner recipe proves otherwise.
			exec |= coro.NoReturn
		default:
			return coro.SSAFunctionPolicy{}, fmt.Errorf("callable declaration %q has unsupported progress %q", fn.Name(), certificate.Contract.Progress)
		}
		if policy.Effect != coro.NoSuspend || policy.Exec != 0 || policy.NeedsDispatch ||
			policy.OverrideExternal && policy.External != external {
			return coro.SSAFunctionPolicy{}, fmt.Errorf("frontend C declaration %q conflicts with its frozen callable contract", fn.Name())
		}
		policy.IgnoreBody = true
		policy.External = external
		policy.OverrideExternal = true
		policy.Exec = exec
		policy.CallableContractCertificate = certificate
		return policy, nil
	default:
		return coro.SSAFunctionPolicy{}, fmt.Errorf("callable contract for %q has invalid scope %q", fn.Name(), certificate.Scope)
	}
}

// isCoroManagedDescriptorCall recognizes only the ordinary-call source shape
// for which cl owns the complete v1 {descriptor, environment} ABI. Spawn has a
// separate predicate because it selects only the coroutine capability, creates
// an independent scheduler G, and discards any source results as required by
// Go.
func isCoroManagedDescriptorCall(call ssa.CallInstruction) bool {
	direct, ok := call.(*ssa.Call)
	if !ok || direct == nil || direct.Common() == nil {
		return false
	}
	common := direct.Common()
	if common.StaticCallee() != nil || common.IsInvoke() || common.Method != nil {
		return false
	}
	if _, builtin := common.Value.(*ssa.Builtin); builtin {
		return false
	}
	sig := common.Signature()
	if sig == nil || sig.Recv() != nil ||
		typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 {
		return false
	}
	return true
}

// isCoroManagedInterfaceDescriptorCall recognizes the one source shape whose
// ABI Method.Ifn_ word cl replaces with the universal method descriptor. It is
// deliberately narrower than an arbitrary interface operation: defer/go
// invokes, foreign classifications, raw method addresses, and generic or
// variadic methods retain their independent fail-closed domains.
func isCoroManagedInterfaceDescriptorCall(call ssa.CallInstruction) bool {
	direct, ok := call.(*ssa.Call)
	if !ok || direct == nil || direct.Common() == nil {
		return false
	}
	common := direct.Common()
	if common.StaticCallee() != nil || !common.IsInvoke() || common.Method == nil {
		return false
	}
	if _, ok := types.Unalias(common.Value.Type()).Underlying().(*types.Interface); !ok {
		return false
	}
	sig := common.Signature()
	return sig != nil && !sig.Variadic() &&
		typeParamLen(sig.TypeParams()) == 0 && typeParamLen(sig.RecvTypeParams()) == 0
}

func isCoroManagedDescriptorSpawn(call ssa.CallInstruction) bool {
	spawn, ok := call.(*ssa.Go)
	if !ok || spawn == nil || spawn.Common() == nil {
		return false
	}
	common := spawn.Common()
	if common.IsInvoke() || common.Method != nil {
		return false
	}
	if _, builtin := common.Value.(*ssa.Builtin); builtin {
		return false
	}
	sig := common.Signature()
	return sig != nil && sig.Recv() == nil && !sig.Variadic() &&
		typeParamLen(sig.TypeParams()) == 0 && typeParamLen(sig.RecvTypeParams()) == 0
}

// Analyze applies the frozen emission universe to config before running the
// coroutine analysis. The frozen frontend patch-alias resolver is
// authoritative, so roots, body callees, function values, and later code
// generation all use the same exact *ssa.Function objects. The frontend's
// structural identity resolver is composed with builder identity policy.
// Builders use this helper instead of calling AnalyzeSSA directly.
func (in CoroPlanInput) Analyze(roots coro.Roots, config coro.SSAConfig) (*coro.SSAPlan, error) {
	// Keep the builder/frontend policy that exists before context-dependent
	// intrinsic effects are added. A raw-plain variant shares the source SSA
	// body, but an exact worker syscall call has different physical semantics:
	// the managed body parks its coroutine while the legacy body executes the
	// ordinary synchronous intrinsic. Source attribution is required here;
	// merely subtracting MayPark from the aggregate plan would also hide a real
	// channel/select park or a builder-supplied effect.
	rawPlainBasePolicyEffects := make(map[*ssa.Function]coro.Effect)
	if config.ClassifyLocalBody != nil {
		return nil, fmt.Errorf("build coroutine plan: builder cannot override frozen ProgramIR local body facts")
	}
	if in.localBodyFacts != nil {
		config.ClassifyLocalBody = in.localBodyFacts
	}
	if config.ClassifyConditionalManagedStoreReference != nil {
		// A Go global function cell is a managed descriptor publication. Raw
		// provenance belongs to exact invocation occurrences owned by the frontend
		// (funcAddr, raw callback arguments, or raw roots). Only the build-owned
		// complete global-slot proof may suppress demand from an ordinary Store.
		return nil, fmt.Errorf("build coroutine plan: builder cannot authorize conditional managed Store references")
	}
	conditionalStores, err := collectCoroGlobalFunctionSlotStores(in.requiredGlobalFunctionSlots)
	if err != nil {
		return nil, fmt.Errorf("build coroutine plan: freeze conditional global function-slot Stores: %w", err)
	}
	if len(conditionalStores) != 0 {
		config.ClassifyConditionalManagedStoreReference = func(owner *ssa.Function, store *ssa.Store) (*ssa.Function, bool, error) {
			proof, certified := conditionalStores[store]
			if !certified {
				return nil, false, nil
			}
			if owner == nil || proof.owner != owner || store == nil || store.Parent() != owner || proof.store != store || proof.target == nil {
				return nil, false, fmt.Errorf("frozen conditional global function-slot Store occurrence no longer matches its exact owner")
			}
			return proof.target, true, nil
		}
	}
	if config.DynamicImplements != nil {
		return nil, fmt.Errorf("build coroutine plan: builder cannot override the frozen frontend dynamic implementation relation")
	}
	config.DynamicImplements = in.dynamicImplements
	if config.ClassifyTrustedInlineCall != nil {
		return nil, fmt.Errorf("build coroutine plan: builder cannot manufacture trusted-inline invocation capabilities; the frozen frontend owns exact call-site proofs")
	}
	config.ClassifyTrustedInlineCall = in.trustedInlineCall
	if config.ClassifyRawPlainCall != nil {
		return nil, fmt.Errorf("build coroutine plan: builder cannot manufacture raw/plain invocation capabilities; the frozen ProgramIR owns exact call-site proofs")
	}
	config.ClassifyRawPlainCall = func(caller *ssa.Function, call ssa.CallInstruction) (coro.SSARawPlainCallCertificate, bool, error) {
		if in.callSitePlan == nil {
			return coro.SSARawPlainCallCertificate{}, false, nil
		}
		site, frozen, err := in.callSitePlan(call)
		if err != nil {
			return coro.SSARawPlainCallCertificate{}, false, err
		}
		if !frozen || !site.RawPlain {
			if frozen && site.RawPlainCertificate != "" {
				return coro.SSARawPlainCallCertificate{}, false, fmt.Errorf(
					"call in %q has raw/plain certificate data without the raw/plain policy",
					caller.Name(),
				)
			}
			return coro.SSARawPlainCallCertificate{}, false, nil
		}
		if site.RawPlainCertificate == "" {
			return coro.SSARawPlainCallCertificate{}, false, fmt.Errorf(
				"raw/plain call in %q has an empty frozen certificate",
				caller.Name(),
			)
		}
		return coro.SSARawPlainCallCertificate{ID: site.RawPlainCertificate}, true, nil
	}
	// Only the frozen frontend may certify the universal descriptor ABI. A plan
	// builder can still distinguish an unresolved foreign boundary, but cannot
	// turn an arbitrary open call (notably an interface invoke or excluded static
	// declaration) into a managed descriptor call by policy alone.
	classifyUnknown := config.ClassifyUnknownCall
	if config.ClassifyRawCFunctionType != nil {
		return nil, fmt.Errorf("build coroutine plan: builder cannot manufacture raw C function transport; frozen frontend type metadata is authoritative")
	}
	config.ClassifyRawCFunctionType = in.rawCFunctionType
	config.ClassifyUnknownCall = func(caller *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
		if call != nil && call.Common() != nil && call.Common().StaticCallee() == nil && !call.Common().IsInvoke() &&
			in.rawCFunctionType != nil {
			rawC, err := in.rawCFunctionType(call.Common().Value.Type())
			if err != nil {
				return coro.UnknownManaged, fmt.Errorf("classify frozen raw C callee transport in %q: %w", caller.Name(), err)
			}
			if rawC {
				return coro.UnknownForeign, nil
			}
		}
		target := coro.UnknownManaged
		if classifyUnknown != nil {
			var err error
			target, err = classifyUnknown(caller, call)
			if err != nil {
				return coro.UnknownManaged, err
			}
		}
		if target == coro.UnknownManagedDispatch || target == coro.UnknownManagedInterfaceDispatch {
			return coro.UnknownManaged, fmt.Errorf("builder cannot certify managed descriptor dispatch in %q; the frozen frontend owns the function-value ABI", caller.Name())
		}
		if certificate, rawPlainClosed := in.requiredClosedDynamic[call]; rawPlainClosed && certificate.SyncDispatch {
			// Compiler-owned raw callbacks (currently TLS destructors) carry a
			// separate closed singleton/direct-plain certificate. They execute on
			// the scheduler or foreign stack and must never be recolored into the
			// managed descriptor/child-await domain.
			return target, nil
		}
		managedInterface := isCoroManagedInterfaceDescriptorCall(call)
		managedShape := managedInterface || isCoroManagedDescriptorCall(call) ||
			isCoroManagedDescriptorSpawn(call)
		if target != coro.UnknownManaged || !managedShape {
			return target, nil
		}
		if managedInterface {
			return coro.UnknownManagedInterfaceDispatch, nil
		}
		return coro.UnknownManagedDispatch, nil
	}
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
	if in.functionBackground != nil || in.foreignNoBlock != nil || in.foreignSync != nil ||
		in.foreignSchedulerWait != nil || in.foreignWorker != nil || in.callableIdentity != nil || in.callableContract != nil ||
		in.noPreempt != nil || in.noUnwind != nil || in.assemblyNoSuspend != nil || config.ClassifyFunction != nil {
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
			frontendManagedBodyless := false
			if in.functionBackground != nil {
				background, classified, err := in.functionBackground(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend ABI for %q: %w", fn.Name(), err)
				}
				frontendC = classified && background == llssa.InC
				frontendManagedBodyless = classified && background == llssa.InGo && len(fn.Blocks) == 0
			}
			noPreemptCertificate := ""
			noPreemptCertified := false
			if in.noPreempt != nil {
				noPreemptCertificate, noPreemptCertified, err = in.noPreempt(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen no-preempt certificate for %q: %w", fn.Name(), err)
				}
				if !noPreemptCertified && noPreemptCertificate != "" {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen frontend returned no-preempt certificate data for %q without certifying it", fn.Name())
				}
			}
			if policy.TrustedNoPreempt && !noPreemptCertified {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot suppress preemption for %q without an exact frozen frontend certificate", fn.Name())
			}
			if noPreemptCertified {
				if noPreemptCertificate == "" {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen no-preempt certificate for %q is empty", fn.Name())
				}
				if frontendC || len(fn.Blocks) == 0 || policy.IgnoreBody ||
					policy.OverrideExternal && policy.External != coro.Defined {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("//llgo:nopreempt on %q does not name one analyzed bodyful Go function", fn.Name())
				}
				policy.TrustedNoPreempt = true
			}
			noUnwindCertificate := ""
			noUnwindCertified := false
			if in.noUnwind != nil {
				noUnwindCertificate, noUnwindCertified, err = in.noUnwind(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen no-unwind certificate for %q: %w", fn.Name(), err)
				}
				if !noUnwindCertified && noUnwindCertificate != "" {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen frontend returned no-unwind certificate data for %q without certifying it", fn.Name())
				}
			}
			if policy.TrustedNoUnwind && !noUnwindCertified {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot suppress local unwind for %q without an exact frozen frontend certificate", fn.Name())
			}
			if noUnwindCertified {
				if noUnwindCertificate == "" {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen no-unwind certificate for %q is empty", fn.Name())
				}
				if frontendC || len(fn.Blocks) == 0 || policy.IgnoreBody ||
					policy.OverrideExternal && policy.External != coro.Defined {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("//llgo:nounwind on %q does not name one analyzed bodyful Go function", fn.Name())
				}
				policy.TrustedNoUnwind = true
			}
			var identityCertificate cl.CoroCallableIdentityCertificate
			identityCertified := false
			if in.callableIdentity != nil {
				identityCertificate, identityCertified, err = in.callableIdentity(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend callable identity for %q: %w", fn.Name(), err)
				}
				if !identityCertified && !identityCertificate.IsZero() {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen frontend returned callable identity data for %q without certifying it", fn.Name())
				}
			}
			if requested := policy.CallableIdentityCertificate; !requested.IsZero() {
				if !identityCertified {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot certify callable identity for %q without an exact frozen frontend identity", fn.Name())
				}
				if requested != identityCertificate {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder callable identity for %q conflicts with the frozen frontend certificate", fn.Name())
				}
			}
			if identityCertified {
				if !frontendC {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen callable identity for %q does not name a frontend C declaration", fn.Name())
				}
				if err := identityCertificate.Validate(); err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen callable identity for %q is invalid: %w", fn.Name(), err)
				}
				policy.CallableIdentityCertificate = identityCertificate
			} else if frontendC && in.callableIdentity != nil {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("frontend C declaration %q has no frozen callable identity", fn.Name())
			}
			var certificate cl.CoroForeignNoBlockCertificate
			certified := false
			if in.foreignNoBlock != nil {
				certificate, certified, err = in.foreignNoBlock(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend foreign noblock certificate for %q: %w", fn.Name(), err)
				}
			}
			var syncCertificate cl.CoroForeignSyncCertificate
			syncCertified := false
			if in.foreignSync != nil {
				syncCertificate, syncCertified, err = in.foreignSync(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend foreign sync certificate for %q: %w", fn.Name(), err)
				}
			}
			var schedulerWaitCertificate cl.CoroForeignSchedulerWaitCertificate
			schedulerWaitCertified := false
			if in.foreignSchedulerWait != nil {
				schedulerWaitCertificate, schedulerWaitCertified, err = in.foreignSchedulerWait(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend foreign schedulerwait certificate for %q: %w", fn.Name(), err)
				}
			}
			var workerCertificate cl.CoroForeignWorkerCertificate
			workerCertified := false
			if in.foreignWorker != nil {
				workerCertificate, workerCertified, err = in.foreignWorker(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend foreign worker certificate for %q: %w", fn.Name(), err)
				}
			}
			var callableCertificate cl.CoroCallableContractCertificate
			callableCertified := false
			if in.callableContract != nil {
				callableCertificate, callableCertified, err = in.callableContract(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend callable contract for %q: %w", fn.Name(), err)
				}
				if !callableCertified && !callableCertificate.IsZero() {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen frontend returned callable contract data for %q without certifying it", fn.Name())
				}
			}
			certificateKinds := 0
			for _, present := range []bool{certified, syncCertified, schedulerWaitCertified, workerCertified} {
				if present {
					certificateKinds++
				}
			}
			if certificateKinds > 1 {
				return coro.SSAFunctionPolicy{}, fmt.Errorf("frontend C declaration %q has mutually exclusive frozen foreign-call certificates", fn.Name())
			}
			if requested := policy.CallableContractCertificate; !requested.IsZero() {
				if !callableCertified {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot certify callable function %q without an exact frozen frontend contract", fn.Name())
				}
				if requested != callableCertificate {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder callable contract for %q conflicts with the frozen frontend certificate", fn.Name())
				}
			}
			if callableCertified {
				if callableCertificate.Scope == coro.CallableContractScopeDeclaration {
					if !identityCertified {
						return coro.SSAFunctionPolicy{}, fmt.Errorf("callable declaration contract for %q has no frozen callable identity", fn.Name())
					}
					if err := coro.ValidateCallableContractIdentity(identityCertificate, callableCertificate); err != nil {
						return coro.SSAFunctionPolicy{}, fmt.Errorf("callable declaration contract for %q: %w", fn.Name(), err)
					}
				}
				if certificateKinds != 0 {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("function %q has mutually exclusive generic callable and legacy foreign-call certificates", fn.Name())
				}
				policy, err = applyFrozenCallableContractPolicy(fn, frontendC, policy, callableCertificate)
				if err != nil {
					return coro.SSAFunctionPolicy{}, err
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
			if requested := policy.ForeignSyncCertificate; requested != "" {
				if !syncCertified {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot certify foreign function %q without exact frozen frontend sync metadata", fn.Name())
				}
				if requested != syncCertificate.ID {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder foreign sync certificate for %q conflicts with the frozen frontend proof", fn.Name())
				}
			}
			if requested := policy.ForeignSchedulerWaitCertificate; requested != "" {
				if !schedulerWaitCertified {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot certify foreign function %q without exact frozen frontend schedulerwait metadata", fn.Name())
				}
				if requested != schedulerWaitCertificate.ID {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder foreign schedulerwait certificate for %q conflicts with the frozen frontend proof", fn.Name())
				}
			}
			if requested := policy.ForeignWorkerCertificate; requested != "" {
				if !workerCertified {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot certify foreign function %q without exact frozen frontend worker metadata", fn.Name())
				}
				if requested != workerCertificate.ID {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder foreign worker certificate for %q conflicts with the frozen frontend proof", fn.Name())
				}
			}
			if certified {
				if !frontendC && !frontendManagedBodyless {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen noblock certificate for %q does not name a frontend C or bodyless managed-Go declaration", fn.Name())
				}
				if policy.Effect != coro.NoSuspend || policy.Exec != 0 || policy.NeedsDispatch ||
					policy.OverrideExternal && policy.External != coro.ExternalKnown {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frontend declaration %q conflicts with its frozen noblock certificate", fn.Name())
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
			if syncCertified {
				if !frontendC {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen foreign sync certificate for %q does not name a frontend C declaration", fn.Name())
				}
				if policy.Effect != coro.NoSuspend || policy.Exec != 0 || policy.NeedsDispatch ||
					policy.OverrideExternal && policy.External != coro.ExternalKnown {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frontend C declaration %q conflicts with its frozen foreign sync certificate", fn.Name())
				}
				policy.IgnoreBody = true
				policy.External = coro.ExternalKnown
				policy.OverrideExternal = true
				// sync is a same-thread/no-retention proof, not a lock-free,
				// bounded-latency, or async-signal-safety proof.
				policy.Exec = coro.IRQUnsafe
				policy.ForeignSyncCertificate = syncCertificate.ID
			}
			if schedulerWaitCertified {
				if !frontendC {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen foreign schedulerwait certificate for %q does not name a frontend C declaration", fn.Name())
				}
				if policy.Effect != coro.NoSuspend || policy.Exec != 0 || policy.NeedsDispatch ||
					policy.OverrideExternal && policy.External != coro.ExternalUnknownForeign {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frontend C declaration %q conflicts with its frozen foreign schedulerwait certificate", fn.Name())
				}
				// Keep the managed boundary fully conservative. The exact certificate
				// is consumed only by the compiler-owned raw host/scheduler-stack
				// closure validator below.
				policy.IgnoreBody = true
				policy.External = coro.ExternalUnknownForeign
				policy.OverrideExternal = true
				policy.Exec = coro.BlockForeign | coro.IRQUnsafe
				policy.ForeignSchedulerWaitCertificate = schedulerWaitCertificate.ID
			}
			if workerCertified {
				if !frontendC {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen foreign worker certificate for %q does not name a frontend C declaration", fn.Name())
				}
				if policy.Effect != coro.NoSuspend || policy.Exec != 0 || policy.NeedsDispatch ||
					policy.OverrideExternal && policy.External != coro.ExternalUnknownForeign {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frontend C declaration %q conflicts with its frozen foreign worker certificate", fn.Name())
				}
				// worker is a thread-independence, callback, by-value, and
				// no-retention contract. It deliberately preserves the managed
				// blocking foreign edge; the exact call-site worker ABI validator
				// decides whether a particular call can use it.
				policy.IgnoreBody = true
				policy.External = coro.ExternalUnknownForeign
				policy.OverrideExternal = true
				policy.Exec = coro.BlockForeign | coro.IRQUnsafe
				policy.ForeignWorkerCertificate = workerCertificate.ID
			}
			assemblyCertificate := ""
			assemblyCertified := false
			if in.assemblyNoSuspend != nil {
				assemblyCertificate, assemblyCertified, err = in.assemblyNoSuspend(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen assembly no-suspend certificate for %q: %w", fn.Name(), err)
				}
			}
			if requested := policy.AssemblyNoSuspendCertificate; requested != "" {
				if !assemblyCertified {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder cannot certify assembly function %q without exact frozen translated-module metadata", fn.Name())
				}
				if requested != assemblyCertificate {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("builder assembly no-suspend certificate for %q conflicts with the frozen proof", fn.Name())
				}
			}
			if assemblyCertified {
				if callableCertified {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("function %q has mutually exclusive callable and assembly certificates", fn.Name())
				}
				if frontendC {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("frozen assembly no-suspend certificate for %q names a frontend C declaration", fn.Name())
				}
				if certificateKinds != 0 || policy.Effect != coro.NoSuspend || policy.Exec != 0 || policy.NeedsDispatch ||
					policy.OverrideExternal && policy.External != coro.ExternalKnown {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("translated assembly declaration %q conflicts with its frozen no-suspend certificate", fn.Name())
				}
				policy.IgnoreBody = true
				policy.External = coro.ExternalKnown
				policy.OverrideExternal = true
				policy.Exec = coro.IRQUnsafe
				policy.AssemblyNoSuspendCertificate = assemblyCertificate
			}
			if policy.IgnoreBody && !frontendC && !assemblyCertified && !(frontendManagedBodyless && certified) {
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
	// A structured frontend operation has no managed callee edge: cl replaces
	// its source call with a suspend in the owner's exact frame. Seed that
	// physical effect from the same frozen call-site plan used to elide the
	// declaration/adapter, so synchronous source callers are transparently
	// coroutine primary bodies and the plan digest records both the owner effect
	// and site.
	if in.callSitePlan != nil {
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
			rawPlainBasePolicyEffects[fn] = policy.Effect
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(ssa.CallInstruction)
					if !ok || call.Common() == nil {
						continue
					}
					callSite, frozen, err := in.callSitePlan(call)
					if err != nil {
						return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen intrinsic effect in %q: %w", fn.Name(), err)
					}
					if frozen && callSite.Elision == cl.CoroCallElidedCgoWorker {
						policy.Effect = policy.Effect.Join(coro.WaitForeign)
					} else if _, ordinary := call.(*ssa.Call); ordinary &&
						frozen && callSite.Intrinsic && callSite.IntrinsicSemantics.SuspendsCurrentFrame() {
						policy.Effect = policy.Effect.Join(callSite.IntrinsicSemantics.CurrentFrameEffect())
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
	// bounded: otherwise a future CPU-heavy/looping version could run forever
	// without a preemption cut. Exact static targets are available before the
	// first analysis. Descriptor targets are discovered from its immutable
	// CallPlans and added in one deterministic second fixed point below.
	var spawnSeeded map[*ssa.Function]struct{}
	{
		spawnSeeded = make(map[*ssa.Function]struct{})
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
					spawnSeeded[fn] = struct{}{}
					// Preserve the direct-static path for top-level functions and
					// exact methods. A captured closure or dynamic function value is
					// intentionally deferred to the descriptor CallPlan fixed point.
					if target, err := in.closedStaticSpawnTarget(fn, spawn); err == nil {
						spawnSeeded[target] = struct{}{}
					} else {
						if !isCoroManagedDescriptorSpawn(spawn) {
							return nil, fmt.Errorf("coroutine spawn in %q: %w", fn.Name(), err)
						}
					}
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
			if _, required := spawnSeeded[fn]; required {
				if policy.IgnoreBody {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("coroutine spawn function %q is not a Go-emitted body", fn.Name())
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
			// implementation. A frozen schedulerwait leaf is the sole blocking
			// exception, and only when this exact member belongs to the
			// compiler-owned raw host/scheduler-stack island.
			const supportedExec = coro.MayUnwind | coro.NeedsCleanupFrame | coro.IRQUnsafe
			allowedExec := coro.ExecFlags(supportedExec)
			callableWaitsForeign := false
			callableNoReturn := false
			if certificate := policy.CallableContractCertificate; !certificate.IsZero() && certificate.Scope == coro.CallableContractScopeDeclaration {
				switch certificate.Contract.Progress {
				case coro.ProgressMayBlock, coro.ProgressUnknown, coro.ProgressAsyncCompletion:
					callableWaitsForeign = true
				case coro.ProgressNoReturn:
					callableWaitsForeign = true
					callableNoReturn = true
				}
			}
			if policy.ForeignSchedulerWaitCertificate != "" || callableWaitsForeign {
				if _, hostStack := in.requiredHostPlain[fn]; !hostStack {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("blocking C declaration %q is outside the compiler-owned raw host/scheduler-stack island", fn.Name())
				}
				allowedExec |= coro.BlockForeign
			}
			if callableNoReturn {
				allowedExec |= coro.NoReturn
			}
			if unsupported := policy.Exec &^ allowedExec; unsupported != 0 {
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
			// Suppress a local loop/budget preemption seed only for the exact
			// compiler-owned runtime island. requiredPlain also contains ordinary
			// source callbacks published to C; those must retain NeedsPreempt and
			// fail the synchronous callback contract when they can run without a
			// bound. requiredHostPlain is frozen before those callbacks are merged.
			_, compilerRuntimeIsland := in.requiredHostPlain[fn]
			policy.TrustedNoPreempt = compilerRuntimeIsland
			if classified && background == llssa.InC {
				if !policy.IgnoreBody || !policy.OverrideExternal || (policy.External != coro.ExternalUnknownForeign && policy.External != coro.ExternalKnown) {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("compiler runtime ABI C declaration %q conflicts with frozen foreign classification: %s", fn.Name(), policy.External)
				}
				if policy.ForeignSchedulerWaitCertificate == "" && !callableWaitsForeign {
					policy.External = coro.ExternalKnown
					policy.OverrideExternal = true
				} else if policy.External != coro.ExternalUnknownForeign || policy.Exec != coro.BlockForeign|coro.IRQUnsafe {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("schedulerwait C declaration %q lost its managed unknown-foreign/blocking boundary", fn.Name())
				}
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
		requested := false
		if classifyElided != nil {
			var err error
			requested, err = classifyElided(caller, call)
			if err != nil {
				return false, err
			}
		}
		if in.callSitePlan == nil {
			return requested, nil
		}
		frontend, frozen, err := in.callSitePlan(call)
		if err != nil {
			return false, fmt.Errorf("read frozen call SitePlan in %q: %w", caller.Name(), err)
		}
		if !frozen {
			return false, fmt.Errorf("call %q in %q is absent from the frozen ProgramIR", call.String(), caller.Name())
		}
		if requested && !frontend.ElidesCall() {
			return false, fmt.Errorf("builder cannot elide ordinary call in %q; only calls omitted by the build frontend may be elided", caller.Name())
		}
		return frontend.ElidesCall(), nil
	}
	classifyElidedCertificate := config.ClassifyElidedCallCertificate
	config.ClassifyElidedCallCertificate = func(caller *ssa.Function, call ssa.CallInstruction) (string, error) {
		requested := ""
		if classifyElidedCertificate != nil {
			var err error
			requested, err = classifyElidedCertificate(caller, call)
			if err != nil {
				return "", err
			}
		}
		if in.callSitePlan == nil {
			return requested, nil
		}
		frontend, frozen, err := in.callSitePlan(call)
		if err != nil {
			return "", fmt.Errorf("read frozen elided-call SitePlan in %q: %w", caller.Name(), err)
		}
		if !frozen {
			return "", fmt.Errorf("elided call %q in %q is absent from the frozen ProgramIR", call.String(), caller.Name())
		}
		if requested != "" && requested != frontend.ElisionCertificate {
			return "", fmt.Errorf("builder cannot forge an elided-call capability in %q", caller.Name())
		}
		return frontend.ElisionCertificate, nil
	}
	classifyStaticSpawnTarget := config.ClassifyStaticSpawnTarget
	config.ClassifyStaticSpawnTarget = func(caller *ssa.Function, spawn *ssa.Go) (*ssa.Function, bool, error) {
		var requested *ssa.Function
		requestedRedirect := false
		if classifyStaticSpawnTarget != nil {
			var err error
			requested, requestedRedirect, err = classifyStaticSpawnTarget(caller, spawn)
			if err != nil {
				return nil, false, err
			}
		}
		if in.callSitePlan == nil {
			return requested, requestedRedirect, nil
		}
		frontend, frozen, err := in.callSitePlan(spawn)
		if err != nil {
			return nil, false, fmt.Errorf("read frozen static-spawn SitePlan in %q: %w", caller.Name(), err)
		}
		if !frozen {
			return nil, false, fmt.Errorf("spawn %q in %q is absent from the frozen ProgramIR", spawn.String(), caller.Name())
		}
		compilerTarget := frontend.StaticSpawnTarget
		if requestedRedirect && (compilerTarget == nil || requested != compilerTarget) {
			return nil, false, fmt.Errorf("builder cannot forge a static spawn target in %q", caller.Name())
		}
		return compilerTarget, compilerTarget != nil, nil
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
	if in.staticCodeAddressCallArgument != nil || config.ClassifyStaticCodeAddressCallArgument != nil {
		classifyCodeAddress := config.ClassifyStaticCodeAddressCallArgument
		config.ClassifyStaticCodeAddressCallArgument = func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			compilerRequired := false
			var err error
			if in.staticCodeAddressCallArgument != nil {
				compilerRequired, err = in.staticCodeAddressCallArgument(call, argument)
				if err != nil {
					return false, fmt.Errorf("classify frozen static code-address argument %d in %q: %w", argument, caller.Name(), err)
				}
			}
			if classifyCodeAddress != nil {
				requested, err := classifyCodeAddress(caller, call, argument)
				if err != nil {
					return false, err
				}
				if requested && !compilerRequired {
					return false, fmt.Errorf("builder cannot authorize static code-address lowering for non-compiler call argument %d in %q", argument, caller.Name())
				}
			}
			return compilerRequired, nil
		}
	}
	// The build-owned C callback proof is raw provenance. Keep the managed
	// classifier independent: a builder cannot turn the same physical C
	// publication into managed SyncDemand merely by requesting the legacy
	// classifier name.
	if config.ClassifyDirectPlainCallArgument != nil {
		classifyDirectPlain := config.ClassifyDirectPlainCallArgument
		config.ClassifyDirectPlainCallArgument = func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			requested, err := classifyDirectPlain(caller, call, argument)
			if err != nil {
				return false, err
			}
			if requested {
				return false, fmt.Errorf("builder cannot authorize direct-plain ABI for call argument %d in %q; the frozen C callback proof has raw provenance", argument, caller.Name())
			}
			return false, nil
		}
	}
	if len(in.requiredDirectPlain) != 0 || config.ClassifyRawDirectPlainCallArgument != nil {
		required := make(map[coroCallArgumentKey]struct{}, len(in.requiredDirectPlain))
		for _, use := range in.requiredDirectPlain {
			required[coroCallArgumentKey{call: use.call, argument: use.argument}] = struct{}{}
		}
		classifyRawDirectPlain := config.ClassifyRawDirectPlainCallArgument
		config.ClassifyRawDirectPlainCallArgument = func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			key := coroCallArgumentKey{call: call, argument: argument}
			_, compilerRequired := required[key]
			if classifyRawDirectPlain != nil {
				requested, err := classifyRawDirectPlain(caller, call, argument)
				if err != nil {
					return false, err
				}
				if requested && !compilerRequired {
					return false, fmt.Errorf("builder cannot authorize raw direct-plain ABI for non-compiler call argument %d in %q", argument, caller.Name())
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
				if !classified && (requested.MayBeNil || requested.SyncDispatch || len(requested.Targets) != 0 || len(requested.SyncOnlyCallArguments) != 0) {
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
	if in.demandReferences != nil || in.syncDemandReferences != nil ||
		config.ClassifyDemandReferences != nil || config.ClassifySyncDemandReferences != nil {
		classifyDemandReferences := config.ClassifyDemandReferences
		classifySyncDemandReferences := config.ClassifySyncDemandReferences
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
		config.ClassifySyncDemandReferences = func(owner *ssa.Function) ([]*ssa.Function, error) {
			var compilerTargets []*ssa.Function
			var err error
			if in.syncDemandReferences != nil {
				compilerTargets, err = in.syncDemandReferences(owner)
				if err != nil {
					return nil, fmt.Errorf("classify frozen frontend synchronous demand references for %q: %w", owner.Name(), err)
				}
			}
			compilerTargets = append([]*ssa.Function(nil), compilerTargets...)
			if classifySyncDemandReferences != nil {
				requested, err := classifySyncDemandReferences(owner)
				if err != nil {
					return nil, err
				}
				if !sameExactCoroFunctionReferences(requested, compilerTargets) {
					return nil, fmt.Errorf("builder synchronous demand references in %q conflict with the frozen frontend raw-ABI references", owner.Name())
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
	if config.OutcomeMode != coro.OutcomeLegacy && config.OutcomeMode != coro.OutcomeExplicitStatus {
		return nil, fmt.Errorf(
			"builder outcome mode %d is unsupported by the stackless architecture",
			config.OutcomeMode,
		)
	}
	config.OutcomeMode = coro.OutcomeExplicitStatus
	config.ResolveFunction = func(fn *ssa.Function) (*ssa.Function, bool, error) {
		canonical, ok := in.ResolveFunction(fn)
		return canonical, ok, nil
	}
	config.EmissionUniverse = in.EmissionUniverse
	plan, err := coro.AnalyzeSSA(in.Program, allRoots, config)
	if err == nil {
		var dynamicTargets map[*ssa.Function]struct{}
		dynamicTargets, err = requiredCoroManagedSpawnTargets(plan)
		for target := range dynamicTargets {
			if _, already := spawnSeeded[target]; already {
				delete(dynamicTargets, target)
			}
		}
		if err == nil && len(dynamicTargets) != 0 {
			classify := config.ClassifyFunction
			config.ClassifyFunction = func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				var policy coro.SSAFunctionPolicy
				if classify != nil {
					var classifyErr error
					policy, classifyErr = classify(fn)
					if classifyErr != nil {
						return coro.SSAFunctionPolicy{}, classifyErr
					}
				}
				if _, required := dynamicTargets[fn]; !required {
					return policy, nil
				}
				if policy.IgnoreBody {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("managed descriptor spawn target %q is not a Go-emitted body", fn.Name())
				}
				policy.Effect = policy.Effect.Join(coro.YieldOnly)
				return policy, nil
			}
			for target := range dynamicTargets {
				spawnSeeded[target] = struct{}{}
			}
			plan, err = coro.AnalyzeSSA(in.Program, allRoots, config)
		}
	}
	if err == nil {
		var rawPlain *coroRawABIPlainClosure
		rawPlain, err = in.liveCoroRawABIPlainClosure(plan, rawPlainBasePolicyEffects)
		if err == nil && rawPlain != nil && len(rawPlain.functions) != 0 {
			// The preliminary fixed point keeps legacy synchronous method-table
			// and compiler-runtime crossings in managed demand until liveness is
			// closed. Exact C callback arguments already carry raw provenance.
			// Once the complete live closure is known, move the remaining legacy
			// crossings into that same independent raw domain. Do not leave the
			// old managed roots/references in place: doing so would manufacture a
			// managed entry for a raw-only helper and defeat EmitRawPlain.
			rawRoots := migrateCoroRawPlainRoots(
				roots, in.requiredRoots, in.requiredPlain, rawPlain.entries,
			)
			rawConfig := migrateCoroRawPlainReferenceClassifiers(config, rawPlain.rawReferences)
			classify := config.ClassifyFunction
			rawConfig.ClassifyFunction = func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				var policy coro.SSAFunctionPolicy
				if classify != nil {
					var classifyErr error
					policy, classifyErr = classify(fn)
					if classifyErr != nil {
						return coro.SSAFunctionPolicy{}, classifyErr
					}
				}
				if _, required := rawPlain.functions[fn]; !required {
					return policy, nil
				}
				if policy.IgnoreBody || policy.OverrideExternal && policy.External != coro.Defined {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("live raw ABI plain function %q is not one owned Go body", fn.Name())
				}
				// Preserve every Effect/Exec fact. Raw provenance chooses whether this
				// exact body is raw-only, shares one no-suspend plain body, or needs a
				// managed coroutine plus a raw variant; policy never recolors it.
				// Only physical roots/address publications are RawPlainEntry values.
				policy.RawPlainVariant = true
				if _, entry := rawPlain.entries[fn]; entry {
					policy.RawPlainEntry = true
				}
				return policy, nil
			}
			plan, err = coro.AnalyzeSSA(in.Program, rawRoots, rawConfig)
			if err == nil {
				err = validateLiveCoroRawABIPlainClosure(plan, rawPlain)
			}
		}
	}
	if err == nil {
		err = validateRequiredCoroDirectPlainCallArguments(plan, in.requiredDirectPlain)
	}
	if err == nil {
		err = validateRequiredCoroClosedDynamicCalls(plan, in.requiredClosedDynamic, in.requiredGlobalFunctionSlots)
	}
	if err == nil {
		err = validateCoroSpawnPlan(plan)
	}
	if err == nil && in.recordAnalysis != nil {
		in.recordAnalysis(plan)
	}
	return plan, err
}

// migrateCoroRawPlainRoots constructs the second fixed-point root set. User
// roots remain ordinary managed roots. A frozen build-owned synchronous root
// that is both required-plain and an actual live raw entry has its sync bit
// moved to RawPlainDemand. Every exact owned body in that entry's closed raw
// call/closure island receives raw provenance through ordinary call edges and
// the exact rawReferences frozen below for non-call edges such as MakeClosure.
// Closure members must not become Roots: a raw root is an address-publication
// capability and therefore requires RawPlainEntry, while an internal member
// needs only RawPlainVariant. An async bit on the same entry, or a separate user
// root for it, remains managed and therefore correctly produces a mixed
// managed+raw plan.
func migrateCoroRawPlainRoots(
	user, required coro.Roots,
	requiredPlain, entries map[*ssa.Function]struct{},
) coro.Roots {
	result := make(coro.Roots, 0, len(user)+len(required))
	result = append(result, user...)
	for _, root := range required {
		managed := root.Demand.Join(root.ManagedDemand)
		raw := root.RawPlainDemand
		_, plainCrossing := requiredPlain[root.Function]
		_, liveEntry := entries[root.Function]
		if plainCrossing && liveEntry && managed.Contains(coro.SyncDemand) {
			managed &^= coro.SyncDemand
			raw = true
			result = append(result, coro.Root{
				Function: root.Function, ManagedDemand: managed, RawPlainDemand: raw,
			})
			continue
		}
		result = append(result, root)
	}
	return result
}

// migrateCoroRawPlainReferenceClassifiers moves the frozen synchronous subset
// out of the managed demand-reference classifier and into the independent raw
// classifier for the second fixed point. The same physical reference must not
// occur in both domains merely because the preliminary analysis represented it
// as SyncDemand. Independently supplied raw and managed references are retained.
func migrateCoroRawPlainReferenceClassifiers(
	config coro.SSAConfig,
	exactRawReferences map[*ssa.Function][]*ssa.Function,
) coro.SSAConfig {
	managedClassifier := config.ClassifyDemandReferences
	syncClassifier := config.ClassifySyncDemandReferences
	if syncClassifier == nil {
		return config
	}
	rawClassifier := config.ClassifyRawPlainDemandReferences
	type splitReferences struct {
		managed []*ssa.Function
		raw     []*ssa.Function
		err     error
	}
	cache := make(map[*ssa.Function]splitReferences)
	classify := func(owner *ssa.Function) splitReferences {
		if result, ok := cache[owner]; ok {
			return result
		}
		var result splitReferences
		var managed, synchronous, raw []*ssa.Function
		if managedClassifier != nil {
			managed, result.err = managedClassifier(owner)
			if result.err != nil {
				cache[owner] = result
				return result
			}
		}
		synchronous, result.err = syncClassifier(owner)
		if result.err != nil {
			cache[owner] = result
			return result
		}
		if rawClassifier != nil {
			raw, result.err = rawClassifier(owner)
			if result.err != nil {
				cache[owner] = result
				return result
			}
		}
		raw = append(raw, exactRawReferences[owner]...)

		syncSet := make(map[*ssa.Function]struct{}, len(synchronous))
		for _, target := range synchronous {
			syncSet[target] = struct{}{}
		}
		result.managed = make([]*ssa.Function, 0, len(managed))
		for _, target := range managed {
			if _, migrated := syncSet[target]; migrated {
				continue
			}
			result.managed = append(result.managed, target)
		}
		seenRaw := make(map[*ssa.Function]struct{}, len(raw)+len(synchronous))
		result.raw = make([]*ssa.Function, 0, len(raw)+len(synchronous))
		for _, targets := range [][]*ssa.Function{raw, synchronous} {
			for _, target := range targets {
				if _, duplicate := seenRaw[target]; duplicate {
					continue
				}
				seenRaw[target] = struct{}{}
				result.raw = append(result.raw, target)
			}
		}
		cache[owner] = result
		return result
	}
	config.ClassifyDemandReferences = func(owner *ssa.Function) ([]*ssa.Function, error) {
		result := classify(owner)
		return append([]*ssa.Function(nil), result.managed...), result.err
	}
	config.ClassifySyncDemandReferences = nil
	config.ClassifyRawPlainDemandReferences = func(owner *ssa.Function) ([]*ssa.Function, error) {
		result := classify(owner)
		return append([]*ssa.Function(nil), result.raw...), result.err
	}
	return config
}

// coroRawABIPlainClosure is the exact owned Go-body closure that needs a
// second, legacy-stack entry for one frozen raw-address publication.
//
// normal records members reachable from the published callback's ordinary
// entry. A member absent from normal is reachable only after crossing an exact
// UnwindOnly lowered edge. That distinction is physical: such a terminal-only
// path cannot return to the scheduler, so it may synchronously finish an
// already-committed fatal panic through a legacy foreign leaf. It is not a
// general no-block certificate for that foreign function.
type coroRawABIPlainClosure struct {
	functions          map[*ssa.Function]struct{}
	entries            map[*ssa.Function]struct{}
	normal             map[*ssa.Function]struct{}
	hostStack          map[*ssa.Function]struct{}
	externalPlain      map[*ssa.Function]struct{}
	closedDynamic      map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate
	basePolicyEffects  map[*ssa.Function]coro.Effect
	rawSyncIntrinsics  map[ssa.CallInstruction]string
	nonRawLocalEffects map[*ssa.Function]coro.Effect
	normalReturnBlocks map[*ssa.Function]map[*ssa.BasicBlock]struct{}
	rawReferences      map[*ssa.Function][]*ssa.Function
	rawReferenceSeen   map[*ssa.Function]map[*ssa.Function]struct{}
}

func (closure *coroRawABIPlainClosure) recordRawReference(owner, target *ssa.Function) {
	if closure == nil || owner == nil || target == nil {
		return
	}
	seen := closure.rawReferenceSeen[owner]
	if seen == nil {
		seen = make(map[*ssa.Function]struct{})
		closure.rawReferenceSeen[owner] = seen
	}
	if _, duplicate := seen[target]; duplicate {
		return
	}
	seen[target] = struct{}{}
	closure.rawReferences[owner] = append(closure.rawReferences[owner], target)
}

func (closure *coroRawABIPlainClosure) terminalOnly(fn *ssa.Function) bool {
	if closure == nil {
		return false
	}
	_, included := closure.functions[fn]
	_, normal := closure.normal[fn]
	return included && !normal
}

// instructionUnwindOnly applies the same name-independent CFG proof used by
// the frontend for compiler-lowered helpers to an explicit SSA call site. A
// static call in a block that cannot reach a normal Return belongs to the
// terminal episode even when its owner also has ordinary return paths.
func (closure *coroRawABIPlainClosure) instructionUnwindOnly(owner *ssa.Function, instruction ssa.Instruction) bool {
	if closure == nil || owner == nil || instruction == nil || instruction.Parent() != owner || instruction.Block() == nil {
		return false
	}
	if closure.normalReturnBlocks == nil {
		closure.normalReturnBlocks = make(map[*ssa.Function]map[*ssa.BasicBlock]struct{})
	}
	reachable, ok := closure.normalReturnBlocks[owner]
	if !ok {
		reachable = make(map[*ssa.BasicBlock]struct{})
		queue := make([]*ssa.BasicBlock, 0, len(owner.Blocks))
		for _, block := range owner.Blocks {
			for _, blockInstruction := range block.Instrs {
				if _, normalReturn := blockInstruction.(*ssa.Return); !normalReturn {
					continue
				}
				reachable[block] = struct{}{}
				queue = append(queue, block)
				break
			}
		}
		for head := 0; head < len(queue); head++ {
			for _, predecessor := range queue[head].Preds {
				if _, seen := reachable[predecessor]; seen {
					continue
				}
				reachable[predecessor] = struct{}{}
				queue = append(queue, predecessor)
			}
		}
		closure.normalReturnBlocks[owner] = reachable
	}
	_, reachesNormalReturn := reachable[instruction.Block()]
	return !reachesNormalReturn
}

// liveCoroRawABIPlainClosure derives the bounded plain island required by an
// exact synchronous legacy-ABI crossing. The preliminary fixed point is the
// authority for liveness: a type-data owner that is not demanded emits no raw
// callback address. Every live crossing is a seed, including one whose
// preliminary body is plain; the second fixed point must record raw provenance
// instead of silently retaining a managed SyncDemand root.
//
// A raw method-table target is intentionally not a seed. In particular, a live
// receiver method may contain an unbounded loop or a real suspend and must keep
// its ordinary coroutine plan until the method-table ABI has a physical dynamic
// dispatch adapter. The seed shapes accepted here are receiver-less,
// non-capturing functions selected by either the frontend's exact raw
// synchronous-use classifier or the compiler's exact required-plain runtime
// root set. Its exact synchronous static/lowered Go-call closure must share that
// ABI. Foreign leaves are never added: their independently frozen
// no-block/unknown policy continues to flow through the second analysis.
func (in CoroPlanInput) liveCoroRawABIPlainClosure(
	preliminary *coro.SSAPlan,
	basePolicyEffects map[*ssa.Function]coro.Effect,
) (*coroRawABIPlainClosure, error) {
	if preliminary == nil {
		return nil, nil
	}
	closure := &coroRawABIPlainClosure{
		functions:          make(map[*ssa.Function]struct{}),
		entries:            make(map[*ssa.Function]struct{}),
		normal:             make(map[*ssa.Function]struct{}),
		hostStack:          make(map[*ssa.Function]struct{}),
		externalPlain:      make(map[*ssa.Function]struct{}),
		closedDynamic:      in.requiredClosedDynamic,
		basePolicyEffects:  basePolicyEffects,
		rawSyncIntrinsics:  make(map[ssa.CallInstruction]string),
		nonRawLocalEffects: make(map[*ssa.Function]coro.Effect),
		normalReturnBlocks: make(map[*ssa.Function]map[*ssa.BasicBlock]struct{}),
		rawReferences:      make(map[*ssa.Function][]*ssa.Function),
		rawReferenceSeen:   make(map[*ssa.Function]map[*ssa.Function]struct{}),
	}
	// reachable records the strongest context already propagated through an
	// owned body: false means terminal-only, true means ordinarily reachable.
	// A terminal member discovered first is reprocessed if a later ordinary
	// path reaches it, ensuring that ordinary reachability always wins.
	reachable := make(map[*ssa.Function]bool)
	hostReachable := make(map[*ssa.Function]bool)
	queue := make([]*ssa.Function, 0)

	enqueueGoBody := func(fn *ssa.Function, normal, hostStack bool) error {
		if fn == nil {
			return fmt.Errorf("live raw ABI plain closure contains a nil function")
		}
		functionPlan, planned := preliminary.FunctionPlan(fn)
		if !planned {
			return fmt.Errorf("live raw ABI plain closure target %q is absent from the preliminary plan", fn.Name())
		}
		if functionPlan.External != coro.Defined || preliminary.IgnoresBody(fn) || len(fn.Blocks) == 0 {
			if _, required := in.requiredPlain[fn]; required {
				closure.externalPlain[fn] = struct{}{}
			}
			return nil
		}
		if in.functionBackground != nil {
			background, classified, err := in.functionBackground(fn)
			if err != nil {
				return fmt.Errorf("classify live raw ABI plain function %q: %w", fn.Name(), err)
			}
			if !classified || background != llssa.InGo {
				return nil
			}
		}
		previousNormal, seen := reachable[fn]
		previousHost := hostReachable[fn]
		if seen && (previousNormal || !normal) && (previousHost || !hostStack) {
			return nil
		}
		reachable[fn] = previousNormal || normal
		hostReachable[fn] = previousHost || hostStack
		closure.functions[fn] = struct{}{}
		if normal {
			closure.normal[fn] = struct{}{}
		}
		if hostStack {
			closure.hostStack[fn] = struct{}{}
		}
		queue = append(queue, fn)
		return nil
	}

	// A managed generated-cgo call publishes the exact synchronous adapter
	// entry to a bounded worker thunk. The source call itself is deliberately
	// elided from the ordinary managed call graph, so carry its ProgramIR-owned
	// physical target into the independent raw domain explicitly. This is the
	// same provenance shape as a raw function-address publication, but remains
	// owned by the exact live call site rather than a package/name heuristic.
	if in.callSitePlan != nil {
		for _, owner := range preliminary.Functions() {
			if owner.Function == nil || owner.Plan.Demand == coro.NoDemand {
				continue
			}
			for _, block := range owner.Function.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(ssa.CallInstruction)
					if !ok || call.Common() == nil {
						continue
					}
					site, frozen, err := in.callSitePlan(call)
					if err != nil {
						return nil, fmt.Errorf("classify live cgo worker crossing in %q: %w", owner.Function.Name(), err)
					}
					if !frozen || site.Elision != cl.CoroCallElidedCgoWorker {
						continue
					}
					target := site.CgoWorkerTarget
					if target == nil || site.ElisionCertificate == "" {
						return nil, fmt.Errorf("live cgo worker crossing in %q has no exact target and certificate", owner.Function.Name())
					}
					canonical, exact := in.ResolveFunction(target)
					if !exact || canonical == nil || canonical != target {
						return nil, fmt.Errorf("live cgo worker crossing in %q targets non-canonical function %q", owner.Function.Name(), target.Name())
					}
					if target.Signature == nil || target.Signature.Recv() != nil ||
						target.Parent() != nil || len(target.FreeVars) != 0 {
						return nil, fmt.Errorf("live cgo worker target %q has no exact receiver-less, non-capturing top-level shape", target.Name())
					}
					if err := enqueueGoBody(target, true, false); err != nil {
						return nil, err
					}
					closure.entries[target] = struct{}{}
					closure.recordRawReference(owner.Function, target)
				}
			}
		}
	}

	// A required-plain compiler/runtime ABI root is a physical synchronous
	// crossing just like a published raw callback address. New C callback roots
	// already carry RawPlainDemand; older compiler-runtime crossings enter the
	// preliminary pass as managed SyncDemand and are migrated after closure.
	// Seed either exact provenance here. The second analysis decides whether the
	// body is raw-only or also has an independent managed consumer.
	for index, root := range in.requiredRoots {
		managed := root.Demand.Join(root.ManagedDemand)
		if root.Function == nil || !root.RawPlainDemand && !managed.Contains(coro.SyncDemand) {
			continue
		}
		if _, required := in.requiredPlain[root.Function]; !required {
			continue
		}
		canonical, exact := in.ResolveFunction(root.Function)
		if !exact || canonical == nil {
			return nil, fmt.Errorf("compiler runtime ABI synchronous root %d targets %q outside the frozen emission universe", index, root.Function.Name())
		}
		if canonical != root.Function {
			return nil, fmt.Errorf("compiler runtime ABI synchronous root %d targets non-canonical function %q", index, root.Function.Name())
		}
		functionPlan, planned := preliminary.FunctionPlan(root.Function)
		if !planned || functionPlan.Demand == coro.NoDemand {
			return nil, fmt.Errorf("compiler runtime ABI synchronous root %d function %q was not demanded by the preliminary plan", index, root.Function.Name())
		}
		if root.Function.Signature == nil || root.Function.Signature.Recv() != nil || len(root.Function.FreeVars) != 0 {
			return nil, fmt.Errorf("compiler runtime ABI synchronous root %d function %q has no exact receiver-less, non-capturing function shape", index, root.Function.Name())
		}
		_, hostStack := in.requiredHostPlain[root.Function]
		if err := enqueueGoBody(root.Function, true, hostStack); err != nil {
			return nil, err
		}
		closure.entries[root.Function] = struct{}{}
	}

	// llgo.funcAddr is itself a physical synchronous ABI publication. The SSA
	// plan freezes each exact sole-consumer MakeInterface occurrence and marks
	// its non-capturing target RawPlainEntry; seed that target here as well so
	// the complete legacy-stack call closure receives the same no-suspend and
	// foreign-leaf validation as compiler/runtime callback roots.
	for _, owner := range preliminary.Functions() {
		if owner.Function == nil || owner.Plan.Demand == coro.NoDemand {
			continue
		}
		for _, block := range owner.Function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				for argument, value := range call.Common().Args {
					if !preliminary.RawFunctionAddressArgument(call, argument) {
						continue
					}
					boxed, ok := value.(*ssa.MakeInterface)
					if !ok {
						return nil, fmt.Errorf("live raw ABI funcAddr publication in %q lost its MakeInterface shape", owner.Function.Name())
					}
					target, ok := boxed.X.(*ssa.Function)
					if !ok || target.Signature == nil || target.Signature.Recv() != nil || len(target.FreeVars) != 0 {
						return nil, fmt.Errorf("live raw ABI funcAddr publication in %q has no exact receiver-less, non-capturing target", owner.Function.Name())
					}
					canonical, exact := in.ResolveFunction(target)
					if !exact || canonical == nil || canonical != target {
						return nil, fmt.Errorf("live raw ABI funcAddr publication in %q targets non-canonical function %q", owner.Function.Name(), target.Name())
					}
					targetPlan, planned := preliminary.FunctionPlan(target)
					if !planned || targetPlan.Demand == coro.NoDemand || !targetPlan.RawPlainDemand || !targetPlan.RawPlainEntry {
						return nil, fmt.Errorf("live raw ABI funcAddr target %q has no demanded raw-entry plan", target.Name())
					}
					if err := enqueueGoBody(target, true, false); err != nil {
						return nil, err
					}
					closure.entries[target] = struct{}{}
				}
			}
		}
	}

	if in.syncDemandReferences != nil {
		for _, owner := range preliminary.Functions() {
			if owner.Function == nil || owner.Plan.Demand == coro.NoDemand {
				continue
			}
			references, err := in.syncDemandReferences(owner.Function)
			if err != nil {
				return nil, fmt.Errorf("classify live raw ABI references for %q: %w", owner.Function.Name(), err)
			}
			for index, target := range append([]*ssa.Function(nil), references...) {
				// The frontend classifier is the frozen physical use-site proof. Do
				// not infer this contract again from a package path or function name.
				if target == nil || target.Signature == nil || target.Signature.Recv() != nil || len(target.FreeVars) != 0 {
					return nil, fmt.Errorf("live raw ABI reference %d in %q has no exact receiver-less, non-capturing function shape", index, owner.Function.Name())
				}
				canonical, exact := in.ResolveFunction(target)
				if !exact || canonical == nil {
					return nil, fmt.Errorf("live raw ABI reference %d in %q targets %q outside the frozen emission universe", index, owner.Function.Name(), target.Name())
				}
				if canonical != target {
					return nil, fmt.Errorf("live raw ABI reference %d in %q targets non-canonical function %q", index, owner.Function.Name(), target.Name())
				}
				targetPlan, planned := preliminary.FunctionPlan(target)
				if !planned || targetPlan.Demand == coro.NoDemand {
					return nil, fmt.Errorf("live raw ABI reference %d in %q was not demanded by the preliminary plan", index, owner.Function.Name())
				}
				_, hostStack := in.requiredHostPlain[target]
				if err := enqueueGoBody(target, true, hostStack); err != nil {
					return nil, err
				}
				closure.entries[target] = struct{}{}
			}
		}
	}

	for head := 0; head < len(queue); head++ {
		fn := queue[head]
		_, normal := closure.normal[fn]
		_, hostStack := closure.hostStack[fn]
		for _, lowered := range preliminary.LoweredCalls(fn) {
			if lowered.Target == nil {
				return nil, fmt.Errorf("live raw ABI function %q has a nil lowered helper for %q", fn.Name(), lowered.LogicalName)
			}
			// UnwindOnly is the frozen frontend CFG proof that every physical
			// use is past a no-normal-return boundary. All other exact calls
			// inherit the current reachability context.
			if err := enqueueGoBody(lowered.Target, normal && !lowered.UnwindOnly, hostStack); err != nil {
				return nil, err
			}
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if direct, ok := instruction.(*ssa.Call); ok && in.callSitePlan != nil {
					callSite, frozen, err := in.callSitePlan(direct)
					if err != nil {
						return nil, fmt.Errorf("classify raw-plain intrinsic in %q: %w", fn.Name(), err)
					}
					if !frozen {
						return nil, fmt.Errorf("raw-plain call %q in %q is absent from the frozen ProgramIR", direct.String(), fn.Name())
					}
					if callSite.Intrinsic && callSite.IntrinsicSemantics.SuspendsCurrentFrame() {
						if callSite.IntrinsicSemantics == cl.CoroIntrinsicCallInlineSuspend && callSite.ElisionCertificate != "" {
							// The only frontend certificate currently exposed here is
							// the exact worker-syscall call-site certificate. Its managed
							// body parks, while rawPlainBody deliberately selects the
							// ordinary synchronous intrinsic lowering.
							closure.rawSyncIntrinsics[direct] = callSite.ElisionCertificate
						} else {
							closure.nonRawLocalEffects[fn] = closure.nonRawLocalEffects[fn].Join(callSite.IntrinsicSemantics.CurrentFrameEffect())
						}
					}
				}
				if makeClosure, ok := instruction.(*ssa.MakeClosure); ok {
					target, exact := makeClosure.Fn.(*ssa.Function)
					if !exact || target == nil || len(makeClosure.Bindings) != len(target.FreeVars) {
						return nil, fmt.Errorf("live raw ABI function %q has a non-exact closure construction %q", fn.Name(), makeClosure.String())
					}
					canonical, resolved := in.ResolveFunction(target)
					if !resolved || canonical == nil || canonical != target {
						return nil, fmt.Errorf("live raw ABI function %q closure target %q is not one exact canonical function", fn.Name(), target.Name())
					}
					targetNormal := normal && !closure.instructionUnwindOnly(fn, instruction)
					if err := enqueueGoBody(target, targetNormal, hostStack); err != nil {
						return nil, err
					}
					if _, included := closure.functions[target]; included {
						// MakeClosure is a code-address publication inside this
						// exact raw body. Generic call propagation cannot see it,
						// so freeze one raw-only reference edge without promoting
						// the captured target to a public RawPlainEntry.
						closure.recordRawReference(fn, target)
					}
				}
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil || call.Common().StaticCallee() == nil {
					continue
				}
				callPlan, planned := preliminary.CallPlan(call)
				if !planned {
					// Frontend-elided intrinsics have no physical direct edge;
					// their replacement helpers, if any, are covered above.
					continue
				}
				if callPlan.Kind != coro.CallDirect && callPlan.Kind != coro.CallDefer {
					// A spawned target has its own managed entry. Foreign calls
					// retain their independent policy and never receive Go trust.
					continue
				}
				if callPlan.Open || len(callPlan.Targets) != 1 {
					// No certificate is inferred through a dynamic/open edge. Its
					// conservative effect remains in the second fixed point.
					continue
				}
				target, found := preliminary.Function(callPlan.Targets[0])
				if !found || target == nil {
					return nil, fmt.Errorf("live raw ABI static call in %q has an unresolved preliminary target", fn.Name())
				}
				targetNormal := normal && !closure.instructionUnwindOnly(fn, instruction)
				if err := enqueueGoBody(target, targetNormal, hostStack); err != nil {
					return nil, err
				}
			}
		}
	}
	return closure, nil
}

// validateLiveCoroRawABIPlainClosure proves that the exact closure selected
// above has a real legacy-stack recipe. Managed-only Yield/Await/Outcome bits
// are implemented differently by the raw variant and are therefore allowed;
// a real event wait, park, open call, spawn, or uncertified foreign boundary is
// not. This validator never clears or rewrites the managed plan.
func coroRawPlainSSALocalEffect(fn *ssa.Function) coro.Effect {
	if fn == nil {
		return coro.NoSuspend
	}
	effect := coro.NoSuspend
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			switch instruction := instruction.(type) {
			case *ssa.Send:
				effect = effect.Join(coro.MayPark)
			case *ssa.UnOp:
				if instruction.Op == token.ARROW {
					effect = effect.Join(coro.MayPark)
				}
			case *ssa.Select:
				if instruction.Blocking {
					effect = effect.Join(coro.MayPark)
				}
			}
		}
	}
	return effect
}

func validateLiveCoroRawABIPlainClosure(plan *coro.SSAPlan, raw *coroRawABIPlainClosure) error {
	if plan == nil {
		return fmt.Errorf("live raw ABI plain closure validation requires a coroutine plan")
	}
	if raw == nil {
		return fmt.Errorf("live raw ABI plain closure validation requires an exact closure")
	}
	const managedOnlyEffects = coro.YieldOnly | coro.AwaitStructured | coro.OutcomeStructured
	const legacyExec = coro.IRQUnsafe | coro.NeedsPreempt | coro.MayUnwind | coro.NeedsCleanupFrame | coro.NoReturn | coro.PanicOnly

	validateTarget := func(owner, target *ssa.Function, terminalOnly bool, site string) error {
		if target == nil {
			return fmt.Errorf("live raw ABI plain closure %q has a nil target at %s", owner.Name(), site)
		}
		targetPlan, ok := plan.FunctionPlan(target)
		if !ok {
			return fmt.Errorf("live raw ABI plain closure %q target %q at %s is unplanned", owner.Name(), target.Name(), site)
		}
		if targetPlan.External == coro.Defined {
			if _, exact := raw.functions[target]; !exact || !plan.HasRawPlainVariant(target) {
				return fmt.Errorf("live raw ABI plain closure %q target %q at %s has no exact raw plain variant", owner.Name(), targetPlan.ID, site)
			}
			return nil
		}
		if _, schedulerWait := plan.ForeignSchedulerWaitCertificate(target); schedulerWait {
			if _, compilerOwned := raw.hostStack[owner]; !compilerOwned {
				return fmt.Errorf("live raw ABI plain closure %q reaches schedulerwait target %q (%s) at %s outside a compiler-owned raw host/scheduler-stack island",
					owner.Name(), targetPlan.ID, target.String(), site)
			}
			if targetPlan.External != coro.ExternalUnknownForeign || targetPlan.Emission != coro.EmitExternal ||
				targetPlan.Effect != coro.NoSuspend || targetPlan.Exec != coro.BlockForeign|coro.IRQUnsafe {
				return fmt.Errorf("live raw ABI plain closure %q reaches malformed schedulerwait target %q (%s) at %s (external=%s effect=%s exec=%s)",
					owner.Name(), targetPlan.ID, target.String(), site, targetPlan.External, targetPlan.Effect, targetPlan.Exec)
			}
			return nil
		}
		if _, compilerPlain := raw.externalPlain[target]; compilerPlain {
			// requiredPlain is itself a frozen build-owned physical ABI proof for
			// an exact C declaration reached by this closed raw island. It does not
			// erase the declaration's plan facts: only the precise external-known,
			// no-suspend, nonblocking shape is admissible here. schedulerwait was
			// handled above because its intentionally blocking shape has the
			// stronger host-stack ownership requirement.
			if targetPlan.External != coro.ExternalKnown || targetPlan.Emission != coro.EmitExternal ||
				targetPlan.Effect != coro.NoSuspend || targetPlan.Exec&(coro.BlockForeign|coro.ThreadAffine|coro.OpaqueExec) != 0 {
				return fmt.Errorf("live raw ABI plain closure %q reaches malformed compiler-required external target %q (%s) at %s (external=%s effect=%s exec=%s)",
					owner.Name(), targetPlan.ID, target.String(), site, targetPlan.External, targetPlan.Effect, targetPlan.Exec)
			}
			return nil
		}
		if callable, certified := plan.CallableContractCertificate(target); certified &&
			callable.Scope == coro.CallableContractScopeDeclaration &&
			coroRawPlainDirectForeignContractCompatible(callable.Contract) {
			// A raw ABI body executes on its native caller's stack. The same
			// default may-block declaration that becomes a worker await in the
			// managed twin is therefore an ordinary synchronous call here: it
			// may block the foreign caller, but it cannot occupy a scheduler
			// executor. Compiler-owned scheduler-stack leaves were placed in
			// externalPlain above and retain their stricter explicit-policy
			// gate.
			expectedExec := coro.BlockForeign | coro.IRQUnsafe |
				coro.CallableContractExecConstraints(callable.Contract)
			if targetPlan.External == coro.ExternalUnknownForeign &&
				targetPlan.Emission == coro.EmitExternal &&
				targetPlan.Effect == coro.NoSuspend &&
				targetPlan.Exec == expectedExec {
				return nil
			}
			return fmt.Errorf("live raw ABI plain closure %q reaches malformed direct foreign callable target %q (%s) at %s (external=%s effect=%s exec=%s expected-exec=%s)",
				owner.Name(), targetPlan.ID, target.String(), site,
				targetPlan.External, targetPlan.Effect, targetPlan.Exec, expectedExec)
		}
		if terminalOnly {
			// A terminal-only raw path has already committed to never returning
			// to the scheduler. It may finish that episode through a synchronous
			// legacy foreign call, even if that leaf can block (for example a
			// fatal-panic diagnostic write immediately followed by process exit).
			// This is deliberately local to the exact UnwindOnly reachability
			// proof: managed/open targets and real event suspension remain invalid.
			if targetPlan.External != coro.ExternalUnknownManaged &&
				targetPlan.Emission == coro.EmitExternal && targetPlan.Effect == coro.NoSuspend &&
				!targetPlan.Exec.Contains(coro.OpaqueExec) &&
				(targetPlan.External == coro.ExternalKnown ||
					targetPlan.External == coro.ExternalUnknownForeign && targetPlan.Exec.Contains(coro.BlockForeign)) {
				return nil
			}
			return fmt.Errorf("terminal-only live raw ABI plain closure %q reaches non-legacy external target %q (%s) at %s (external=%s effect=%s exec=%s)",
				owner.Name(), targetPlan.ID, target.String(), site, targetPlan.External, targetPlan.Effect, targetPlan.Exec)
		}
		if targetPlan.External != coro.ExternalKnown || targetPlan.Emission != coro.EmitExternal ||
			targetPlan.Effect != coro.NoSuspend || targetPlan.Exec&(coro.BlockForeign|coro.OpaqueExec) != 0 {
			return fmt.Errorf("live raw ABI plain closure %q reaches uncertified external target %q (%s) at %s (external=%s effect=%s exec=%s)",
				owner.Name(), targetPlan.ID, target.String(), site, targetPlan.External, targetPlan.Effect, targetPlan.Exec)
		}
		_, foreignNoBlock := plan.ForeignNoBlockCertificate(target)
		_, foreignSync := plan.ForeignSyncCertificate(target)
		_, assemblyNoSuspend := plan.AssemblyNoSuspendCertificate(target)
		if !foreignNoBlock && !foreignSync && !assemblyNoSuspend {
			return fmt.Errorf("live raw ABI plain closure %q reaches external target %q (%s) at %s without an exact foreign-noblock, foreign-sync, or assembly-no-suspend certificate",
				owner.Name(), targetPlan.ID, target.String(), site)
		}
		return nil
	}

	for fn := range raw.functions {
		functionPlan, ok := plan.FunctionPlan(fn)
		if !ok || !plan.HasRawPlainVariant(fn) || functionPlan.External != coro.Defined || plan.IgnoresBody(fn) {
			return fmt.Errorf("live raw ABI plain closure function %q has no owned RawPlainVariant plan", fn.Name())
		}
		if !functionPlan.RawPlainDemand {
			return fmt.Errorf(
				"live raw ABI plain closure function %q (%s) has no raw provenance",
				functionPlan.ID, fn.String(),
			)
		}
		_, physicalEntry := raw.entries[fn]
		if functionPlan.RawPlainEntry != physicalEntry {
			return fmt.Errorf("live raw ABI plain closure function %q raw-entry capability=%t, want physical-crossing=%t", functionPlan.ID, functionPlan.RawPlainEntry, physicalEntry)
		}
		if functionPlan.ManagedDemand == coro.NoDemand {
			if !functionPlan.RawPlainOnly || functionPlan.Emission != coro.EmitRawPlain ||
				functionPlan.Primary != coro.PrimaryPlain || functionPlan.FuncRep != coro.DirectPlain {
				return fmt.Errorf("raw-only live raw ABI plain closure function %q has inconsistent physical plan: %+v", functionPlan.ID, functionPlan)
			}
		} else {
			if functionPlan.RawPlainOnly {
				return fmt.Errorf("mixed live raw ABI plain closure function %q is incorrectly raw-only", functionPlan.ID)
			}
			if functionPlan.Effect.MaySuspend() {
				if functionPlan.Emission != coro.EmitCoroutine || functionPlan.Primary != coro.PrimaryCoroutine {
					return fmt.Errorf("suspending mixed live raw ABI plain closure function %q has inconsistent managed primary: %+v", functionPlan.ID, functionPlan)
				}
			} else if functionPlan.Emission != coro.EmitPlain || functionPlan.Primary != coro.PrimaryPlain {
				return fmt.Errorf("no-suspend mixed live raw ABI plain closure function %q has inconsistent shared plain body: %+v", functionPlan.ID, functionPlan)
			}
		}
		terminalOnly := raw.terminalOnly(fn)
		allowedEffects := managedOnlyEffects
		allowedExec := coro.ExecFlags(legacyExec)
		rawCForeignCalls := make(map[ssa.CallInstruction]struct{})
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				callPlan, planned := plan.CallPlan(call)
				if !planned || callPlan.Transport != coro.RawCCodePointer {
					continue
				}
				common := call.Common()
				if _, ordinary := call.(*ssa.Call); !ordinary || common.StaticCallee() != nil || common.IsInvoke() || common.Method != nil ||
					callPlan.Kind != coro.CallForeign || callPlan.Rep != coro.DirectPlain || !callPlan.Open ||
					callPlan.Unresolved != coro.UnknownForeign || callPlan.SyncDispatch {
					return fmt.Errorf("live raw ABI plain closure function %q has malformed raw C code-pointer call %q", functionPlan.ID, call.String())
				}
				rawCForeignCalls[call] = struct{}{}
			}
		}
		if len(rawCForeignCalls) != 0 {
			allowedEffects |= coro.WaitForeign
			allowedExec |= coro.BlockForeign
		}
		if terminalOnly {
			allowedEffects |= coro.WaitForeign
			allowedExec |= coro.BlockForeign
		}
		rawSyncEffects := coro.NoSuspend
		for call, certificate := range raw.rawSyncIntrinsics {
			if call == nil || call.Parent() != fn {
				continue
			}
			plannedCertificate, planned := plan.ElidedCallCertificate(call)
			if !planned || !plan.ElidesCall(call) || plannedCertificate != certificate {
				return fmt.Errorf("live raw ABI plain closure function %q (%s) lost its exact synchronous raw intrinsic certificate", functionPlan.ID, fn.String())
			}
			rawSyncEffects = rawSyncEffects.Join(coro.MayPark)
		}
		// Aggregate Effect/Exec intentionally retain conservative managed-primary
		// contributions from every explicit static call, including calls in a
		// block that cannot return normally. Raw feasibility instead validates
		// local facts and then every exact edge recursively, using the same CFG
		// terminal proof above. This keeps the managed fixed point untouched while
		// avoiding false ordinary reachability from fatal formatting/allocation.
		// A worker intrinsic is the sole local effect whose physical meaning
		// differs between the two bodies: managed parks, raw executes the ordinary
		// synchronous syscall. Prove every other source independently before
		// admitting that exact certified bit in the aggregate FunctionPlan.
		if unsupported := raw.basePolicyEffects[fn] &^ managedOnlyEffects; unsupported != 0 {
			return fmt.Errorf("live raw ABI plain closure function %q (%s) has unsupported declared raw-stack effect %s", functionPlan.ID, fn.String(), unsupported)
		}
		nonRawLocalEffects := coroRawPlainSSALocalEffect(fn).Join(raw.nonRawLocalEffects[fn])
		if unsupported := nonRawLocalEffects &^ allowedEffects; unsupported != 0 {
			return fmt.Errorf("live raw ABI plain closure function %q (%s) has real local suspend effect %s", functionPlan.ID, fn.String(), unsupported)
		}
		if unsupported := functionPlan.LocalEffect &^ (allowedEffects | rawSyncEffects); unsupported != 0 {
			return fmt.Errorf("live raw ABI plain closure function %q (%s) has real local suspend effect %s", functionPlan.ID, fn.String(), unsupported)
		}
		if unsupported := functionPlan.LocalExec &^ allowedExec; unsupported != 0 {
			return fmt.Errorf("live raw ABI plain closure function %q has unsupported legacy execution constraint %s", functionPlan.ID, unsupported)
		}
		for _, lowered := range plan.LoweredCalls(fn) {
			targetTerminalOnly := terminalOnly || lowered.UnwindOnly
			if err := validateTarget(fn, lowered.Target, targetTerminalOnly, "lowered helper "+lowered.LogicalName); err != nil {
				return err
			}
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if makeClosure, closure := instruction.(*ssa.MakeClosure); closure {
					target, exact := makeClosure.Fn.(*ssa.Function)
					if !exact || target == nil || len(makeClosure.Bindings) != len(target.FreeVars) {
						return fmt.Errorf("live raw ABI plain closure function %q has non-exact closure construction %q", functionPlan.ID, makeClosure.String())
					}
					targetTerminalOnly := terminalOnly || raw.instructionUnwindOnly(fn, instruction)
					if err := validateTarget(fn, target, targetTerminalOnly, "closure "+makeClosure.String()); err != nil {
						return err
					}
				}
				call, callInstruction := instruction.(ssa.CallInstruction)
				if !callInstruction || call.Common() == nil {
					continue
				}
				if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
					continue
				}
				callPlan, planned := plan.CallPlan(call)
				if !planned {
					if plan.ElidesCall(call) {
						continue
					}
					return fmt.Errorf("live raw ABI plain closure function %q call %q has no call plan", functionPlan.ID, call.String())
				}
				if callPlan.Kind == coro.CallSpawn {
					return fmt.Errorf("live raw ABI plain closure function %q spawns at %q", functionPlan.ID, call.String())
				}
				targetTerminalOnly := terminalOnly || raw.instructionUnwindOnly(fn, instruction)
				if call.Common().StaticCallee() == nil || call.Common().IsInvoke() || call.Common().Method != nil {
					if _, rawC := rawCForeignCalls[call]; rawC {
						continue
					}
					certificate, closed := raw.closedDynamic[call]
					if !closed || call.Common().IsInvoke() || call.Common().Method != nil || callPlan.Open || !callPlan.SyncDispatch ||
						callPlan.MayBeNil != certificate.MayBeNil || len(callPlan.Targets) != len(certificate.Targets) {
						return fmt.Errorf("live raw ABI plain closure function %q (%s) has dynamic/open call %q", functionPlan.ID, fn.String(), call.String())
					}
					for index, target := range certificate.Targets {
						targetID, found := plan.FunctionID(target)
						if !found || callPlan.Targets[index] != targetID {
							return fmt.Errorf("live raw ABI plain closure function %q closed dynamic call %q lost exact target %q", functionPlan.ID, call.String(), target.Name())
						}
						targetPlan, planned := plan.FunctionPlan(target)
						if !planned || targetPlan.External != coro.Defined || targetPlan.Effect != coro.NoSuspend ||
							targetPlan.Exec.Contains(coro.NeedsPreempt) || targetPlan.FuncRep != coro.Dispatch ||
							targetPlan.Primary != coro.PrimaryPlain || targetPlan.Emission != coro.EmitPlain {
							return fmt.Errorf("live raw ABI plain closure function %q closed dynamic call %q target %q is not an exact non-suspending descriptor-backed plain body",
								functionPlan.ID, call.String(), target.Name())
						}
					}
					continue
				}
				if callPlan.Open || len(callPlan.Targets) != 1 {
					return fmt.Errorf("live raw ABI plain closure function %q (%s) has dynamic/open call %q", functionPlan.ID, fn.String(), call.String())
				}
				target, found := plan.Function(callPlan.Targets[0])
				if !found {
					return fmt.Errorf("live raw ABI plain closure function %q call %q has unresolved target %q", functionPlan.ID, call.String(), callPlan.Targets[0])
				}
				if err := validateTarget(fn, target, targetTerminalOnly, "call "+call.String()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func coroRawPlainDirectForeignContractCompatible(contract coro.CallableContract) bool {
	if contract.Progress != coro.ProgressMayBlock ||
		contract.Reentry != coro.ReentryNone {
		return false
	}
	switch contract.Affinity {
	case coro.AffinityAnyThread, coro.AffinityCallerThread:
	default:
		return false
	}
	switch contract.Memory {
	case coro.MemoryByValue, coro.MemoryBorrowUntilReturn, coro.MemoryBorrowUntilComplete:
		return true
	default:
		return false
	}
}

func (in CoroPlanInput) closedStaticSpawnTarget(owner *ssa.Function, spawn *ssa.Go) (*ssa.Function, error) {
	if owner == nil || spawn == nil || spawn.Common() == nil || spawn.Parent() != owner {
		return nil, fmt.Errorf("requires an exact owner and call site")
	}
	common := spawn.Common()
	raw, direct := common.Value.(*ssa.Function)
	if !direct || raw == nil || common.IsInvoke() || common.Method != nil || common.StaticCallee() != raw {
		return nil, fmt.Errorf("requires a direct static function or method operand; closures, interfaces, and function values require descriptor spawn")
	}
	target := (*ssa.Function)(nil)
	redirected := false
	if in.callSitePlan != nil {
		site, frozen, err := in.callSitePlan(spawn)
		if err != nil {
			return nil, fmt.Errorf("read frozen static-spawn SitePlan: %w", err)
		}
		if !frozen {
			return nil, fmt.Errorf("spawn is absent from the frozen ProgramIR")
		}
		target = site.StaticSpawnTarget
		redirected = target != nil
	}
	if !redirected {
		target = raw
	}
	canonical, ok := in.ResolveFunction(target)
	if !ok || canonical == nil || canonical != target {
		return nil, fmt.Errorf("target %q is outside the frozen emission universe", target.Name())
	}
	if redirected && (target == raw || target.Synthetic == "") {
		return nil, fmt.Errorf("target %q is not one distinct compiler-owned spawn wrapper", target.Name())
	}
	// A source function literal with no free variables is context-free even
	// though x/tools records its lexical Parent. It has an exact static symbol
	// and receives every value through ordinary parameters, so it uses the same
	// DirectCoro spawn ABI as a package-level function. Capturing literals are
	// represented through MakeClosure and remain in the descriptor path.
	if len(target.FreeVars) != 0 || !redirected && target.Synthetic != "" ||
		target.Origin() != nil || len(target.TypeArgs()) != 0 {
		return nil, fmt.Errorf("target %q is not an exact non-capturing context-free function", target.Name())
	}
	if params := target.TypeParams(); params != nil && params.Len() != 0 {
		return nil, fmt.Errorf("target %q is a generic declaration", target.Name())
	}
	sig := target.Signature
	if sig == nil || sig.Variadic() ||
		typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 {
		return nil, fmt.Errorf("target %q must have a non-variadic, non-generic signature", target.Name())
	}
	if redirected && !types.Identical(common.Signature(), sig) {
		return nil, fmt.Errorf("target %q does not preserve the source spawn signature", target.Name())
	}
	wantArgs := sig.Params().Len()
	if sig.Recv() != nil {
		wantArgs++
	}
	if len(common.Args) != wantArgs {
		return nil, fmt.Errorf("target %q spawn arguments=%d do not match normalized receiver/parameters=%d", target.Name(), len(common.Args), wantArgs)
	}
	argument := 0
	if receiver := sig.Recv(); receiver != nil {
		if !types.Identical(common.Args[0].Type(), receiver.Type()) {
			return nil, fmt.Errorf("target %q receiver argument type %s does not match %s", target.Name(), common.Args[0].Type(), receiver.Type())
		}
		argument++
	}
	for parameter := 0; parameter < sig.Params().Len(); parameter, argument = parameter+1, argument+1 {
		if !types.Identical(common.Args[argument].Type(), sig.Params().At(parameter).Type()) {
			return nil, fmt.Errorf(
				"target %q argument %d type %s does not match parameter %s",
				target.Name(), argument, common.Args[argument].Type(), sig.Params().At(parameter).Type(),
			)
		}
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

// requiredCoroManagedSpawnTargets extracts the exact target set from the first
// immutable CallPlan fixed point. It does not infer targets from display names
// or source types. The caller reruns analysis with YieldOnly seeded on these
// bodies so every descriptor selected as a goroutine root publishes HasCoro.
func requiredCoroManagedSpawnTargets(plan *coro.SSAPlan) (map[*ssa.Function]struct{}, error) {
	if plan == nil {
		return nil, fmt.Errorf("managed descriptor spawn target discovery requires a coroutine plan")
	}
	targets := make(map[*ssa.Function]struct{})
	for _, owner := range plan.Functions() {
		if owner.Function == nil || plan.IgnoresBody(owner.Function) {
			continue
		}
		for _, block := range owner.Function.Blocks {
			for _, instruction := range block.Instrs {
				spawn, ok := instruction.(*ssa.Go)
				if !ok {
					continue
				}
				callPlan, found := plan.CallPlan(spawn)
				if !found || callPlan.Kind != coro.CallSpawn {
					return nil, fmt.Errorf("coroutine spawn in %q has no CallSpawn plan", owner.Plan.ID)
				}
				if callPlan.Rep != coro.Dispatch {
					continue
				}
				if callPlan.Open && callPlan.Unresolved != coro.UnknownManagedDispatch {
					return nil, fmt.Errorf(
						"coroutine descriptor spawn in %q has uncertified open target domain %v",
						owner.Plan.ID, callPlan.Unresolved,
					)
				}
				for _, targetID := range callPlan.Targets {
					target, found := plan.Function(targetID)
					if !found || target == nil {
						return nil, fmt.Errorf("coroutine descriptor spawn target %q is absent from the plan", targetID)
					}
					targets[target] = struct{}{}
				}
			}
		}
	}
	return targets, nil
}

func validateCoroSpawnPlan(plan *coro.SSAPlan) error {
	if plan == nil {
		return fmt.Errorf("coroutine spawn validation requires a coroutine plan")
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
				callPlan, found := plan.CallPlan(spawn)
				if !found {
					return fmt.Errorf("coroutine spawn in %q has no CallPlan", owner.Plan.ID)
				}
				switch callPlan.Rep {
				case coro.DirectCoro:
					if _, _, err := resolveCoroDirectStaticSpawnPlan(plan, spawn); err != nil {
						return fmt.Errorf(
							"closed static spawn in %q (%s) at %s, operand %q: %w",
							owner.Plan.ID, owner.Function.String(), owner.Function.Prog.Fset.Position(spawn.Pos()), spawn.Common().Value.String(), err,
						)
					}
				case coro.Dispatch:
					if _, err := plan.ResolveManagedDispatchSpawn(spawn); err != nil {
						return fmt.Errorf(
							"managed descriptor spawn in %q (%s) at %s, operand %q: %w",
							owner.Plan.ID, owner.Function.String(), owner.Function.Prog.Fset.Position(spawn.Pos()), spawn.Common().Value.String(), err,
						)
					}
				default:
					return fmt.Errorf(
						"coroutine spawn in %q has unsupported representation %s",
						owner.Plan.ID, callPlan.Rep,
					)
				}
			}
		}
	}
	return nil
}

// resolveCoroDirectStaticSpawnPlan extends the original non-method resolver to
// an exact static method call. x/tools SSA already places the receiver first in
// CallCommon.Args, matching cl's normalized physical method signature. No
// bound method value, interface invoke, or dynamic receiver dispatch enters
// this path.
func resolveCoroDirectStaticSpawnPlan(
	plan *coro.SSAPlan,
	spawn *ssa.Go,
) (*ssa.Function, coro.FunctionPlan, error) {
	if plan == nil || spawn == nil || spawn.Common() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a compilation CallPlan")
	}
	target, targetPlan, directErr := plan.ResolveClosedStaticSpawn(spawn)
	if directErr == nil {
		return target, targetPlan, nil
	}
	common := spawn.Common()
	raw, direct := common.Value.(*ssa.Function)
	if direct && raw != nil && raw.Signature != nil && raw.Signature.Recv() == nil {
		return nil, coro.FunctionPlan{}, directErr
	}
	if !direct || raw == nil || common.StaticCallee() != raw || common.IsInvoke() || common.Method != nil ||
		raw.Signature == nil || raw.Signature.Recv() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires an exact static function or method operand")
	}
	callPlan, found := plan.CallPlan(spawn)
	if !found || callPlan.Kind != coro.CallSpawn || callPlan.Rep != coro.DirectCoro ||
		callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"requires one closed non-nil DirectCoro spawn target, got kind=%v representation=%s open=%t may-be-nil=%t targets=%d",
			callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}
	target, found = plan.Function(callPlan.Targets[0])
	if !found || target == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("spawn target %q is absent from the compilation plan", callPlan.Targets[0])
	}
	targetPlan, found = plan.FunctionPlan(target)
	if !found || targetPlan.ID != callPlan.Targets[0] || targetPlan.External != coro.Defined ||
		targetPlan.Emission != coro.EmitCoroutine || targetPlan.Primary != coro.PrimaryCoroutine ||
		targetPlan.FuncRep != coro.DirectCoro || targetPlan.Demand != coro.AsyncDemand ||
		!targetPlan.Effect.Contains(coro.YieldOnly) {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"spawn method target %q is not one demanded preemptible direct coroutine (external=%s emission=%s primary=%s representation=%s demand=%s effect=%s)",
			callPlan.Targets[0], targetPlan.External, targetPlan.Emission, targetPlan.Primary,
			targetPlan.FuncRep, targetPlan.Demand, targetPlan.Effect,
		)
	}
	if target != raw || target.Signature == nil || target.Signature.Recv() == nil || target.Signature.Variadic() ||
		typeParamLen(target.Signature.TypeParams()) != 0 ||
		typeParamLen(target.Signature.RecvTypeParams()) != 0 {
		return nil, coro.FunctionPlan{}, fmt.Errorf("spawn target %q is not an exact non-generic method", targetPlan.ID)
	}
	ownerPlan, found := plan.FunctionPlan(spawn.Parent())
	if !found || ownerPlan.Emission != coro.EmitCoroutine || ownerPlan.Primary != coro.PrimaryCoroutine ||
		ownerPlan.Demand != coro.AsyncDemand || !ownerPlan.Effect.Contains(coro.YieldOnly) {
		return nil, coro.FunctionPlan{}, fmt.Errorf("spawn owner is not one demanded preemptible coroutine primary")
	}
	return target, targetPlan, nil
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

func validateCoroClosedStaticSpawnRunGate(conf *Config, plan *coro.SSAPlan, frameRetentionABI string) error {
	if conf == nil {
		return fmt.Errorf("validate coroutine spawn: missing build configuration")
	}
	if plan == nil {
		return fmt.Errorf("validate coroutine closed static spawn: runnable capability requires a coroutine plan")
	}
	// Main-return cancellation can safely retire ready/yielded children and a
	// structured await tree. A configured worker capability additionally owns
	// the exact ForeignWait cancel/drain protocol: shutdown publishes a sticky
	// task token, waits for the bounded worker's irreversible completion, lets
	// the compiler resume gate discard its result, and only then closes the
	// executor. Platform, host, generic park and opaque waits remain outside this
	// gate until their exact configured source capability is proved here.
	allowed := coro.YieldOnly | coro.AwaitStructured | coro.OutcomeStructured
	if frameRetentionABI == cl.CoroFrameRetentionParkABIV2 {
		allowed |= coro.MayPark
	}
	if conf.coroWorkerSupported() {
		allowed |= coro.WaitForeign
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
				callPlan, found := plan.CallPlan(spawn)
				if !found {
					return fmt.Errorf("validate coroutine spawn in %q: missing CallPlan", owner.Plan.ID)
				}
				var targets []coro.FunctionPlan
				switch callPlan.Rep {
				case coro.DirectCoro:
					_, target, err := resolveCoroDirectStaticSpawnPlan(plan, spawn)
					if err != nil {
						return fmt.Errorf("validate coroutine closed static spawn in %q: %w", owner.Plan.ID, err)
					}
					targets = append(targets, target)
				case coro.Dispatch:
					if _, err := plan.ResolveManagedDispatchSpawn(spawn); err != nil {
						return fmt.Errorf("validate coroutine descriptor spawn in %q: %w", owner.Plan.ID, err)
					}
					for _, targetID := range callPlan.Targets {
						target, found := plan.Function(targetID)
						if !found || target == nil {
							return fmt.Errorf("validate coroutine descriptor spawn in %q: target %q is absent", owner.Plan.ID, targetID)
						}
						targetPlan, found := plan.FunctionPlan(target)
						if !found {
							return fmt.Errorf("validate coroutine descriptor spawn in %q: target %q has no function plan", owner.Plan.ID, targetID)
						}
						targets = append(targets, targetPlan)
					}
				default:
					return fmt.Errorf("validate coroutine spawn in %q: unsupported representation %s", owner.Plan.ID, callPlan.Rep)
				}
				for _, target := range targets {
					effect := target.Effect.Normalize()
					if !effect.Contains(coro.YieldOnly) || effect&^allowed != 0 {
						targetName := "<unresolved>"
						trace := "unavailable"
						if targetFunction, found := plan.Function(target.ID); found && targetFunction != nil {
							targetName = targetFunction.String()
							trace = plan.OpaqueEffectTrace(targetFunction)
						}
						return fmt.Errorf(
							"validate coroutine spawn in %q (%s): target %q (%s) effect %s (local=%s declared=%s exec=%s) is outside the production main-return cancellation subset %s; opaque trace: %s",
							owner.Plan.ID, owner.Function.String(), target.ID, targetName, effect,
							target.LocalEffect.Normalize(), target.DeclaredEffect.Normalize(), target.Exec, allowed, trace,
						)
					}
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
		target               *ssa.Function
		noUnwind             bool
		unwindOnly           bool
		explicitStatusElided bool
		rawPlain             bool
	}
	byName := make(map[string]exactLoweredCall, len(left))
	for _, call := range left {
		if call.LogicalName == "" || call.Target == nil {
			return false
		}
		if _, duplicate := byName[call.LogicalName]; duplicate {
			return false
		}
		byName[call.LogicalName] = exactLoweredCall{
			target:               call.Target,
			noUnwind:             call.NoUnwind,
			unwindOnly:           call.UnwindOnly,
			explicitStatusElided: call.ExplicitStatusElided,
			rawPlain:             call.RawPlain,
		}
	}
	for _, call := range right {
		frozen, ok := byName[call.LogicalName]
		if call.LogicalName == "" || call.Target == nil || !ok || frozen.target != call.Target ||
			frozen.noUnwind != call.NoUnwind ||
			frozen.unwindOnly != call.UnwindOnly || frozen.explicitStatusElided != call.ExplicitStatusElided ||
			frozen.rawPlain != call.RawPlain {
			return false
		}
		delete(byName, call.LogicalName)
	}
	return len(byName) == 0
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
		managedPlain := !function.RawPlainOnly && function.Effect == coro.NoSuspend && !function.Exec.Contains(coro.NeedsPreempt) &&
			(function.FuncRep == coro.DirectPlain || function.FuncRep == coro.Dispatch) && function.Emission == coro.EmitPlain
		rawPlain := function.RawPlainDemand && function.RawPlainEntry && plan.HasRawPlainVariant(use.target) &&
			(function.RawPlainOnly && function.Emission == coro.EmitRawPlain ||
				!function.RawPlainOnly && (function.Emission == coro.EmitPlain || function.Emission == coro.EmitCoroutine))
		if function.External != coro.Defined || !managedPlain && !rawPlain {
			return fmt.Errorf("compiler runtime direct-plain callback %q is not a defined closed singleton with one non-suspending plain primary (external=%s effect=%s exec=%s representation=%s primary=%s emission=%s)",
				use.target.Name(), function.External, function.Effect, function.Exec, function.FuncRep, function.Primary, function.Emission)
		}
		targetID, ok := plan.FunctionID(use.target)
		if !ok {
			return fmt.Errorf("compiler runtime direct-plain callback %q has no FunctionID", use.target.Name())
		}
		argument := use.call.Common().Args[use.argument]
		value, ok := plan.ValuePlan(argument)
		if !ok || len(value.Funcs) != 1 || len(value.Funcs[0].Path) != 0 ||
			(value.Funcs[0].Rep != coro.DirectPlain && !rawPlain) ||
			value.Funcs[0].MayBeNil || len(value.Funcs[0].Targets) != 1 || value.Funcs[0].Targets[0] != targetID {
			return fmt.Errorf("compiler runtime direct-plain callback argument %d for %q is not an exact non-nil direct-plain singleton", use.argument, use.target.Name())
		}
	}
	return nil
}

// CoroPlanBuilder builds one compilation-scoped coroutine plan after every SSA
// package is available and the effective emission universe is frozen, but
// before fingerprinting, cache lookup, or LLVM codegen. A custom builder may
// refine analysis policy for focused tests; production uses the canonical
// default builder. A builder must return a
// plan created by input.Analyze so patch aliases and frontend structural
// identities cannot be bypassed. Active entry resolution uses archive-ready
// identities and fingerprints its canonical CoroPlanDigest into every package.
type CoroPlanBuilder func(input CoroPlanInput) (*coro.SSAPlan, error)

// defaultCoroPlanBuilder is the sole production coroutine analysis policy.
// LLGo owns the complete emission universe, so dynamic calls may use the
// closed-world resolver. The zero instruction budget selects the analyzer's
// bounded preemption default.
func defaultCoroPlanBuilder(input CoroPlanInput) (*coro.SSAPlan, error) {
	return input.Analyze(nil, coro.SSAConfig{DynamicResolution: coro.DynamicCHAClosed})
}

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
	PrintPackages bool // print package paths as they are compiled, like go build -v
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
	// GoBuildFlags contains normalized raw Go build flags forwarded to
	// go/packages. Callers use internal/goflags to parse supported compiler and
	// linker semantics into typed Config fields before calling Do.
	GoBuildFlags []string
	LinkOptions  LinkOptions
	// OmitDWARFByDefault controls linked builds only when -w was not
	// explicitly specified. Explicit -w and -w=false always win.
	OmitDWARFByDefault bool
	PCLNMode           PCLNMode
	// PCLNModeSet marks PCLNMode as authoritative. Command flags set it for
	// explicit requests; Do sets it after resolving the legacy environment
	// default.
	PCLNModeSet bool
	AllowNoBody bool // allow declarations without bodies, as go tool compile does

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
	GlobalRewrites   map[string]Rewrites
	ModuleHook       ModuleHook
	CoroPlanBuilder  CoroPlanBuilder
	CoroPlanObserver CoroPlanObserver

	// compilerBuildTags is a compiler-owned channel for isolated runtime-island
	// builds that deliberately do not enable the complete program-bootstrap
	// configuration. It is not a target capability declaration and production
	// target selection must never derive from it. Keeping it unexported prevents
	// users and named-target BuildTags from forging compiler/runtime ABI choices.
	compilerBuildTags []string
	// resolvedTargetBuildTags records the build tags contributed by the selected
	// named target. Coroutine target ABI classification happens after
	// crosscompile.Use has resolved GOOS/GOARCH and must see the same baremetal or
	// explicit host-reactor capability that selected the runtime source files.
	// It is internal frozen build state, never a user-controlled capability
	// channel.
	resolvedTargetBuildTags []string
}

func (conf *Config) coroWorkerSupported() bool {
	return conf != nil && nativeCoroWorkerRuntimeABI(conf)
}

func (conf *Config) coroNativeFleetSupported() bool {
	return conf != nil && nativeCoroTimerRuntimeABI(conf) && nativeCoroWorkerRuntimeABI(conf)
}

func (conf *Config) coroTargetCapabilities() coro.TargetCapabilities {
	return coro.NewTargetCapabilities(conf.coroWorkerSupported(), conf.coroNativeFleetSupported())
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
		Goos:               goos,
		Goarch:             goarch,
		BinPath:            bin,
		Mode:               mode,
		BuildMode:          BuildModeExe,
		AbiMode:            cabi.ModeAllFunc,
		OmitDWARFByDefault: mode != ModeGen,
		PCLNMode:           PCLNEmbedded,
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
	conf.PCLNMode = effectivePCLNMode(conf)
	conf.PCLNModeSet = true
	if conf.SizeReport && conf.SizeFormat == "" {
		conf.SizeFormat = "text"
	}
	if conf.SizeReport && conf.SizeLevel == "" {
		conf.SizeLevel = "module"
	}
	if err := validatePCLNMode(conf); err != nil {
		return nil, err
	}
	if err := ensureSizeReporting(conf); err != nil {
		return nil, err
	}
	if err := conf.LinkOptions.validate(); err != nil {
		return nil, err
	}
	conf.OptLevel = effectiveOptLevel(conf)
	// Handle crosscompile configuration first to set correct GOOS/GOARCH
	forceEspClang := conf.ForceEspClang || conf.Target != ""
	export, err := crosscompile.Use(conf.Goos, conf.Goarch, conf.Target, IsWasiThreadsEnabled(), forceEspClang, conf.OptLevel, conf.ltoMode(), conf.goGlobalDCEEnabled())
	if err != nil {
		return nil, fmt.Errorf("failed to setup crosscompile: %w", err)
	}
	applyBuildModeCompileFlags(conf.BuildMode, &export)
	// Update GOOS/GOARCH from export if target was used
	if conf.Target != "" && export.GOOS != "" {
		conf.Goos = export.GOOS
	}
	if conf.Target != "" && export.GOARCH != "" {
		conf.Goarch = export.GOARCH
	}
	conf.resolvedTargetBuildTags = conf.resolvedTargetBuildTags[:0]
	for _, value := range export.BuildTags {
		conf.resolvedTargetBuildTags = append(conf.resolvedTargetBuildTags, splitSourcePatchBuildTags(value)...)
	}
	if err := validateLinkOptions(conf, &export); err != nil {
		return nil, err
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

	emitDebugInfo := shouldEmitDebugInfo(conf, &export)
	cl.EnableDebug(emitDebugInfo)
	cl.EnableDbgSyms(emitDebugInfo)
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
	funcInfo := conf.Mode != ModeGen && conf.PCLNMode != PCLNNone
	prog.EnableFuncInfoMetadata(funcInfo)
	// Site records are inline-asm fragments inside function bodies. Darwin
	// DWARF builds avoid them because they disturb LLDB lexical scopes; Linux
	// still needs them because its restricted dynamic symbol table cannot
	// reconstruct every Go entry PC through dlsym. External mode always needs
	// final-PC sites for sidecar construction.
	prog.EnableFuncInfoSites(shouldEnablePCLNSites(conf, funcInfo, emitDebugInfo))
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
	// Alternate packages are compiler support inputs, not user test roots.
	// Loading their test variants duplicates runtime packages in ModeTest and
	// gives shared generic instances no exact emission-package identity. The
	// initial graph above retains Tests/NeedForTest and still owns user tests.
	altCfg.Tests = false
	altCfg.Mode &^= packages.NeedForTest
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
	if emitDebugInfo || mode == ModeGen {
		passOpt = false
	}
	if emitDebugInfo {
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
			if err := finalizeRuntimePCLN(ctx, outFmts, verbose); err != nil {
				return nil, err
			}
			if conf.Mode == ModeBuild && conf.SizeReport {
				if err := reportBinarySize(outFmts.Out, conf.SizeFormat, conf.SizeLevel, allPkgs); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: size report failed: %v\n", err)
				}
			}

			// Generate C headers for c-archive and c-shared modes before linking
			if ctx.buildConf.BuildMode == BuildModeCArchive || ctx.buildConf.BuildMode == BuildModeCShared {
				libname := strings.TrimSuffix(filepath.Base(outFmts.Out), conf.AppExt)
				headerPath := filepath.Join(filepath.Dir(outFmts.Out), libname) + ".h"
				pkgs := cHeaderPackages(allPkgs)
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
	coroNativePipeBuildTag  = "llgo_coro_native_pipe"
	coroNativeTimerBuildTag = "llgo_coro_native_timer"
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
	if conf.PCLNMode == PCLNExternal {
		// The loader is part of the package ABI and therefore of the package
		// cache key. Embedded and none builds compile no sidecar probing code.
		tags = append(tags, "llgo_pclntab_external")
	}
	if conf.AbiMode == cabi.ModeAllFunc {
		tags = append(tags, "llgo_abi_2")
	}
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
		case coroNativePipeBuildTag, coroNativeTimerBuildTag:
			return fmt.Errorf("build tag %q from %s is a compiler-reserved capability and cannot be supplied externally", tag, source)
		}
	}
	return nil
}

func buildCoroPlan(ctx *context, packages ...*aPackage) error {
	if ctx == nil || ctx.buildConf == nil {
		return nil
	}
	if err := validateCoroProgramBootstrapConfig(ctx.buildConf); err != nil {
		return err
	}
	ctx.coroProgramBootstraps = nil
	if ctx.buildConf.BuildMode == BuildModeCArchive {
		return fmt.Errorf("enable coroutine child await: c-archive requires flattened package members and an explicit host bootstrap extraction contract")
	}
	builder := ctx.buildConf.CoroPlanBuilder
	if builder == nil {
		builder = defaultCoroPlanBuilder
	}
	if len(packages) != 0 {
		if err := prepareCoroEmissionUniverse(ctx, packages); err != nil {
			return fmt.Errorf("prepare coroutine emission universe: %w", err)
		}
	}
	if len(packages) != 0 && ctx.coroEmission == nil {
		return fmt.Errorf("enable coroutine entry resolution: prepared emission universe is required")
	}
	analyzedPlans := make(map[*coro.SSAPlan]struct{})
	var analyzedPlansMu sync.Mutex
	var requiredRoots coro.Roots
	var requiredPlain map[*ssa.Function]struct{}
	var requiredHostPlain map[*ssa.Function]struct{}
	var requiredDirectPlain []requiredCoroDirectPlainCallArgument
	var requiredClosedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate
	var requiredGlobalFunctionSlots map[ssa.CallInstruction]coroGlobalFunctionSlotProof
	var frameRetentionABI string
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
		// Preserve the exact compiler-owned raw host/scheduler-stack island
		// before source-level C callback closures are merged into requiredPlain.
		requiredHostPlain = maps.Clone(requiredPlain)
		// Reaching this call proves that the complete runtime universe contained
		// and validated every compiler-owned scheduler/runtime root required by
		// the selected target profile.
		frameRetentionABI = validatedCoroFrameRetentionABI(ctx, true)
		requiredGlobalFunctionSlots = ctx.coroGlobalFunctionSlots
	}
	if ctx.coroEmission != nil {
		rawABIRoots, rawABIPlain, err := requiredCoroRawABIEntryRoots(ctx)
		if err != nil {
			return err
		}
		requiredRoots = append(requiredRoots, rawABIRoots...)
		if requiredPlain == nil {
			requiredPlain = make(map[*ssa.Function]struct{})
		}
		for function := range rawABIPlain {
			requiredPlain[function] = struct{}{}
		}
		directPlain, plainClosure, err := requiredCoroDirectPlainCallArguments(ctx, requiredClosedDynamic)
		if err != nil {
			return err
		}
		for function := range plainClosure {
			requiredPlain[function] = struct{}{}
		}
		requiredDirectPlain = appendUniqueCoroDirectPlainCallArguments(requiredDirectPlain, directPlain...)
	}
	requiredRoots = appendCoroDirectPlainRoots(requiredRoots, requiredDirectPlain)
	managedEntryRoots, err := requiredCoroProgramManagedEntryRoots(ctx)
	if err != nil {
		return err
	}
	requiredRoots = append(requiredRoots, managedEntryRoots...)
	patchInitRoots, err := requiredCoroPatchInitEntryRoots(ctx)
	if err != nil {
		return err
	}
	requiredRoots = append(requiredRoots, patchInitRoots...)
	requiredRoots = mergeCoroRequiredRoots(requiredRoots)
	input := CoroPlanInput{
		Program:                     ctx.progSSA,
		requiredRoots:               requiredRoots,
		requiredPlain:               requiredPlain,
		requiredHostPlain:           requiredHostPlain,
		requiredDirectPlain:         requiredDirectPlain,
		requiredClosedDynamic:       requiredClosedDynamic,
		requiredGlobalFunctionSlots: requiredGlobalFunctionSlots,
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
		input.localBodyFacts = ctx.coroEmission.CoroLocalBodyFacts
		input.functionBackground = ctx.coroEmission.FunctionBackground
		input.rawCFunctionType = func(typ types.Type) (bool, error) {
			if typ == nil {
				return false, nil
			}
			if _, signature := types.Unalias(typ).Underlying().(*types.Signature); !signature {
				return false, nil
			}
			return ctx.prog.TypeBackground(typ) == llssa.InC, nil
		}
		input.foreignNoBlock = ctx.coroEmission.CoroForeignNoBlockCertificate
		input.foreignSync = ctx.coroEmission.CoroForeignSyncCertificate
		input.foreignSchedulerWait = ctx.coroEmission.CoroForeignSchedulerWaitCertificate
		input.foreignWorker = ctx.coroEmission.CoroForeignWorkerCertificate
		input.callableIdentity = ctx.coroEmission.CoroCallableIdentityCertificate
		input.callableContract = ctx.coroEmission.CoroCallableContractCertificate
		input.noPreempt = ctx.coroEmission.CoroNoPreemptCertificate
		input.noUnwind = ctx.coroEmission.CoroNoUnwindCertificate
		input.trustedInlineCall = ctx.coroEmission.CoroTrustedInlineCallCertificate
		input.dynamicImplements = ctx.coroEmission.CoroDynamicImplements
		input.assemblyNoSuspend = func(fn *ssa.Function) (string, bool, error) {
			certificate, ok, err := ctx.coroEmission.CoroAssemblyNoSuspendCertificate(fn)
			return certificate.ID, ok, err
		}
		input.callSitePlan = ctx.coroEmission.CoroCallSitePlan
		input.rawFunctionAddressCallArgument = ctx.coroEmission.CoroRawFunctionAddressCallArgument
		input.staticCodeAddressCallArgument = ctx.coroEmission.CoroStaticCodeAddressCallArgument
		input.demandReferences = ctx.coroEmission.CoroDemandReferences
		input.syncDemandReferences = ctx.coroEmission.CoroSyncDemandReferences
		input.loweredCalls = ctx.coroEmission.CoroLoweredCalls
		input.augmentFunctionIDs = func(config coro.FunctionIDConfig) coro.FunctionIDConfig {
			if config.CoroABI == "" {
				config.CoroABI = activeCoroABIVersion(ctx.buildConf)
			}
			if config.SchedulerABI == "" {
				config.SchedulerABI = activeCoroSchedulerABIVersion(ctx.buildConf)
			}
			config.ArchiveReady = true
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
	analyzedPlansMu.Lock()
	if _, ok := analyzedPlans[plan]; !ok {
		analyzedPlansMu.Unlock()
		return fmt.Errorf("validate coroutine plan: builder must return a plan created by CoroPlanInput.Analyze")
	}
	analyzedPlansMu.Unlock()
	if ctx.coroEmission != nil {
		if err := ctx.coroEmission.ValidateCoroPlan(plan); err != nil {
			return fmt.Errorf("validate coroutine plan coverage: %w", err)
		}
	}
	if err := validateCoroClosedStaticSpawnRunGate(ctx.buildConf, plan, frameRetentionABI); err != nil {
		return err
	}
	var metadata coro.PlanDigestMetadata
	var digest string
	var loweringFacts coro.LoweringFacts
	var loweringFactsDigest string
	if ctx.coroEmission != nil {
		if ctx.coroEmission == nil {
			return fmt.Errorf("build coroutine lowering facts: missing frozen emission universe")
		}
		factsReport, factsErr := ctx.coroEmission.BuildCoroLoweringFactsReport(plan)
		if factsErr != nil {
			return fmt.Errorf("build coroutine lowering facts: %w", factsErr)
		}
		loweringFacts = factsReport.Facts
		loweringFactsDigest = factsReport.Digest
		metadata, err = buildCoroPlanDigestMetadata(ctx)
		if err != nil {
			return fmt.Errorf("build coroutine plan digest metadata: %w", err)
		}
		metadata.FrameRetentionABI = frameRetentionABI
		metadata.LoweringFactsSchema = loweringFacts.Schema
		metadata.LoweringFactsDigest = loweringFactsDigest
		digest, err = plan.CoroPlanDigest(metadata)
		if err != nil {
			return fmt.Errorf("build coroutine plan digest: %w", err)
		}
	}
	ctx.coroPlan = plan
	ctx.coroPlanDigest = digest
	ctx.coroPlanMetadata = metadata
	ctx.coroLoweringFacts = loweringFacts
	ctx.coroLoweringFactsDigest = loweringFactsDigest
	ctx.clCompilation = &cl.Compilation{
		CoroPlan:                plan,
		CoroPlanObserver:        ctx.buildConf.CoroPlanObserver,
		CoroTargetCapabilities:  ctx.buildConf.coroTargetCapabilities(),
		CoroFrameRetentionABI:   frameRetentionABI,
		CoroPlanDigest:          digest,
		CoroLoweringFacts:       loweringFacts,
		CoroLoweringFactsDigest: loweringFactsDigest,
		CoroABI:                 metadata.CoroABI,
		SchedulerABI:            metadata.SchedulerABI,
		PanicABI:                metadata.PanicABI,
		FuncRepABI:              metadata.FuncRepABI,
		EmissionUniverse:        ctx.coroEmission,
	}
	if ctx.coroEmission != nil {
		bootstraps, err := prepareCoroProgramBootstrapsV1(ctx)
		if err != nil {
			ctx.coroPlan = nil
			ctx.coroPlanDigest = ""
			ctx.coroPlanMetadata = coro.PlanDigestMetadata{}
			ctx.coroLoweringFacts = coro.LoweringFacts{}
			ctx.coroLoweringFactsDigest = ""
			ctx.clCompilation = nil
			ctx.coroProgramBootstraps = nil
			return fmt.Errorf("prepare coroutine program bootstrap before codegen: %w", err)
		}
		ctx.coroProgramBootstraps = bootstraps
	}
	if ctx.coroEmission != nil {
		if err := validateCoroUnwindOnlyLoweredCalls(plan, metadata.PanicABI); err != nil {
			ctx.coroPlan = nil
			ctx.coroPlanDigest = ""
			ctx.coroPlanMetadata = coro.PlanDigestMetadata{}
			ctx.coroLoweringFacts = coro.LoweringFacts{}
			ctx.coroLoweringFactsDigest = ""
			ctx.clCompilation = nil
			ctx.coroProgramBootstraps = nil
			return fmt.Errorf("validate coroutine unwind-only lowered calls before codegen: %w", err)
		}
	}
	return nil
}

// mergeCoroRequiredRoots preserves first-discovery order while joining every
// independently frozen capability for the same exact function. A runtime ABI
// body may be both a compiler-owned synchronous entry and an exported raw ABI
// entry; retaining two Root records is semantically redundant and obscures
// which one physical entry the plan must expose.
func mergeCoroRequiredRoots(roots coro.Roots) coro.Roots {
	result := make(coro.Roots, 0, len(roots))
	byFunction := make(map[*ssa.Function]int, len(roots))
	for _, root := range roots {
		if index, duplicate := byFunction[root.Function]; duplicate {
			result[index].Demand = result[index].Demand.Join(root.Demand)
			result[index].ManagedDemand = result[index].ManagedDemand.Join(root.ManagedDemand)
			result[index].RawPlainDemand = result[index].RawPlainDemand || root.RawPlainDemand
			continue
		}
		byFunction[root.Function] = len(result)
		result = append(result, root)
	}
	return result
}

func appendCoroDirectPlainRoots(roots coro.Roots, uses []requiredCoroDirectPlainCallArgument) coro.Roots {
	rooted := make(map[*ssa.Function]int, len(roots)+len(uses))
	for index, root := range roots {
		if _, duplicate := rooted[root.Function]; !duplicate {
			rooted[root.Function] = index
		}
	}
	for _, use := range uses {
		if use.target == nil {
			continue
		}
		if index, ok := rooted[use.target]; ok {
			roots[index].RawPlainDemand = true
			continue
		}
		rooted[use.target] = len(roots)
		roots = append(roots, coro.Root{Function: use.target, RawPlainDemand: true})
	}
	return roots
}

// validateCoroUnwindOnlyLoweredCalls preserves the panic boundary's fail-
// closed physical contract. Unwind-only edges do not taint an owner's normal-
// return plan. A plain owner still runs on a native activation and retains the
// legacy helper contract; only a physical coroutine owner needs an
// ExplicitStatus recipe for every such edge.
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
			if panicABI == coro.PanicExplicitStatusABIV0 {
				if owner.Plan.Emission != coro.EmitCoroutine {
					continue
				}
				if lowered.ExplicitStatusElided {
					// The immutable frontend fact is accepted only for a source
					// terminal operation wholly owned by the ExplicitStatus emitter.
					// Its plain helper remains demanded for a possible plain primary,
					// but no call is emitted from this physical coroutine body.
					continue
				}
				targetPlan, ok := plan.FunctionPlan(lowered.Target)
				if ok && targetPlan.External == coro.Defined && targetPlan.Emission == coro.EmitPlain &&
					targetPlan.Primary == coro.PrimaryPlain && targetPlan.FuncRep != coro.DirectCoro &&
					targetPlan.Demand != coro.NoDemand && targetPlan.Effect == coro.NoSuspend &&
					targetPlan.Exec&(coro.MayUnwind|coro.BlockForeign|coro.ThreadAffine|coro.NeedsPreempt|coro.OpaqueExec) == 0 {
					// UnwindOnly describes the owner's control-flow path, not a
					// requirement that every helper can itself produce a panic
					// outcome. An exact defined no-suspend/no-unwind plain helper
					// (for example interface type-word extraction) is called
					// directly by resolveCoroLoweredRuntimeCall and cannot bypass
					// ExplicitStatus transport. Keep its unique plain body rather
					// than manufacturing a coroutine/outcome wrapper.
					continue
				}
				if !ok || targetPlan.External != coro.Defined || targetPlan.Emission != coro.EmitCoroutine ||
					targetPlan.Primary != coro.PrimaryCoroutine ||
					(targetPlan.FuncRep != coro.DirectCoro && targetPlan.FuncRep != coro.Dispatch) ||
					!targetPlan.Demand.Contains(coro.AsyncDemand) ||
					!targetPlan.Effect.Contains(coro.OutcomeStructured) {
					return fmt.Errorf(
						"unwind-only lowered call %q in %q has no exact ExplicitStatus coroutine child (target=%q external=%s emission=%s primary=%s representation=%s demand=%s effect=%s exec=%s)",
						lowered.LogicalName, owner.Plan.ID, targetPlan.ID, targetPlan.External,
						targetPlan.Emission, targetPlan.Primary, targetPlan.FuncRep, targetPlan.Demand, targetPlan.Effect, targetPlan.Exec,
					)
				}
				continue
			}
			if panicABI != coro.PanicLegacyABIV0 {
				return fmt.Errorf("lowered call %q in %q is unwind-only under unsupported panic ABI %q", lowered.LogicalName, owner.Plan.ID, panicABI)
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
// managed closure to contain only bounded direct calls to plain primary bodies.
// A target may still have FuncRep=Dispatch when some other consumer stores it
// as a first-class value; representation is independent from the one primary
// body selected by this exact static edge. In particular, a
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
		functionPlan.Emission != coro.EmitPlain || functionPlan.Primary != coro.PrimaryPlain {
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
	return coro.PhysicalABIV1
}

// requiredCoroProgramManagedEntryRoots injects the exact main-package
// initializer and main body as managed async-capable roots for the runnable
// startup program. Duplicate builder roots are harmless: AnalyzeSSA joins
// demand by canonical function. Descriptor-only builds keep their historical
// explicit-root contract and legacy native entry.
func requiredCoroProgramManagedEntryRoots(ctx *context) (coro.Roots, error) {
	if ctx == nil || ctx.buildConf == nil {
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

// requiredCoroRawABIEntryRoots turns each source-level physical export or
// alias into an explicit raw root before analysis. EmissionUniverse already
// froze the distinction between an ordinary managed Go linkname pair and a
// real external ABI crossing; using that exact certificate here keeps
// planning and codegen from independently guessing from comments. Bodyless C
// declarations remain foreign leaves and therefore do not become Go roots.
func requiredCoroRawABIEntryRoots(ctx *context) (coro.Roots, map[*ssa.Function]struct{}, error) {
	if ctx == nil || ctx.coroEmission == nil {
		return nil, nil, nil
	}
	var roots coro.Roots
	plain := make(map[*ssa.Function]struct{})
	for _, fn := range ctx.coroEmission.Functions() {
		directive, err := ctx.coroEmission.CoroRawABIDirective(fn)
		if err != nil {
			return nil, nil, fmt.Errorf("classify coroutine raw ABI entry %q: %w", fn.Name(), err)
		}
		if directive == "" {
			continue
		}
		goBody, err := frozenGoEmittedBody(ctx.coroEmission, fn)
		if err != nil {
			return nil, nil, fmt.Errorf("classify coroutine raw ABI entry %q (%s): %w", fn.Name(), directive, err)
		}
		if !goBody {
			continue
		}
		roots = append(roots, coro.Root{Function: fn, RawPlainDemand: true})
		plain[fn] = struct{}{}
	}
	return roots, plain, nil
}

// requiredCoroPatchInitEntryRoots preserves the public half of every patched
// package initializer. Source SSA import edges target the original initializer,
// while cl renames that body to $hasPatch and emits a compiler-inserted call to
// it from the alternate public initializer. Consequently the public function
// is an exact linker entry, not a source-call-graph root.
func requiredCoroPatchInitEntryRoots(ctx *context) (coro.Roots, error) {
	if ctx == nil || ctx.coroEmission == nil {
		return nil, nil
	}
	entries, err := ctx.coroEmission.CoroPatchInitEntries()
	if err != nil {
		return nil, err
	}
	roots := make(coro.Roots, 0, len(entries))
	for _, entry := range entries {
		goBody, err := frozenGoEmittedBody(ctx.coroEmission, entry)
		if err != nil {
			return nil, fmt.Errorf("classify coroutine patch initializer %q: %w", entry.Name(), err)
		}
		if !goBody {
			return nil, fmt.Errorf("coroutine patch initializer %q has no emitted Go body", entry.Name())
		}
		roots = append(roots, coro.Root{Function: entry, Demand: coro.AsyncDemand})
	}
	return roots, nil
}

func activeCoroSchedulerABIVersion(conf *Config) string {
	if conf != nil && conf.coroWorkerSupported() {
		return coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	}
	return coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
}

func activeCoroPanicABIVersion(conf *Config) string {
	return coro.PanicExplicitStatusABIV0
}

func activeCoroFuncRepABIVersion(conf *Config) string {
	return coro.FuncRepABIV1
}

// nativeCoroDoorbellRuntimeABI mirrors the production target file selection
// for the compiler-owned callback root, bootstrap hash, and entry relocation.
// Named targets remain excluded until their OS/runtime contract explicitly
// opts into the POSIX pipe backend. llgo_coro_host is an explicit request for
// the embedding-owned pull reactor even on a POSIX host, so it must not also
// acquire the native pipe capability.
func nativeCoroDoorbellRuntimeABI(conf *Config) bool {
	if conf == nil || conf.Target != "" ||
		(conf.Goos != "darwin" && conf.Goos != "linux") {
		return false
	}
	for _, tag := range []string{"baremetal", "tinygo.wasm", "wasip2", "wasm_unknown", "llgo_coro_host", "coro_runtime_adapter_test"} {
		if configHasBuildTag(conf, tag) {
			return false
		}
	}
	return true
}

// nativeCoroWorkerRuntimeABI is the exact production adapter currently able
// to consume the bounded worker park/resume hooks. Host-pull, WASM, embedded,
// bare-metal, named-target, and test adapters need their own event transport;
// selecting the compiler lowering there would otherwise emit unresolved
// pthread/queue hooks and, more importantly, claim a cross-thread capability
// the target has not provided.
func nativeCoroWorkerRuntimeABI(conf *Config) bool {
	return nativeCoroDoorbellRuntimeABI(conf)
}

// hostCoroPullRuntimeABI selects the allocation-free, embedding-owned reactor
// ABI. It mirrors coro_target_host_llgo.go: wasm architectures, baremetal
// targets, and targets which explicitly opt into llgo_coro_host use bounded V2
// slices and export POD pull actions. The test adapter is a separate runtime
// implementation and remains excluded. This says nothing about an ordinary
// WASI command providing a reactor; _start may perform only the initial slice,
// and an embedding must keep the instance alive and consume the pull ABI.
func hostCoroPullRuntimeABI(conf *Config) bool {
	if conf == nil ||
		nativeCoroDoorbellRuntimeABI(conf) || configHasBuildTag(conf, "coro_runtime_adapter_test") {
		return false
	}
	return conf.Goarch == "wasm" || configHasBuildTag(conf, "baremetal") ||
		configHasBuildTag(conf, "llgo_coro_host")
}

func validateCoroHostPullEntryConfig(conf *Config, pyInit bool) error {
	if pyInit && hostCoroPullRuntimeABI(conf) {
		return fmt.Errorf("coroutine host-pull executable cannot own Python initialization: asynchronous completion has no compiler-owned Py_Finalize return edge")
	}
	return nil
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

// nativeCoroFleetRuntimeABI is the only timer-capable native coroutine target.
// It is derived from the immutable architecture plan and target ABI; there is no second
// feature tag which can select a competing single-P runtime island.
func nativeCoroFleetRuntimeABI(conf *Config) bool {
	return conf != nil && conf.coroNativeFleetSupported()
}

// validatedCoroFrameRetentionABI selects the sole stackless frame-lifetime
// identity only after the caller has successfully closed and
// signature-validated the compiler-owned runtime root plan. Frame retention is
// a target-neutral LLVM coroutine property: timer, poll, worker, and host-pull
// transports decide how a parked frame becomes ready, not whether that frame
// remains alive. The redundant complete-universe check keeps incomplete
// report/test universes fail-closed even if this selector is called directly.
func validatedCoroFrameRetentionABI(ctx *context, runtimePlanValidated bool) string {
	if !runtimePlanValidated || ctx == nil || ctx.coroEmission == nil || !ctx.coroEmission.CompleteRuntimeABI() ||
		ctx.buildConf == nil {
		return ""
	}
	return cl.CoroFrameRetentionParkABIV2
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
	for _, tag := range conf.resolvedTargetBuildTags {
		if tag == want {
			return true
		}
	}
	return false
}

func validCoroProgramRunResultPointerV2(typ types.Type) bool {
	pointer, ok := types.Unalias(typ).(*types.Pointer)
	if !ok {
		return false
	}
	result, ok := pointer.Elem().Underlying().(*types.Struct)
	if !ok || result.NumFields() != 8 {
		return false
	}
	for index := 0; index < result.NumFields(); index++ {
		if !types.Identical(result.Field(index).Type(), types.Typ[types.Uint32]) || result.Tag(index) != "" {
			return false
		}
	}
	return true
}

func validCoroHostActionPointerV1(typ types.Type) bool {
	pointer, ok := types.Unalias(typ).(*types.Pointer)
	if !ok {
		return false
	}
	action, ok := pointer.Elem().Underlying().(*types.Struct)
	if !ok || action.NumFields() != 8 {
		return false
	}
	for index := 0; index < action.NumFields(); index++ {
		if !types.Identical(action.Field(index).Type(), types.Typ[types.Uint32]) || action.Tag(index) != "" {
			return false
		}
	}
	return true
}

func validateCoroHostPullRuntimeFunctionV1(name string, fn *ssa.Function) (bool, error) {
	switch name {
	case coroHostNextActionSymbolV1, coroHostProfileSymbolV1, coroHostNextDeadlineSymbolV1,
		coroHostPublishTimeSymbolV1, coroHostAckCancelSymbolV1, coroHostContinueSliceSymbolV1:
	default:
		return false, nil
	}
	sig := fn.Signature
	if sig == nil || sig.Recv() != nil || sig.Variadic() || typeParamLen(sig.TypeParams()) != 0 ||
		typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
		return true, fmt.Errorf("coroutine host-pull runtime ABI %q has a receiver, variadic/type parameters, closure state, or no signature", name)
	}
	uint32Type := types.Typ[types.Uint32]
	boolType := types.Typ[types.Bool]
	allUint32Params := func(count int) bool {
		if sig.Params().Len() != count {
			return false
		}
		for index := 0; index < count; index++ {
			if !types.Identical(sig.Params().At(index).Type(), uint32Type) {
				return false
			}
		}
		return true
	}
	oneResult := func(want types.Type) bool {
		return sig.Results().Len() == 1 && types.Identical(sig.Results().At(0).Type(), want)
	}
	switch name {
	case coroHostNextActionSymbolV1:
		if sig.Params().Len() == 1 && validCoroHostActionPointerV1(sig.Params().At(0).Type()) && oneResult(uint32Type) {
			return true, nil
		}
		return true, fmt.Errorf("coroutine host next-action ABI %q must have exact func(*{8 x uint32}) uint32 signature", name)
	case coroHostProfileSymbolV1:
		if allUint32Params(0) && oneResult(uint32Type) {
			return true, nil
		}
		return true, fmt.Errorf("coroutine host profile ABI %q must have exact func() uint32 signature", name)
	case coroHostNextDeadlineSymbolV1:
		if sig.Params().Len() == 1 && validCoroHostActionPointerV1(sig.Params().At(0).Type()) && oneResult(boolType) {
			return true, nil
		}
		return true, fmt.Errorf("coroutine host next-deadline ABI %q must have exact func(*{8 x uint32}) bool signature", name)
	case coroHostPublishTimeSymbolV1:
		if allUint32Params(2) && oneResult(boolType) {
			return true, nil
		}
		return true, fmt.Errorf("coroutine host publish-time ABI %q must have exact func(uint32, uint32) bool signature", name)
	case coroHostAckCancelSymbolV1:
		if allUint32Params(4) && oneResult(boolType) {
			return true, nil
		}
		return true, fmt.Errorf("coroutine host cancel-ack ABI %q must have exact func(uint32, uint32, uint32, uint32) bool signature", name)
	case coroHostContinueSliceSymbolV1:
		if sig.Params().Len() == 8 && oneResult(uint32Type) && validCoroProgramRunResultPointerV2(sig.Params().At(7).Type()) {
			for index := 0; index != 7; index++ {
				if !types.Identical(sig.Params().At(index).Type(), uint32Type) {
					return true, fmt.Errorf("coroutine host continue-slice ABI %q must have seven uint32 parameters followed by *{8 x uint32}", name)
				}
			}
			return true, nil
		}
		return true, fmt.Errorf("coroutine host continue-slice ABI %q must have exact func(7 x uint32, *{8 x uint32}) uint32 signature", name)
	default:
		panic("unreachable host-pull runtime ABI validator")
	}
}

// requiredCoroProgramRuntimePlan returns the Go bodies referenced only by
// compiler-generated entry/coroutine IR and their exact static call closure.
// They are not visible from the application's source roots. The closure is a
// trusted raw host/scheduler-stack island: CFG loops do not turn its fixed C
// ABI into a coroutine. Exact ordinary C leaves receive a temporary
// compatible-known summary; schedulerwait leaves retain their managed
// unknown-foreign/blocking summary and are admitted only by raw validation.
// Fallback SSA stubs remain ignored, and ordinary C declarations outside this
// compiler-owned closure stay unknown foreign.
func requiredCoroProgramRuntimePlan(ctx *context) (coro.Roots, map[*ssa.Function]struct{}, []requiredCoroDirectPlainCallArgument, map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate, error) {
	if ctx == nil || ctx.buildConf == nil {
		return nil, nil, nil, nil, nil
	}
	if ctx.coroSSAEmission == nil || ctx.coroEmission == nil {
		return nil, nil, nil, nil, fmt.Errorf("coroutine program bootstrap runtime roots require a frozen emission universe")
	}
	closedDynamic, err := proveCoroTLSDestructorClosedDynamicCalls(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	privateFieldClosedDynamic, err := proveCoroPrivateFunctionFieldCalls(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for call, certificate := range privateFieldClosedDynamic {
		if previous, exists := closedDynamic[call]; exists && !sameCoroClosedDynamicCallCertificate(previous, certificate) {
			return nil, nil, nil, nil, fmt.Errorf("closed dynamic call in %q has conflicting TLS-field and private-field proofs", call.Parent().Name())
		}
		closedDynamic[call] = cloneCoroClosedDynamicCallCertificate(certificate)
	}
	globalClosedDynamic, globalFunctionSlots, err := proveCoroGlobalFunctionSlotClosedDynamicCalls(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for call, certificate := range globalClosedDynamic {
		if previous, exists := closedDynamic[call]; exists && !sameCoroClosedDynamicCallCertificate(previous, certificate) {
			return nil, nil, nil, nil, fmt.Errorf("closed dynamic call in %q has conflicting TLS-field and global-slot proofs", call.Parent().Name())
		}
		closedDynamic[call] = cloneCoroClosedDynamicCallCertificate(certificate)
	}
	ctx.coroGlobalFunctionSlots = globalFunctionSlots
	// runtimeLinkRequirements makes the real LLGo runtime package init an
	// entry-module call for every executable. That edge is
	// compiler-generated LLVM IR and therefore invisible to the source SSA call
	// graph, so keep it as an explicit managed root.
	names := []string{"init"}
	demandByName := map[string]coro.Demand{"init": coro.AsyncDemand}
	plainRootByName := map[string]bool{"init": false}
	names = append(names,
		coroFrameAllocatorBootstrapSymbolV1,
		coroProgramBeginSymbolV1,
	)
	if nativeCoroDoorbellRuntimeABI(ctx.buildConf) || hostCoroPullRuntimeABI(ctx.buildConf) {
		names = append(names, coroProgramRunSliceSymbolV2, coroProgramContinueSliceSymbolV2)
	} else {
		names = append(names, coroProgramRunSymbolV1, coroProgramContinueSymbolV1)
	}
	if ctx.buildConf.coroWorkerSupported() {
		names = append(names,
			coroWorkerParkSymbolV1,
			coroWorkerResumeSymbolV1,
			coroOSThreadLockedSymbolV1,
			coroOSThreadForeignCallSymbolV1,
		)
		if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
			// The fixed native C worker owns both blocking leaves and crosses
			// back into Go only through this POD completion callback. Unlike the
			// old Go worker loop, this exact root has no WaitForeign edge and must
			// remain one synchronous plain body rather than acquiring a dead
			// managed coroutine twin.
			names = append(names, coroNativeWorkerCompleteSymbolV1)
		}
	}
	if nativeCoroFleetRuntimeABI(ctx.buildConf) {
		// A fixed C pthread routine enters this exact Go body from a raw native
		// stack. Its static closure owns the ordinary-domain reducer, bounded
		// reactor wait, and LLVM resume/destroy wrappers; it must never acquire a
		// managed entry or an independently suspended coroutine twin.
		names = append(names, coroNativeFleetOwnerSymbolV1)
	}
	if hostCoroPullRuntimeABI(ctx.buildConf) {
		names = append(names,
			coroHostNextActionSymbolV1,
			coroHostProfileSymbolV1,
			coroHostNextDeadlineSymbolV1,
			coroHostPublishTimeSymbolV1,
			coroHostAckCancelSymbolV1,
			coroHostContinueSliceSymbolV1,
		)
	}
	if nativeCoroTimerRuntimeABI(ctx.buildConf) {
		names = append(names,
			coroTimerParkSymbolV2,
			coroTimerParkControlledSymbolV2,
			coroTimerResumeSymbolV2,
			coroTimerRequestControlledSymbolV2,
			coroPollParkSymbolV2,
			coroPollResumeSymbolV2,
			coroPollUpdateDeadlineOrAbortSymbolV1,
			coroPollPostClosingOrAbortSymbolV1,
			coroKeyedParkSymbolV2,
			coroKeyedResumeSymbolV2,
			coroSemaphorePrepareOrAbortSymbolV2,
			coroSemaphoreReleaseOrAbortSymbolV2,
			coroNotifyPrepareOrAbortSymbolV2,
			coroNotifyOneOrAbortSymbolV2,
			coroNotifyAllOrAbortSymbolV2,
		)
	}
	names = append(names,
		"__llgo_coro_frame_alloc_v1",
		"__llgo_coro_frame_publish_v1",
		"__llgo_coro_await_prepare_v1",
		"__llgo_coro_preempt_poll_v1",
		"__llgo_coro_yield_prepare_v1",
		coroRunDecisionTakeSymbolV1,
		coroRunDecisionTakeZeroSymbolV1,
		"__llgo_coro_complete_prepare_v1",
		"__llgo_coro_frame_free_v1",
		"__llgo_coro_await_prepare_v3",
		"__llgo_coro_await_consume_v1",
		"__llgo_coro_complete_prepare_v2",
		"__llgo_coro_critical_enter_v1",
		"__llgo_coro_critical_exit_v1",
		coroOSThreadLockSymbolV1,
		coroOSThreadUnlockSymbolV1,
	)
	names = append(names,
		// These typed Go helpers are compiler calls, not ordinary source
		// calls. Try/park/resume must finish on the current executor stack;
		// in particular park runs after state publication and immediately
		// before llvm.coro.suspend, while resume runs inside its terminating
		// resume gate. Keep their complete exact closure in the same required
		// plain runtime island as the raw channel hooks below.
		"CoroChanTrySend",
		"CoroChanTryRecv",
		"CoroChanTryClose",
		"CoroChanSelectTry",
		"CoroChanSelectPark",
		"CoroChanSelectResume",
		coroChanSendParkSymbolV1,
		coroChanRecvParkSymbolV1,
		coroChanResumeSymbolV1,
		"__llgo_coro_fault_prepare_v1",
		"__llgo_coro_fault_prepare_v2",
	)
	names = append(names,
		"__llgo_coro_panic_prepare_v1",
		"__llgo_coro_recover_take_v1",
		"__llgo_coro_fault_payload_v1",
		"__llgo_coro_fault_payload_v2",
		"__llgo_coro_spawn_begin_v1",
		"__llgo_coro_spawn_commit_v1",
		coroProgramMainReturnSymbolV1,
	)
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
		if handled, err := validateCoroHostPullRuntimeFunctionV1(name, fn); handled && err != nil {
			return nil, nil, nil, nil, err
		}
		if name == "__llgo_coro_complete_prepare_v2" {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 4 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(3).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine terminal completion ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32) signature", name)
			}
		}
		if name == "__llgo_coro_fault_payload_v1" {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 3 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine fault payload ABI %q must have exact func(uint32, unsafe.Pointer, unsafe.Pointer) signature", name)
			}
		}
		if name == "__llgo_coro_fault_prepare_v2" {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 6 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(3).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(4).Type(), types.Typ[types.Uintptr]) ||
				!types.Identical(sig.Params().At(5).Type(), types.Typ[types.Uintptr]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("parameterized coroutine fault prepare ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32, uintptr, uintptr) signature", name)
			}
		}
		if name == "__llgo_coro_fault_payload_v2" {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 5 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uintptr]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.Uintptr]) ||
				!types.Identical(sig.Params().At(3).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(4).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("parameterized coroutine fault payload ABI %q must have exact func(uint32, uintptr, uintptr, unsafe.Pointer, unsafe.Pointer) signature", name)
			}
		}
		if name == "__llgo_coro_critical_enter_v1" || name == "__llgo_coro_critical_exit_v1" {
			sig := fn.Signature
			wantResult := name == "__llgo_coro_critical_exit_v1"
			validResults := sig != nil && sig.Results().Len() == 0
			if wantResult {
				validResults = sig != nil && sig.Results().Len() == 1 &&
					types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool])
			}
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) || !validResults ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				result := ""
				if wantResult {
					result = " bool"
				}
				return nil, nil, nil, nil, fmt.Errorf("coroutine critical runtime ABI %q must have exact func(unsafe.Pointer)%s signature", name, result)
			}
		}
		if name == coroOSThreadLockSymbolV1 || name == coroOSThreadUnlockSymbolV1 ||
			name == coroOSThreadLockedSymbolV1 {
			sig := fn.Signature
			wantResult := name == coroOSThreadLockedSymbolV1
			validResults := sig != nil && sig.Results().Len() == 0
			if wantResult {
				validResults = sig != nil && sig.Results().Len() == 1 &&
					types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool])
			}
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) || !validResults ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				result := ""
				if wantResult {
					result = " bool"
				}
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine OS-thread runtime ABI %q must have exact func(unsafe.Pointer)%s signature",
					name, result,
				)
			}
		}
		if name == coroOSThreadForeignCallSymbolV1 {
			sig := fn.Signature
			valid := sig != nil && sig.Recv() == nil && !sig.Variadic() &&
				sig.Params().Len() == 15 && sig.Results().Len() == 0 &&
				types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) &&
				types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uintptr]) &&
				types.Identical(sig.Params().At(2).Type(), types.Typ[types.Uint32])
			for index := 3; valid && index < 12; index++ {
				valid = types.Identical(sig.Params().At(index).Type(), types.Typ[types.Uintptr])
			}
			wordPointer := types.NewPointer(types.Typ[types.Uintptr])
			for index := 12; valid && index < 15; index++ {
				valid = types.Identical(sig.Params().At(index).Type(), wordPointer)
			}
			if !valid || typeParamLen(sig.TypeParams()) != 0 ||
				typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine locked-thread foreign ABI %q must have exact func(unsafe.Pointer, uintptr, uint32, [9]uintptr, *uintptr, *uintptr, *uintptr) signature",
					name,
				)
			}
		}
		if name == coroProgramContinueSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) || sig.Results().Len() != 0 ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine program bootstrap runtime ABI %q must have exact func(uint32) signature", name)
			}
		}
		if name == coroProgramRunSliceSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 4 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.Uint32]) ||
				!validCoroProgramRunResultPointerV2(sig.Params().At(3).Type()) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine program run-slice ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, uint32, *{8 x uint32}) uint32 signature", name)
			}
		}
		if name == coroProgramContinueSliceSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 5 || sig.Results().Len() != 1 ||
				!validCoroProgramRunResultPointerV2(sig.Params().At(4).Type()) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine program continue-slice ABI %q must have exact func(uint32, uint32, uint32, uint32, *{8 x uint32}) uint32 signature", name)
			}
			for parameter := 0; parameter != 4; parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.Uint32]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine program continue-slice ABI %q must have exact func(uint32, uint32, uint32, uint32, *{8 x uint32}) uint32 signature", name)
				}
			}
		}
		if name == coroNativeWorkerCompleteSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 5 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine native worker completion %q must have exact func(uint32, uint32, uintptr, uintptr, uintptr) uint32 signature", name)
			}
			for parameter := 2; parameter < sig.Params().Len(); parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.Uintptr]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine native worker completion %q must have exact func(uint32, uint32, uintptr, uintptr, uintptr) uint32 signature", name)
				}
			}
		}
		if name == coroNativeFleetOwnerSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 0 ||
				sig.Results().Len() != 1 ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine native fleet owner %q must have exact func() uint32 signature", name)
			}
		}
		if name == coroTimerParkSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 5 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(3).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(4).Type(), types.Typ[types.Int64]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine timer park V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, int64) signature", name)
			}
		}
		if name == coroTimerParkControlledSymbolV2 {
			sig := fn.Signature
			uint32Pointer := types.NewPointer(types.Typ[types.Uint32])
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 9 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(3).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(4).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(5).Type(), uint32Pointer) ||
				!types.Identical(sig.Params().At(6).Type(), uint32Pointer) ||
				!types.Identical(sig.Params().At(7).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(8).Type(), types.Typ[types.Int64]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("controlled coroutine timer park V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, *uint32, *uint32, uint32, int64) signature", name)
			}
		}
		if name == coroTimerResumeSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 2 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine timer resume V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer) uint32 signature", name)
			}
		}
		if name == coroTimerRequestControlledSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 1 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("controlled coroutine timer request V2 ABI %q must have exact func(uint32) uint32 signature", name)
			}
		}
		if name == coroPollParkSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 8 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(3).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(4).Type(), types.Typ[types.Uintptr]) ||
				!types.Identical(sig.Params().At(5).Type(), types.Typ[types.Int32]) ||
				!types.Identical(sig.Params().At(6).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(7).Type(), types.Typ[types.Int64]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine poll park V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr, int32, uint32, int64) signature", name)
			}
		}
		if name == coroPollResumeSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 2 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine poll resume V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer) uint32 signature", name)
			}
		}
		if name == coroPollUpdateDeadlineOrAbortSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 3 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uintptr]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.Int64]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine poll update-deadline-or-abort ABI %q must have exact func(uintptr, uint32, int64) signature", name)
			}
		}
		if name == coroPollPostClosingOrAbortSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 2 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uintptr]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine poll post-closing-or-abort ABI %q must have exact func(uintptr, uint32) signature", name)
			}
		}
		if name == coroKeyedParkSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 4 || sig.Results().Len() != 0 ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine keyed park V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) signature", name)
			}
			for parameter := 0; parameter < sig.Params().Len(); parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.UnsafePointer]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine keyed park V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) signature", name)
				}
			}
		}
		if name == coroKeyedResumeSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 2 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine keyed resume V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer) uint32 signature", name)
			}
		}
		if name == coroSemaphorePrepareOrAbortSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 2 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine semaphore prepare V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer) signature", name)
			}
		}
		if name == coroSemaphoreReleaseOrAbortSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 1 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine semaphore release V2 ABI %q must have exact func(unsafe.Pointer) signature", name)
			}
		}
		if name == coroNotifyPrepareOrAbortSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 3 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine notify prepare V2 ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, uint32) signature", name)
			}
		}
		if name == coroNotifyOneOrAbortSymbolV2 || name == coroNotifyAllOrAbortSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 2 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine notify publication V2 ABI %q must have exact func(unsafe.Pointer, uint32) signature", name)
			}
		}
		if name == coroRunDecisionTakeSymbolV1 {
			sig := fn.Signature
			uint32Pointer := types.NewPointer(types.Typ[types.Uint32])
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 8 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine run-decision ABI %q must have exact func(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) signature", name)
			}
			for parameter := 1; parameter < 3; parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.Uint32]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine run-decision ABI %q must have exact func(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) signature", name)
				}
			}
			for parameter := 3; parameter < sig.Params().Len(); parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), uint32Pointer) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine run-decision ABI %q must have exact func(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) signature", name)
				}
			}
		}
		if name == coroChanSendParkSymbolV1 || name == coroChanRecvParkSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 7 || sig.Results().Len() != 0 ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine channel park ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr) signature", name)
			}
			for parameter := 0; parameter < 6; parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.UnsafePointer]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine channel park ABI %q must use unsafe.Pointer for parameter %d", name, parameter)
				}
			}
			if !types.Identical(sig.Params().At(6).Type(), types.Typ[types.Uintptr]) {
				return nil, nil, nil, nil, fmt.Errorf("coroutine channel park ABI %q must use uintptr element size", name)
			}
		}
		if name == coroChanResumeSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 2 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine channel resume ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer) uint32 signature", name)
			}
		}
		if name == coroRunDecisionTakeZeroSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 1 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine zero-ticket run-decision ABI %q must have exact func(unsafe.Pointer) uint32 signature", name)
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
						// raw host/scheduler-stack island. Fixed-point analysis must prove the
						// target NoSuspend/!NeedsPreempt without suppressing either.
						continue
					}
					continue
				}
				callee, ok := ctx.coroEmission.Resolve(raw)
				if !ok || callee == nil {
					continue
				}
				callSite, found, err := ctx.coroEmission.CoroCallSitePlan(call)
				if err != nil {
					return nil, nil, nil, nil, fmt.Errorf("classify compiler runtime ABI intrinsic %q in %q: %w", callee.Name(), fn.Name(), err)
				}
				if found && callSite.ElidesCall() {
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
	// A function address handed to a raw C ABI is an externally callable entry,
	// even when the ordinary Go reference graph cannot retain that exact generic
	// instantiation.  The frozen direct-plain proof above is the authority for
	// both its synchronous ABI and its liveness; make that liveness explicit
	// instead of relying on a descriptor/reference edge that the raw lowering
	// deliberately replaces.
	roots = appendCoroDirectPlainRoots(roots, directPlain)
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

// requiredCoroDirectPlainCallArguments freezes source-level C callback ABI
// boundaries across the whole emission universe. A callback passed to a
// //llgo:type C function parameter is a raw synchronous entry, never a Go
// descriptor. The exact closed target closure is independently revalidated
// after fixed-point planning as NoSuspend/DirectPlain.
func requiredCoroDirectPlainCallArguments(
	ctx *context,
	closedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate,
) ([]requiredCoroDirectPlainCallArgument, map[*ssa.Function]struct{}, error) {
	if ctx == nil || ctx.coroEmission == nil || ctx.coroSSAEmission == nil {
		return nil, nil, nil
	}
	var uses []requiredCoroDirectPlainCallArgument
	plain := make(map[*ssa.Function]struct{})
	for _, function := range ctx.coroSSAEmission.Functions() {
		if function == nil || len(function.Blocks) == 0 {
			continue
		}
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil || call.Common().StaticCallee() == nil {
					continue
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
					closure, proved, err := provenCoroDirectPlainStaticClosure(ctx, target, closedDynamic)
					if err != nil {
						return nil, nil, fmt.Errorf("prove direct-plain callback target %q in %q: %w", target.Name(), function.Name(), err)
					}
					if !proved {
						continue
					}
					uses = append(uses, requiredCoroDirectPlainCallArgument{call: call, argument: argument, target: target})
					for _, member := range closure {
						plain[member] = struct{}{}
					}
				}
			}
		}
	}
	return uses, plain, nil
}

func appendUniqueCoroDirectPlainCallArguments(
	destination []requiredCoroDirectPlainCallArgument,
	values ...requiredCoroDirectPlainCallArgument,
) []requiredCoroDirectPlainCallArgument {
	seen := make(map[coroCallArgumentKey]struct{}, len(destination)+len(values))
	for _, value := range destination {
		seen[coroCallArgumentKey{call: value.call, argument: value.argument}] = struct{}{}
	}
	for _, value := range values {
		key := coroCallArgumentKey{call: value.call, argument: value.argument}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		destination = append(destination, value)
	}
	return destination
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
// are direct, statically resolved emitted bodies (or builtins). An ordinary
// indirect call through an exact //llgo:type C value is a raw code-pointer leaf:
// it stays on the foreign/native stack and never becomes a managed descriptor
// edge. An exact frozen C declaration may terminate the closure when it carries
// a frontend-owned noblock/sync certificate. The declaration then enters
// requiredPlain and is classified through the same frozen
// IgnoreBody/ExternalKnown path as the compiler runtime ABI. The compiler-owned
// TLS callback retains its separately frozen field-flow exception for its exact
// declaration leaves. Dynamic managed calls, go/defer, other bodyless leaves,
// captured closures, and unresolved aliases remain on the ordinary Dispatch
// path. Effect and representation are independently checked after fixed-point
// analysis; this prefilter only establishes that it is sound to seed the
// candidate's exact raw host/scheduler-stack island.
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
				callSite, found, err := ctx.coroEmission.CoroCallSitePlan(call)
				if err != nil {
					return nil, false, fmt.Errorf("classify direct-plain callback intrinsic %q in %q: %w", call.String(), function.Name(), err)
				}
				if found && callSite.ElidesCall() {
					// The frontend emits the exact intrinsic operation inline;
					// its declaration/fallback SSA body is not a callable edge.
					// Post-plan raw feasibility still validates the operation's
					// source-attributed effect and physical lowering contract.
					continue
				}
				raw := call.Common().StaticCallee()
				if raw == nil {
					if !call.Common().IsInvoke() && call.Common().Method == nil &&
						ctx.prog.TypeBackground(call.Common().Value.Type()) == llssa.InC {
						// The callable address is already a one-word C value. Its
						// behavior remains conservatively foreign in the fixed-point
						// plan; this prefilter establishes only that no managed
						// descriptor call is hidden in the raw callback body.
						continue
					}
					if certificate, certified := closedDynamic[call]; certified && certificate.SyncDispatch && !call.Common().IsInvoke() {
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
					if !classified || background != llssa.InC {
						return nil, false, nil
					}
					_, noBlock, err := ctx.coroEmission.CoroForeignNoBlockCertificate(callee)
					if err != nil {
						return nil, false, err
					}
					_, synchronous, err := ctx.coroEmission.CoroForeignSyncCertificate(callee)
					if err != nil {
						return nil, false, err
					}
					if !tlsCallback && !noBlock && !synchronous {
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
	for call, certificate := range closedDynamic {
		if !certificate.SyncDispatch {
			continue
		}
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
		CoroABI:             activeCoroABIVersion(ctx.buildConf),
		SchedulerABI:        activeCoroSchedulerABIVersion(ctx.buildConf),
		PanicABI:            activeCoroPanicABIVersion(ctx.buildConf),
		FuncRepABI:          activeCoroFuncRepABIVersion(ctx.buildConf),
		LoweringFactsSchema: ctx.coroLoweringFacts.Schema,
		LoweringFactsDigest: ctx.coroLoweringFactsDigest,
		TargetTriple:        target.Triple,
		TargetCPU:           target.CPU,
		TargetFeatures:      target.Features,
		TargetABI:           target.TargetABI,
		PointerBits:         ctx.prog.PointerSize() * 8,
		Endianness:          endianness,
		DataLayout:          ctx.prog.DataLayout(),
	}, nil
}

func prepareCoroEmissionUniverse(ctx *context, packages []*aPackage) error {
	if ctx != nil && ctx.coroRawGlobalSymbols == nil {
		inventory, err := freezeCoroRawGlobalSymbolInventory(ctx, packages)
		if err != nil {
			return fmt.Errorf("freeze coroutine raw data-symbol inventory: %w", err)
		}
		ctx.coroRawGlobalSymbols = inventory
	}
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
		assemblyProofMap, err := plan9asmNoSuspendProofsForPkg(ctx, aPkg.PkgPath)
		if err != nil {
			return fmt.Errorf("freeze coroutine assembly proofs for %q: %w", aPkg.PkgPath, err)
		}
		assemblyProofSymbols := make([]string, 0, len(assemblyProofMap))
		for symbol := range assemblyProofMap {
			assemblyProofSymbols = append(assemblyProofSymbols, symbol)
		}
		slices.Sort(assemblyProofSymbols)
		assemblyProofs := make([]cl.CoroAssemblyNoSuspendProof, 0, len(assemblyProofSymbols))
		for _, symbol := range assemblyProofSymbols {
			proof := assemblyProofMap[symbol]
			assemblyProofs = append(assemblyProofs, cl.CoroAssemblyNoSuspendProof{
				PhysicalSymbol: proof.Symbol,
				ABISignature:   proof.Signature,
				CallClosure:    append([]string(nil), proof.CallClosure...),
				ClosureSHA256:  proof.ClosureSHA256,
			})
		}
		identity := coroRawPackageIdentity(aPkg)
		inputs = append(inputs, cl.EmissionPackage{
			SSA:                     aPkg.SSA,
			Files:                   files,
			Identity:                identity,
			MetadataOnly:            metadataOnly,
			AssemblyNoSuspendProofs: assemblyProofs,
			RawDataSymbols:          ctx.coroRawGlobalSymbols.emissionProfile(identity),
		})
		hasRuntimeABI = hasRuntimeABI || aPkg.PkgPath == llssa.PkgRuntime
	}
	emission, err := cl.PrepareEmissionUniverseWithOptions(ctx.prog, ctx.patches, inputs, cl.EmissionUniverseOptions{
		// Archive-producing entry resolution with the real runtime input must
		// freeze every hidden compiler/runtime ABI edge. Isolated plan tests may
		// deliberately prepare an incomplete package universe.
		CompleteRuntimeABI:     hasRuntimeABI,
		CoroTargetCapabilities: ctx.buildConf.coroTargetCapabilities(),
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

// cHeaderPackages excludes the patched standard runtime implementation. Its
// //export callbacks are linker implementation details and may use internal C
// types that are deliberately not representable in a public generated header.
func cHeaderPackages(allPkgs []*aPackage) []llssa.Package {
	pkgs := make([]llssa.Package, 0, len(allPkgs))
	for _, pkg := range allPkgs {
		if pkg == nil || pkg.LPkg == nil || pkg.Package == nil || pkg.PkgPath == "runtime" || isRuntimePkg(pkg.PkgPath) || !hasLocalCExports(pkg.LPkg) {
			continue
		}
		pkgs = append(pkgs, pkg.LPkg)
	}
	return pkgs
}

func hasLocalCExports(pkg llssa.Package) bool {
	if pkg == nil {
		return false
	}
	for name := range pkg.ExportFuncs() {
		if !strings.Contains(name, ".") || strings.HasPrefix(name, pkg.Path()+".") {
			return true
		}
	}
	return false
}

// applyBuildModeCompileFlags adds code-generation flags that must be present
// while package C/C++ sources are compiled. Passing -fPIC only to the final
// shared-library link is too late for objects containing global references.
func applyBuildModeCompileFlags(mode BuildMode, export *crosscompile.Export) {
	if mode == BuildModeCShared && export != nil && !slices.Contains(export.CCFLAGS, "-fPIC") {
		export.CCFLAGS = append(export.CCFLAGS, "-fPIC")
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

	// coroPlan is the compilation-scoped authority for the sole stackless
	// execution architecture.
	coroPlan        *coro.SSAPlan
	coroEmission    *cl.EmissionUniverse
	coroSSAEmission *coro.SSAEmissionUniverse
	// coroRawGlobalSymbols freezes package-owned non-Go linker inputs before
	// whole-program analysis. Each global function-cell certificate consumes
	// only the profile for its exact stable emission-package identity.
	coroRawGlobalSymbols *coroRawGlobalSymbolInventory
	// coroGlobalFunctionSlots retains the conditional writer/escape inventory
	// paired with build-owned closed dynamic call certificates until final-plan
	// validation proves every omitted edge physically EmitNone.
	coroGlobalFunctionSlots map[ssa.CallInstruction]coroGlobalFunctionSlotProof
	// coroTLSDestructorFixturePkg is an internal test-only identity override.
	// Production builds leave it empty and accept only runtime/internal/clite/tls.
	coroTLSDestructorFixturePkg string
	coroPlanDigest              string
	coroPlanMetadata            coro.PlanDigestMetadata
	coroLoweringFacts           coro.LoweringFacts
	coroLoweringFactsDigest     string
	// Frozen immediately after whole-program analysis, before package codegen.
	// linkMainPkg only consumes these exact per-entry-package tables.
	coroProgramBootstraps map[string]*coroProgramBootstrapV1

	// clCompilation is shared by all source packages in this build. Active
	// cache registration is enabled only after coroPlanDigest and its complete
	// ABI/target record have been frozen into archive fingerprints.
	clCompilation *cl.Compilation

	// pclnExternal is populated while generating the synthetic main module
	// and completed with final linked PCs by the post-link externalizer.
	pclnExternal *pclnmap.Data
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
	return true
}

// runtimeLinkRequirements reflects the single stackless architecture: every
// linked program initializes and links the scheduler runtime.
func runtimeLinkRequirements(conf *Config, needRuntime, needPyInit bool) (initRuntime, linkRuntime bool) {
	return true, true
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
	ctx.pclnExternal = nil
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
	if err := validateCoroHostPullEntryConfig(ctx.buildConf, needPyInit); err != nil {
		return err
	}

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
	var funcInfo []funcInfoRecord
	var pcLineInfo []pcLineRecord
	var funcInfoStubs []funcInfoStubRecord
	if ctx.buildConf.PCLNMode != PCLNNone {
		funcInfo = prepareFuncInfoTableRecords(collectFuncInfo(linkedOrder), nil)
		pcLineInfo = collectPCLineInfo(linkedOrder)
		funcInfoStubs = collectFuncInfoStubRecords(linkedOrder, funcInfo)
	}
	var coroRootAnchors []string
	var coroManifestHash [16]byte
	var coroBootstrap *coroProgramBootstrapV1
	var err error
	coroRootAnchors, err = collectLinkedCoroRootAnchors(linkedOrder)
	if err != nil {
		return err
	}
	if ctx.buildConf.BuildMode == BuildModeExe {
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
	linkArgs = append(linkArgs, cSharedExportArgs(ctx, linkedOrder)...)

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
		return ctx.createMergedArchiveFile(app, objFiles, printCmds)
	}

	buildArgs := []string{"-o", app}
	buildArgs = append(buildArgs, linkArgs...)
	buildArgs = append(buildArgs, dwarfLinkerArgs(ctx.buildConf, &ctx.crossCompile)...)
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

	if shouldEmitDebugInfo(ctx.buildConf, &ctx.crossCompile) {
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

// cSharedExportArgs keeps //export functions as shared-library link roots. The
// functions live in package archives and otherwise remain unreferenced, so the
// linker can omit both their object files and dynamic symbols.
func cSharedExportArgs(ctx *context, pkgs []*aPackage) []string {
	if ctx == nil || ctx.buildConf == nil || ctx.buildConf.BuildMode != BuildModeCShared {
		return nil
	}
	exports := make(map[string]none)
	for _, pkg := range pkgs {
		if pkg == nil || pkg.LPkg == nil {
			continue
		}
		for _, name := range pkg.LPkg.ExportFuncs() {
			if name != "" {
				exports[name] = none{}
			}
		}
	}
	names := make([]string, 0, len(exports))
	for name := range exports {
		names = append(names, name)
	}
	slices.Sort(names)
	args := make([]string, 0, len(names))
	for _, name := range names {
		if ctx.buildConf.Goos == "darwin" {
			args = append(args, "-Wl,-u,_"+name)
		} else {
			args = append(args, "-Wl,--undefined="+name)
		}
	}
	return args
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
	return ctx.buildConf.Target == "" && ctx.buildConf.Goos == "linux" && effectivePCLNMode(ctx.buildConf) != PCLNNone
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

// archiveMerger returns an archiver with MRI support, which is required to
// flatten package archives into the final c-archive instead of nesting .a
// files as members. LLVM is already a required LLGo toolchain dependency.
func (c *context) archiveMerger() (string, error) {
	if ar := os.Getenv("LLGO_AR"); ar != "" {
		return ar, nil
	}
	if c.crossCompile.CC != "" {
		llvmAr := filepath.Join(filepath.Dir(c.crossCompile.CC), "llvm-ar")
		if _, err := os.Stat(llvmAr); err == nil {
			return llvmAr, nil
		}
	}
	if llvmAr, err := exec.LookPath("llvm-ar"); err == nil {
		return llvmAr, nil
	}
	return "", errors.New("llvm-ar is required to create a flat c-archive")
}

// createMergedArchiveFile combines object files and package archives into one
// flat archive. A regular `ar rcs output.a input.a` stores input.a as a nested
// member, which C linkers cannot search or load.
func (c *context) createMergedArchiveFile(archivePath string, inputs []string, verbose ...bool) error {
	if len(inputs) == 0 {
		return fmt.Errorf("no inputs provided for archive %s", archivePath)
	}
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(archivePath), filepath.Base(archivePath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	_ = os.Remove(tmpName)

	var script strings.Builder
	fmt.Fprintf(&script, "CREATE %s\n", strconv.Quote(tmpName))
	for _, input := range inputs {
		command := "ADDMOD"
		if strings.HasSuffix(strings.ToLower(input), ".a") {
			command = "ADDLIB"
		}
		fmt.Fprintf(&script, "%s %s\n", command, strconv.Quote(input))
	}
	script.WriteString("SAVE\nEND\n")

	arCmd, err := c.archiveMerger()
	if err != nil {
		return err
	}
	cmd := exec.Command(arCmd, "-M")
	cmd.Stdin = strings.NewReader(script.String())
	printCmds := c.shouldPrintCommands(len(verbose) > 0 && verbose[0])
	if printCmds {
		fmt.Fprintf(os.Stderr, "%s -M\n%s", filepath.Base(arCmd), script.String())
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("merge archive %s: %w\n%s", archivePath, err, output)
	}
	if err := os.Rename(tmpName, archivePath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("publish archive %s: %w", archivePath, err)
	}
	return nil
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
	} else {
		printCompiledPackage(ctx.buildConf, aPkg)
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

	// The C ABI transformer assumes every call's operand count and physical
	// function type already agree. Verify the frontend module before handing it
	// to LLVM's unchecked C mutation APIs; otherwise one malformed call can turn
	// an out-of-range LLVMGetOperand into a host-process segmentation fault.
	if err = gllvm.VerifyModule(ret.Module(), gllvm.ReturnStatusAction); err != nil {
		var broken []string
		var firstBrokenIR string
		for fn := ret.Module().FirstFunction(); !fn.IsNil(); fn = gllvm.NextFunction(fn) {
			if gllvm.VerifyFunction(fn, gllvm.ReturnStatusAction) != nil {
				broken = append(broken, fn.Name())
				if firstBrokenIR == "" {
					firstBrokenIR = fn.String()
					const diagnosticLimit = 64 << 10
					if len(firstBrokenIR) > diagnosticLimit {
						firstBrokenIR = firstBrokenIR[:diagnosticLimit] + "\n; ... truncated ..."
					}
				}
			}
		}
		return fmt.Errorf("verify frontend LLVM module for package %s before C ABI transform (invalid functions %v): %w\nfirst invalid function:\n%s", pkgPath, broken, err, firstBrokenIR)
	}

	ctx.cTransformer.SetSkipFuncs(cabiSkipFuncsForPlan9Asm(ctx, pkgPath, ret.Module()))
	llabi.LowerLargeAggregates(ctx.prog.TargetData(), ret.Module())
	ctx.cTransformer.TransformModule(ret.Path(), ret.Module())
	ctx.cTransformer.SetSkipFuncs(nil)

	mod := ret.Module()
	mod.SetDataLayout(ctx.prog.DataLayout())
	mod.SetTarget(ctx.prog.TargetSpec().Triple)
	// Coroutine splitting is a mandatory correctness pass, not an optimization.
	// In particular, native debug builds intentionally skip the default
	// optimization pipeline, but TargetMachine cannot select unresolved
	// llvm.coro.* operators. ModeGen deliberately retains frontend coroutine IR
	// for golden/LIT inspection and never reaches object emission here.
	if ctx.mode != ModeGen {
		if err := lowerCoroPackageModule(ctx, pkgPath, mod); err != nil {
			return err
		}
	}

	// Run the default LLVM optimization pipeline selected by the requested -O level.
	if ctx.passOpt {
		pbo := gllvm.NewPassBuilderOptions()
		defer pbo.Dispose()
		if err = gllvm.VerifyModule(mod, gllvm.ReturnStatusAction); err != nil {
			var broken []string
			for fn := mod.FirstFunction(); !fn.IsNil(); fn = gllvm.NextFunction(fn) {
				if gllvm.VerifyFunction(fn, gllvm.ReturnStatusAction) != nil {
					broken = append(broken, fn.Name())
				}
			}
			return fmt.Errorf("verify LLVM module for package %s (invalid functions %v): %w", pkgPath, broken, err)
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
	// ModeGen transfers the live frontend module to its caller for IR/LIT
	// inspection. It neither links nor consumes a package archive, and emitting
	// that presplit module as native code would hand llvm.coro.* operators to
	// instruction selection. Executable/object-producing modes cross the
	// mandatory lowering boundary above and retain the normal archive path.
	if pkg.ExportFile != "" && ctx.mode != ModeGen {
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

const coroPackageLoweringPipeline = "coro-early,cgscc(coro-split),coro-cleanup"

func lowerCoroPackageModule(ctx *context, pkgPath string, mod gllvm.Module) error {
	if ctx == nil || ctx.prog == nil || mod.IsNil() {
		return fmt.Errorf("lower package coroutine IR for %s: missing build context or module", pkgPath)
	}
	if err := gllvm.VerifyModule(mod, gllvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("verify package %s before coroutine lowering: %w", pkgPath, err)
	}
	if err := validateCoroPackageStaticAllocas(pkgPath, mod); err != nil {
		return err
	}
	options := gllvm.NewPassBuilderOptions()
	defer options.Dispose()
	if err := mod.RunPasses(coroPackageLoweringPipeline, ctx.prog.TargetMachine(), options); err != nil {
		return fmt.Errorf("lower package coroutine IR for %s: %w", pkgPath, err)
	}
	llssa.RemoveKeepAliveCallsAfterCoroSplit(mod)
	if err := gllvm.VerifyModule(mod, gllvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("verify package %s after coroutine lowering: %w", pkgPath, err)
	}
	for function := mod.FirstFunction(); !function.IsNil(); function = gllvm.NextFunction(function) {
		if strings.HasPrefix(function.Name(), "llvm.coro.") && !function.FirstUse().IsNil() {
			return fmt.Errorf("lower package coroutine IR for %s: intrinsic %q still has live uses after %s", pkgPath, function.Name(), coroPackageLoweringPipeline)
		}
	}
	return nil
}

// CoroSplit cannot lower a variable-sized alloca that lives in a presplit
// coroutine. Diagnose the exact producer before entering LLVM: the pass treats
// this as a fatal backend error and would otherwise abort the entire compiler
// process. Plain functions may continue to use dynamic native-stack allocas.
func validateCoroPackageStaticAllocas(pkgPath string, mod gllvm.Module) error {
	for function := mod.FirstFunction(); !function.IsNil(); function = gllvm.NextFunction(function) {
		coroutine := false
		var dynamic []string
		for block := function.FirstBasicBlock(); !block.IsNil(); block = gllvm.NextBasicBlock(block) {
			for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = gllvm.NextInstruction(instruction) {
				if instruction.InstructionOpcode() == gllvm.Call && instruction.CalledValue().Name() == "llvm.coro.id" {
					coroutine = true
				}
				if instruction.InstructionOpcode() == gllvm.Alloca &&
					(instruction.OperandsCount() == 0 || instruction.Operand(0).IsAConstantInt().IsNil()) {
					dynamic = append(dynamic, strings.TrimSpace(instruction.String()))
				}
			}
		}
		if coroutine && len(dynamic) != 0 {
			return fmt.Errorf(
				"lower package coroutine IR for %s: presplit function %q contains variable-sized alloca unsupported by CoroSplit: %s",
				pkgPath, function.Name(), strings.Join(dynamic, "; "),
			)
		}
	}
	return nil
}

func printCompiledPackage(conf *Config, pkg *aPackage) {
	if conf.PrintPackages && !pkg.CacheHit {
		fmt.Fprintln(os.Stderr, pkg.PkgPath)
	}
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
	// Building an alternate package also visits its ordinary dependencies.
	// Some of those dependencies instantiate generic functions from a package
	// that the alternate tree replaces (for example os instantiates
	// runtime.AddCleanup). Freeze every alternate package first so the complete
	// patch map exists, then rewrite all dependency TypesInfo before any SSA body
	// is built. A one-pass CreatePackage+Build sequence permanently captures the
	// original generic origin and cannot be repaired by createSSAPkg later,
	// because that function observes the already-imported SSA package.
	var collected []*packages.Package
	packages.Visit(alts, nil, func(p *packages.Package) {
		if p.Types != nil && !p.IllTyped {
			collected = append(collected, p)
		}
	})

	created := make(map[*packages.Package]none, len(collected))
	for _, p := range collected {
		if !strings.HasPrefix(p.ID, altPkgPathPrefix) {
			continue
		}
		path := p.ID[len(altPkgPathPrefix):]
		// Even if an alt package exists and is pulled in as a dependency of other
		// patches (e.g. runtime/reflect), only enable it for the selected target.
		if !hasAltPkgForTarget(conf, path) {
			continue
		}
		if debugBuild || verbose {
			log.Println("==> CreateSSA", p.ID)
		}
		pkgSSA := prog.CreatePackage(p.Types, p.Syntax, p.TypesInfo, true)
		created[p] = none{}
		patches[path] = cl.Patch{Alt: pkgSSA, Types: typepatch.Clone(p.Types)}
		if debugBuild || verbose {
			log.Println("==> Patching", path)
		}
	}

	patchContext := &context{patches: patches}
	for _, p := range collected {
		applyPatches(patchContext, p, verbose)
		if _, ok := created[p]; ok {
			continue
		}
		if debugBuild || verbose {
			log.Println("==> CreateSSA", p.ID)
		}
		prog.CreatePackage(p.Types, p.Syntax, p.TypesInfo, true)
		created[p] = none{}
	}

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
	if ctx.buildConf.GenLL && ext != ".S" {
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
