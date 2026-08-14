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
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
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
	"github.com/goplus/llgo/internal/dcepass"
	"github.com/goplus/llgo/internal/deadcode"
	"github.com/goplus/llgo/internal/env"
	"github.com/goplus/llgo/internal/firmware"
	"github.com/goplus/llgo/internal/flash"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/goplus/llgo/internal/header"
	"github.com/goplus/llgo/internal/lto"
	"github.com/goplus/llgo/internal/meta"
	"github.com/goplus/llgo/internal/mockable"
	"github.com/goplus/llgo/internal/monitor"
	"github.com/goplus/llgo/internal/optlevel"
	"github.com/goplus/llgo/internal/packages"
	"github.com/goplus/llgo/internal/pclnmap"
	"github.com/goplus/llgo/internal/pclnpost"
	"github.com/goplus/llgo/internal/typepatch"
	"github.com/goplus/llgo/ssa/abi"
	xenv "github.com/goplus/llgo/xtool/env"
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
	managedFunctionValueTarget     func(*ssa.Function) (*ssa.Function, bool, error)
	augmentFunctionIDs             func(coro.FunctionIDConfig) coro.FunctionIDConfig
	localBodyFacts                 func(*ssa.Function) (coro.SSAFunctionBodyFacts, error)
	functionBackground             func(*ssa.Function) (llssa.Background, bool, error)
	rawCFunctionType               func(types.Type) (bool, error)
	foreignNoBlock                 func(*ssa.Function) (cl.CoroForeignNoBlockCertificate, bool, error)
	foreignSync                    func(*ssa.Function) (cl.CoroForeignSyncCertificate, bool, error)
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
	erasedFunctionInterface        func(*ssa.MakeInterface) (bool, error)
	demandReferences               func(*ssa.Function) ([]*ssa.Function, error)
	syncDemandReferences           func(*ssa.Function) ([]*ssa.Function, error)
	managedValueReferences         func(*ssa.Function) ([]*ssa.Function, error)
	loweredCalls                   func(*ssa.Function) ([]coro.SSALoweredCall, error)
	requiredRoots                  coro.Roots
	requiredPlain                  map[*ssa.Function]struct{}
	requiredHostPlain              map[*ssa.Function]struct{}
	requiredDirectPlain            []requiredCoroDirectPlainCallArgument
	requiredClosedDynamic          map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate
	requiredGlobalFunctionSlots    map[ssa.CallInstruction]coroGlobalFunctionSlotProof
	// importedLibraryEffects contains only exact bodyless managed-Go
	// declarations whose producer metadata, FunctionID, structural ABI, target
	// ABI, and physical symbol were preflighted by the build driver. Go callers
	// inherit these facts through the ordinary SSA fixed point; no source
	// annotation is propagated.
	importedLibraryEffects map[*ssa.Function]coro.LibraryEffectFunction
	// libraryForeign contains exact producer-owned C
	// declaration identities and optional target-neutral contracts. The build
	// driver has already matched FunctionID, physical symbol, typed ABI, target
	// profile, and explicit-local conflict policy.
	libraryForeign map[*ssa.Function]coro.LibraryEffectForeignCallable
	recordAnalysis func(*coro.SSAPlan)
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
		external, exec, err := coro.CallableDeclarationPolicy(certificate.Contract)
		if err != nil {
			return coro.SSAFunctionPolicy{}, fmt.Errorf(
				"callable declaration %q: %w", fn.Name(), err,
			)
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
// ABI Method.Ifn_ word cl replaces with the universal method descriptor.
// Ordinary calls, defers, and goroutine spawns share that receiver-aware
// transport; their carrier selects only the call operation. Foreign
// classifications, raw method addresses, and generic or variadic methods
// retain their independent fail-closed domains.
func isCoroManagedInterfaceDescriptorCall(call ssa.CallInstruction) bool {
	switch carrier := call.(type) {
	case *ssa.Call:
		if carrier == nil {
			return false
		}
	case *ssa.Defer:
		if carrier == nil {
			return false
		}
	case *ssa.Go:
		if carrier == nil {
			return false
		}
	default:
		return false
	}
	if call.Common() == nil {
		return false
	}
	common := call.Common()
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
		in.foreignWorker != nil || in.callableIdentity != nil || in.callableContract != nil ||
		in.noPreempt != nil || in.noUnwind != nil || in.assemblyNoSuspend != nil ||
		len(in.importedLibraryEffects) != 0 ||
		len(in.libraryForeign) != 0 ||
		config.ClassifyFunction != nil {
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
			frontendPython := false
			frontendManagedBodyless := false
			if in.functionBackground != nil {
				background, classified, err := in.functionBackground(fn)
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("classify frozen frontend ABI for %q: %w", fn.Name(), err)
				}
				frontendC = classified && background == llssa.InC
				frontendPython = classified && background == llssa.InPython
				frontendManagedBodyless = classified && background == llssa.InGo && len(fn.Blocks) == 0
			}
			if frontendPython {
				if policy != (coro.SSAFunctionPolicy{}) {
					return coro.SSAFunctionPolicy{}, fmt.Errorf(
						"builder policy for Python declaration %q conflicts with compiler-owned lowering",
						fn.Name(),
					)
				}
				// The declaration itself is a typed frontend placeholder. Its exact
				// source call is assigned WaitForeign by the immutable SitePlan and
				// lowered through the generic same-M episode; there is no opaque body
				// edge left for the SSA effect solver to guess.
				return coro.SSAFunctionPolicy{
					IgnoreBody:       true,
					External:         coro.ExternalKnown,
					OverrideExternal: true,
					Exec:             coro.IRQUnsafe,
				}, nil
			}
			importedFact, imported := in.importedLibraryEffects[fn]
			if imported {
				if !frontendManagedBodyless || frontendC {
					return coro.SSAFunctionPolicy{}, fmt.Errorf(
						"imported library effect for %q does not name one exact bodyless managed-Go declaration",
						fn.Name(),
					)
				}
				if policy != (coro.SSAFunctionPolicy{}) {
					return coro.SSAFunctionPolicy{}, fmt.Errorf(
						"builder policy for imported library function %q conflicts with producer-owned metadata",
						fn.Name(),
					)
				}
				policy, err = importedFact.ImportedPolicy()
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf(
						"apply imported library effect for %q: %w", fn.Name(), err,
					)
				}
			}
			importedForeign, importedForeignOK := in.libraryForeign[fn]
			var importedForeignPolicy coro.SSAFunctionPolicy
			if importedForeignOK {
				importedForeignPolicy, err = importedForeign.ImportedPolicy()
				if err != nil {
					return coro.SSAFunctionPolicy{}, fmt.Errorf(
						"apply imported library foreign callable for %q: %w",
						fn.Name(), err,
					)
				}
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
			if importedForeignOK {
				if !frontendC {
					return coro.SSAFunctionPolicy{}, fmt.Errorf(
						"imported library foreign callable for %q does not name one exact frontend C declaration",
						fn.Name(),
					)
				}
				if !identityCertified || identityCertificate.IsZero() {
					return coro.SSAFunctionPolicy{}, fmt.Errorf(
						"imported library foreign callable for %q has no independently frozen local identity",
						fn.Name(),
					)
				}
				identityCertificate = importedForeign.Identity
				identityCertified = true
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
			if importedForeignOK {
				if importedForeign.HasContract {
					callableCertificate = importedForeign.Contract
					callableCertified = true
				} else {
					// Identity-only producer metadata deliberately suppresses
					// the consumer's reconstructed default contract. It grants
					// no worker/same-M/event operation.
					callableCertificate = cl.CoroCallableContractCertificate{}
					callableCertified = false
				}
			}
			certificateKinds := 0
			for _, present := range []bool{certified, syncCertified, workerCertified} {
				if present {
					certificateKinds++
				}
			}
			if importedForeignOK && certificateKinds != 0 {
				return coro.SSAFunctionPolicy{}, fmt.Errorf(
					"frontend C declaration %q has imported callable metadata and a legacy foreign-call certificate",
					fn.Name(),
				)
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
				if importedForeignOK && policy != importedForeignPolicy {
					return coro.SSAFunctionPolicy{}, fmt.Errorf(
						"frontend C declaration %q does not match its imported callable policy",
						fn.Name(),
					)
				}
			} else if importedForeignOK {
				identityOnly := coro.SSAFunctionPolicy{
					CallableIdentityCertificate: importedForeign.Identity,
				}
				if policy != identityOnly {
					return coro.SSAFunctionPolicy{}, fmt.Errorf(
						"frontend C declaration %q conflicts with its imported identity-only callable metadata",
						fn.Name(),
					)
				}
				policy = importedForeignPolicy
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
			if policy.IgnoreBody && !frontendC && !assemblyCertified &&
				!(frontendManagedBodyless && (certified || imported)) {
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
					if frozen && callSite.ControlOperation != cl.CoroControlNone {
						policy.Exec = policy.Exec.Join(callSite.ControlOperation.ExecFlags())
					}
					if frozen && (callSite.Elision == cl.CoroCallElidedCgoWorker ||
						callSite.Elision == cl.CoroCallElidedPython) {
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
						if !isCoroManagedDescriptorSpawn(spawn) &&
							!isCoroManagedInterfaceDescriptorCall(spawn) {
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
			// implementation. A may-block callable is admitted only when this
			// exact member belongs to the compiler-owned raw host/scheduler-stack
			// island.
			const supportedExec = coro.MayUnwind | coro.NeedsCleanupFrame | coro.IRQUnsafe | coro.NeedsRuntimeContext
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
			if callableWaitsForeign {
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
				if !callableWaitsForeign {
					policy.External = coro.ExternalKnown
					policy.OverrideExternal = true
				} else if policy.External != coro.ExternalUnknownForeign || policy.Exec != coro.BlockForeign|coro.IRQUnsafe {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("raw-host C declaration %q lost its managed unknown-foreign/blocking boundary", fn.Name())
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
		if frontend.Elision == cl.CoroCallElidedFrontendUnevaluated {
			// Preserve the ProgramIR decision when the omitted instruction also
			// satisfies AnalyzeSSA's narrow static-call gate. Generated cgo adapters
			// place _Cgo_use/_Cgo_keepalive behind runtime.cgoAlwaysFalse; their
			// blocks are absent from physical lowering and must not reappear as raw
			// closure edges. Dynamic calls, invokes, builtins, spawns, and alternate
			// defer stacks remain conservative until the analyzer has a complete
			// frontend-unevaluated instruction projection for those shapes.
			common := call.Common()
			if common == nil || common.StaticCallee() == nil || common.IsInvoke() {
				return false, nil
			}
			switch exact := call.(type) {
			case *ssa.Call:
				return exact != nil, nil
			case *ssa.Defer:
				return exact != nil && exact.DeferStack == nil, nil
			default:
				return false, nil
			}
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
	classifyStaticCallTarget := config.ClassifyStaticCallTarget
	config.ClassifyStaticCallTarget = func(caller *ssa.Function, call ssa.CallInstruction) (*ssa.Function, bool, error) {
		var requested *ssa.Function
		requestedRedirect := false
		if classifyStaticCallTarget != nil {
			var err error
			requested, requestedRedirect, err = classifyStaticCallTarget(caller, call)
			if err != nil {
				return nil, false, err
			}
		}
		if in.callSitePlan == nil {
			return requested, requestedRedirect, nil
		}
		frontend, frozen, err := in.callSitePlan(call)
		if err != nil {
			return nil, false, fmt.Errorf("read frozen static-call SitePlan in %q: %w", caller.Name(), err)
		}
		if !frozen {
			return nil, false, fmt.Errorf("call %q in %q is absent from the frozen ProgramIR", call.String(), caller.Name())
		}
		compilerTarget := frontend.ManagedStaticTarget
		compilerRedirect := compilerTarget != nil
		if compilerRedirect != (frontend.ManagedStaticTargetCertificate != "") {
			return nil, false, fmt.Errorf("managed static-call target in %q has incomplete frozen identity", caller.Name())
		}
		if requestedRedirect && (!compilerRedirect || requested != compilerTarget) {
			return nil, false, fmt.Errorf("builder cannot forge a managed static-call target in %q", caller.Name())
		}
		return compilerTarget, compilerRedirect, nil
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
	if in.erasedFunctionInterface != nil || config.ClassifyErasedFunctionInterface != nil {
		classifyErased := config.ClassifyErasedFunctionInterface
		config.ClassifyErasedFunctionInterface = func(caller *ssa.Function, box *ssa.MakeInterface) (bool, error) {
			compilerRequired := false
			var err error
			if in.erasedFunctionInterface != nil {
				compilerRequired, err = in.erasedFunctionInterface(box)
				if err != nil {
					return false, fmt.Errorf("classify frozen erased function interface in %q: %w", caller.Name(), err)
				}
			}
			if classifyErased != nil {
				requested, err := classifyErased(caller, box)
				if err != nil {
					return false, err
				}
				if requested && !compilerRequired {
					return false, fmt.Errorf("builder cannot erase a non-compiler function interface in %q", caller.Name())
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
	if in.managedValueReferences != nil || config.ClassifyManagedValueReferences != nil {
		classifyManagedValues := config.ClassifyManagedValueReferences
		config.ClassifyManagedValueReferences = func(owner *ssa.Function) ([]*ssa.Function, error) {
			var compilerTargets []*ssa.Function
			var err error
			if in.managedValueReferences != nil {
				compilerTargets, err = in.managedValueReferences(owner)
				if err != nil {
					return nil, fmt.Errorf(
						"classify frozen frontend managed value references for %q: %w",
						owner.Name(), err,
					)
				}
			}
			compilerTargets = append([]*ssa.Function(nil), compilerTargets...)
			if classifyManagedValues != nil {
				requested, err := classifyManagedValues(owner)
				if err != nil {
					return nil, err
				}
				if !sameExactCoroFunctionReferences(requested, compilerTargets) {
					return nil, fmt.Errorf(
						"builder managed value references in %q conflict with the frozen frontend references",
						owner.Name(),
					)
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
	if in.managedFunctionValueTarget != nil || config.ResolveManagedFunctionValue != nil {
		requested := config.ResolveManagedFunctionValue
		config.ResolveManagedFunctionValue = func(fn *ssa.Function) (*ssa.Function, bool, error) {
			var compilerTarget *ssa.Function
			compilerAdapted := false
			var err error
			if in.managedFunctionValueTarget != nil {
				compilerTarget, compilerAdapted, err = in.managedFunctionValueTarget(fn)
				if err != nil {
					return nil, false, fmt.Errorf(
						"resolve frozen managed function-value target for %q: %w",
						fn.Name(), err,
					)
				}
			}
			if requested != nil {
				requestedTarget, requestedAdapted, requestedErr := requested(fn)
				if requestedErr != nil {
					return nil, false, requestedErr
				}
				if in.managedFunctionValueTarget != nil &&
					(requestedAdapted != compilerAdapted || requestedTarget != compilerTarget) {
					return nil, false, fmt.Errorf(
						"builder managed function-value target for %q conflicts with the frozen frontend adapter",
						fn.Name(),
					)
				}
				if in.managedFunctionValueTarget == nil {
					return requestedTarget, requestedAdapted, nil
				}
			}
			return compilerTarget, compilerAdapted, nil
		}
	}
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
	provenance         map[*ssa.Function]coroRawABIPlainProvenance
	normal             map[*ssa.Function]struct{}
	hostStack          map[*ssa.Function]struct{}
	externalPlain      map[*ssa.Function]struct{}
	closedDynamic      map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate
	basePolicyEffects  map[*ssa.Function]coro.Effect
	rawSyncIntrinsics  map[ssa.CallInstruction]cl.CoroCallSitePlan
	nonRawLocalEffects map[*ssa.Function]coro.Effect
	normalReturnBlocks map[*ssa.Function]map[*ssa.BasicBlock]struct{}
	rawReferences      map[*ssa.Function][]*ssa.Function
	rawReferenceSeen   map[*ssa.Function]map[*ssa.Function]struct{}
}

type coroRawABIPlainProvenance struct {
	parent *ssa.Function
	site   string
}

func (closure *coroRawABIPlainClosure) provenancePath(fn *ssa.Function) string {
	if closure == nil || fn == nil {
		return ""
	}
	const maxDepth = 64
	steps := make([]string, 0, 8)
	seen := make(map[*ssa.Function]struct{})
	for current := fn; current != nil && len(steps) != maxDepth; {
		if _, duplicate := seen[current]; duplicate {
			steps = append(steps, current.String()+" (cycle)")
			break
		}
		seen[current] = struct{}{}
		provenance, ok := closure.provenance[current]
		if !ok {
			steps = append(steps, current.String())
			break
		}
		step := current.String()
		if provenance.site != "" {
			step += " [" + provenance.site + "]"
		}
		steps = append(steps, step)
		current = provenance.parent
	}
	slices.Reverse(steps)
	return strings.Join(steps, " -> ")
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
		provenance:         make(map[*ssa.Function]coroRawABIPlainProvenance),
		normal:             make(map[*ssa.Function]struct{}),
		hostStack:          make(map[*ssa.Function]struct{}),
		externalPlain:      make(map[*ssa.Function]struct{}),
		closedDynamic:      in.requiredClosedDynamic,
		basePolicyEffects:  basePolicyEffects,
		rawSyncIntrinsics:  make(map[ssa.CallInstruction]cl.CoroCallSitePlan),
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

	enqueueGoBody := func(fn *ssa.Function, normal, hostStack bool, provenance coroRawABIPlainProvenance) error {
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
		if !seen {
			closure.provenance[fn] = provenance
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
					if err := enqueueGoBody(target, true, false, coroRawABIPlainProvenance{
						site: "generated cgo worker entry",
					}); err != nil {
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
		if err := enqueueGoBody(root.Function, true, hostStack, coroRawABIPlainProvenance{
			site: "compiler/runtime raw ABI entry",
		}); err != nil {
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
					_, ok := value.(*ssa.MakeInterface)
					if !ok {
						return nil, fmt.Errorf("live raw ABI funcAddr publication in %q lost its MakeInterface shape", owner.Function.Name())
					}
					target, ok := preliminary.RawFunctionAddressTarget(call, argument)
					if !ok || target == nil || target.Signature == nil || target.Signature.Recv() != nil || len(target.FreeVars) != 0 {
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
					if err := enqueueGoBody(target, true, false, coroRawABIPlainProvenance{
						site: "published raw function address",
					}); err != nil {
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
				if err := enqueueGoBody(target, true, hostStack, coroRawABIPlainProvenance{
					parent: owner.Function,
					site:   "frontend raw ABI reference",
				}); err != nil {
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
			if err := enqueueGoBody(lowered.Target, normal && !lowered.UnwindOnly, hostStack, coroRawABIPlainProvenance{
				parent: fn,
				site:   "lowered helper " + lowered.LogicalName,
			}); err != nil {
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
					if callSite.RawPlainSynchronousIntrinsic {
						if !callSite.Intrinsic {
							return nil, fmt.Errorf("raw-plain synchronous call %q in %q is not a frozen intrinsic", direct.String(), fn.Name())
						}
						switch callSite.IntrinsicSemantics {
						case cl.CoroIntrinsicCallUnsupported:
							if callSite.ElidesCall() || callSite.ElisionCertificate != "" {
								return nil, fmt.Errorf("raw-plain synchronous call %q in %q has a malformed retained managed recipe", direct.String(), fn.Name())
							}
						case cl.CoroIntrinsicCallInlineSuspend, cl.CoroIntrinsicCallInlineNativeBlock:
							if callSite.Elision != cl.CoroCallElidedIntrinsic || callSite.ElisionCertificate == "" {
								return nil, fmt.Errorf("raw-plain synchronous call %q in %q has no exact worker elision certificate", direct.String(), fn.Name())
							}
						default:
							return nil, fmt.Errorf("raw-plain synchronous call %q in %q has incompatible managed semantics %d", direct.String(), fn.Name(), callSite.IntrinsicSemantics)
						}
						// A certified worker call parks in the managed twin. An
						// uncertified dynamic syscall remains fail-closed there.
						// In either case the separately proven raw/plain body uses
						// the exact ordinary synchronous intrinsic lowering.
						closure.rawSyncIntrinsics[direct] = callSite
					} else if callSite.Intrinsic && callSite.IntrinsicSemantics.SuspendsCurrentFrame() {
						closure.nonRawLocalEffects[fn] = closure.nonRawLocalEffects[fn].Join(callSite.IntrinsicSemantics.CurrentFrameEffect())
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
					if err := enqueueGoBody(target, targetNormal, hostStack, coroRawABIPlainProvenance{
						parent: fn,
						site:   makeClosure.String(),
					}); err != nil {
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
				if !ok || call.Common() == nil {
					continue
				}
				if _, rawSynchronous := closure.rawSyncIntrinsics[call]; rawSynchronous {
					continue
				}
				callPlan, planned := preliminary.CallPlan(call)
				if !planned {
					// Frontend-elided intrinsics have no physical direct edge;
					// their replacement helpers, if any, are covered above.
					continue
				}
				if direct, ordinary := call.(*ssa.Call); ordinary && call.Common().IsInvoke() {
					receiver, target, _, exact, err := preliminary.ResolveExactInterfaceCall(direct)
					if err != nil {
						return nil, fmt.Errorf("resolve exact raw interface call %q in %q: %w", call.String(), fn.Name(), err)
					}
					if exact {
						if receiver == nil || target == nil {
							return nil, fmt.Errorf("exact raw interface call %q in %q lost its receiver or target", call.String(), fn.Name())
						}
						targetNormal := normal && !closure.instructionUnwindOnly(fn, instruction)
						if err := enqueueGoBody(target, targetNormal, hostStack, coroRawABIPlainProvenance{
							parent: fn,
							site:   "exact interface " + call.String(),
						}); err != nil {
							return nil, err
						}
						// The raw/plain fixed point must preserve the exact method twin.
						// Ordinary SSA interface edges carry managed demand; this frozen
						// occurrence-local reference supplies the independent raw demand.
						closure.recordRawReference(fn, target)
						continue
					}
				}
				if call.Common().StaticCallee() == nil {
					continue
				}
				if callPlan.Kind == coro.CallForeign {
					// A required compiler/runtime C leaf is not part of the Go
					// body closure, but its exact occurrence still belongs to
					// this raw-host proof. Record that operation identity without
					// granting the declaration Go trust or weakening its managed
					// unknown/blocking policy.
					if callPlan.Open || len(callPlan.Targets) != 1 {
						if _, required := in.requiredPlain[call.Common().StaticCallee()]; required {
							return nil, fmt.Errorf("live raw ABI function %q reaches compiler-required foreign operation through an open call %q", fn.Name(), call.String())
						}
						continue
					}
					target, found := preliminary.Function(callPlan.Targets[0])
					if !found || target == nil {
						return nil, fmt.Errorf("live raw ABI foreign call in %q has an unresolved preliminary target", fn.Name())
					}
					if _, required := in.requiredPlain[target]; required {
						closure.externalPlain[target] = struct{}{}
					}
					continue
				}
				if callPlan.Kind != coro.CallDirect && callPlan.Kind != coro.CallDefer {
					// A spawned target has its own managed entry.
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
				if err := enqueueGoBody(target, targetNormal, hostStack, coroRawABIPlainProvenance{
					parent: fn,
					site:   call.String(),
				}); err != nil {
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
	const legacyExec = coro.IRQUnsafe | coro.NeedsPreempt | coro.MayUnwind | coro.NeedsCleanupFrame |
		coro.NoReturn | coro.PanicOnly | coro.NeedsRuntimeContext

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
		if _, compilerPlain := raw.externalPlain[target]; compilerPlain {
			if callable, certified := plan.CallableContractCertificate(target); certified &&
				callable.Scope == coro.CallableContractScopeDeclaration &&
				coroRawPlainDirectForeignContractCompatible(callable.Contract) {
				// Whether a may-block C declaration is safe to execute directly
				// is a property of this exact invocation, not of the declaration.
				// The compiler-owned raw host closure executes on its scheduler
				// stack and deliberately owns this physical wait. The same
				// declaration remains unknown/blocking in the managed plan, where
				// ordinary callers are colored WaitForeign and use a worker.
				if _, compilerOwned := raw.hostStack[owner]; !compilerOwned {
					return fmt.Errorf("live raw ABI plain closure %q reaches blocking raw-host target %q (%s) at %s outside a compiler-owned raw host/scheduler-stack island",
						owner.Name(), targetPlan.ID, target.String(), site)
				}
				expectedExec := coro.BlockForeign | coro.IRQUnsafe |
					coro.CallableContractExecConstraints(callable.Contract)
				if targetPlan.External != coro.ExternalUnknownForeign ||
					targetPlan.Emission != coro.EmitExternal ||
					targetPlan.Effect != coro.NoSuspend ||
					targetPlan.Exec != expectedExec {
					return fmt.Errorf("live raw ABI plain closure %q reaches malformed blocking raw-host target %q (%s) at %s (external=%s effect=%s exec=%s expected-exec=%s)",
						owner.Name(), targetPlan.ID, target.String(), site,
						targetPlan.External, targetPlan.Effect, targetPlan.Exec, expectedExec)
				}
				return nil
			}
			// requiredPlain is itself a frozen build-owned physical ABI proof for
			// an exact C declaration reached by this closed raw island. It does not
			// erase the declaration's plan facts: only the precise external-known,
			// no-suspend, nonblocking shape is admissible here. A compatible
			// may-block declaration was handled above under the stronger
			// invocation-scoped host-stack ownership proof.
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
			// This is one of two exact raw-stack contexts. A compiler-owned host
			// closure deliberately performs the physical wait on its scheduler
			// stack; any other member is reachable from a proven synchronous C
			// ABI entry and may block only that foreign caller's stack. Neither
			// interpretation reclassifies the declaration: the managed twin
			// retains WaitForeign and uses its selected foreign episode.
			_, compilerHostOperation := raw.hostStack[owner]
			expectedExec := coro.BlockForeign | coro.IRQUnsafe |
				coro.CallableContractExecConstraints(callable.Contract)
			if targetPlan.External == coro.ExternalUnknownForeign &&
				targetPlan.Emission == coro.EmitExternal &&
				targetPlan.Effect == coro.NoSuspend &&
				targetPlan.Exec == expectedExec {
				return nil
			}
			return fmt.Errorf("live raw ABI plain closure %q reaches malformed direct foreign callable target %q (%s) at %s (compiler-host-operation=%t external=%s effect=%s exec=%s expected-exec=%s)",
				owner.Name(), targetPlan.ID, target.String(), site,
				compilerHostOperation, targetPlan.External, targetPlan.Effect, targetPlan.Exec, expectedExec)
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
		if callable, certified := plan.CallableContractCertificate(target); certified &&
			callable.Scope == coro.CallableContractScopeDeclaration &&
			coro.CallableContractDirectExecutorCompatible(
				callable.Contract,
			) {
			return nil
		}
		_, foreignNoBlock := plan.ForeignNoBlockCertificate(target)
		_, foreignSync := plan.ForeignSyncCertificate(target)
		_, assemblyNoSuspend := plan.AssemblyNoSuspendCertificate(target)
		if !foreignNoBlock && !foreignSync && !assemblyNoSuspend {
			return fmt.Errorf("live raw ABI plain closure %q reaches external target %q (%s) at %s without an exact direct executor, foreign-noblock, foreign-sync, or assembly-no-suspend certificate",
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
		for call, site := range raw.rawSyncIntrinsics {
			if call == nil || call.Parent() != fn {
				continue
			}
			if !site.Intrinsic || !site.RawPlainSynchronousIntrinsic {
				return fmt.Errorf("live raw ABI plain closure function %q (%s) lost its exact synchronous raw intrinsic recipe", functionPlan.ID, fn.String())
			}
			switch site.IntrinsicSemantics {
			case cl.CoroIntrinsicCallUnsupported:
				if plan.ElidesCall(call) {
					return fmt.Errorf("live raw ABI plain closure function %q (%s) unexpectedly elided its managed fail-closed syscall edge", functionPlan.ID, fn.String())
				}
				if _, planned := plan.CallPlan(call); !planned {
					return fmt.Errorf("live raw ABI plain closure function %q (%s) lost its retained managed fail-closed syscall edge", functionPlan.ID, fn.String())
				}
			case cl.CoroIntrinsicCallInlineSuspend, cl.CoroIntrinsicCallInlineNativeBlock:
				plannedCertificate, planned := plan.ElidedCallCertificate(call)
				if !planned || !plan.ElidesCall(call) || plannedCertificate != site.ElisionCertificate {
					return fmt.Errorf("live raw ABI plain closure function %q (%s) lost its exact worker syscall certificate", functionPlan.ID, fn.String())
				}
			default:
				return fmt.Errorf("live raw ABI plain closure function %q (%s) has incompatible synchronous raw intrinsic semantics %d", functionPlan.ID, fn.String(), site.IntrinsicSemantics)
			}
			rawSyncEffects = rawSyncEffects.Join(site.IntrinsicSemantics.CurrentFrameEffect())
		}
		// Aggregate Effect/Exec intentionally retain conservative managed-primary
		// contributions from every explicit static call, including calls in a
		// block that cannot return normally. Raw feasibility instead validates
		// local facts and then every exact edge recursively, using the same CFG
		// terminal proof above. This keeps the managed fixed point untouched while
		// avoiding false ordinary reachability from fatal formatting/allocation.
		// A syscall intrinsic has a separate raw/plain meaning: a certified
		// managed worker parks, while the raw twin executes the ordinary
		// synchronous syscall. An uncertified dynamic trap keeps its managed
		// fail-closed edge but is equally exact in this proven raw body. Prove
		// every other source independently.
		if unsupported := raw.basePolicyEffects[fn] &^ managedOnlyEffects; unsupported != 0 {
			return fmt.Errorf("live raw ABI plain closure function %q (%s) has unsupported declared raw-stack effect %s", functionPlan.ID, fn.String(), unsupported)
		}
		nonRawLocalEffects := coroRawPlainSSALocalEffect(fn).Join(raw.nonRawLocalEffects[fn])
		if unsupported := nonRawLocalEffects &^ allowedEffects; unsupported != 0 {
			return fmt.Errorf("live raw ABI plain closure function %q (%s) has real local suspend effect %s; provenance: %s",
				functionPlan.ID, fn.String(), unsupported, raw.provenancePath(fn))
		}
		if unsupported := functionPlan.LocalEffect &^ (allowedEffects | rawSyncEffects); unsupported != 0 {
			return fmt.Errorf("live raw ABI plain closure function %q (%s) has real local suspend effect %s; provenance: %s",
				functionPlan.ID, fn.String(), unsupported, raw.provenancePath(fn))
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
				if _, rawSynchronous := raw.rawSyncIntrinsics[call]; rawSynchronous {
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
					if direct, ordinary := call.(*ssa.Call); ordinary && call.Common().IsInvoke() {
						receiver, target, _, exact, err := plan.ResolveExactInterfaceCall(direct)
						if err != nil {
							return fmt.Errorf("live raw ABI plain closure function %q exact interface call %q: %w", functionPlan.ID, call.String(), err)
						}
						if exact {
							if receiver == nil || target == nil {
								return fmt.Errorf("live raw ABI plain closure function %q exact interface call %q lost its receiver or target", functionPlan.ID, call.String())
							}
							targetTerminalOnly := terminalOnly || raw.instructionUnwindOnly(fn, instruction)
							if err := validateTarget(fn, target, targetTerminalOnly, "exact interface "+call.String()); err != nil {
								return err
							}
							continue
						}
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
	invalidDirectOperand := func() error {
		signature := common.Signature()
		variadic := signature != nil && signature.Variadic()
		return fmt.Errorf(
			"requires a direct static function or method operand; closures, interfaces, function values, and inline-only operations require a compiler-owned spawn carrier (operand %T %q, signature %v, variadic=%t, arguments=%d)",
			common.Value, common.Value.String(), signature, variadic, len(common.Args),
		)
	}
	if common.IsInvoke() || common.Method != nil {
		return nil, invalidDirectOperand()
	}
	target := (*ssa.Function)(nil)
	redirected := false
	spawnWrapper := false
	if in.callSitePlan != nil {
		site, frozen, err := in.callSitePlan(spawn)
		if err != nil {
			return nil, fmt.Errorf("read frozen static-spawn SitePlan: %w", err)
		}
		if !frozen {
			return nil, fmt.Errorf("spawn is absent from the frozen ProgramIR")
		}
		if (site.ManagedStaticTarget != nil) != (site.ManagedStaticTargetCertificate != "") {
			return nil, fmt.Errorf("spawn managed-call target has incomplete frozen identity")
		}
		if site.ManagedStaticTarget != nil && site.StaticSpawnTarget != nil {
			return nil, fmt.Errorf("spawn has both a managed-call target and an intrinsic wrapper")
		}
		target = site.ManagedStaticTarget
		if target == nil {
			target = site.StaticSpawnTarget
			spawnWrapper = target != nil
		}
		redirected = target != nil
	}
	if !redirected {
		if !direct || raw == nil || common.StaticCallee() != raw {
			return nil, invalidDirectOperand()
		}
		target = raw
	} else if !spawnWrapper && (!direct || raw == nil || common.StaticCallee() != raw) {
		return nil, invalidDirectOperand()
	}
	canonical, ok := in.ResolveFunction(target)
	if !ok || canonical == nil || canonical != target {
		return nil, fmt.Errorf("target %q is outside the frozen emission universe", target.Name())
	}
	if redirected && direct && target == raw {
		return nil, fmt.Errorf("target %q is not one distinct compiler-owned spawn redirect", target.Name())
	}
	if spawnWrapper && target.Synthetic == "" {
		return nil, fmt.Errorf("target %q is not one compiler-owned spawn wrapper", target.Name())
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
	if redirected && !spawnWrapper && !types.Identical(common.Signature(), sig) {
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
				expectedOpen := coro.UnknownManagedDispatch
				if spawn.Common() != nil && spawn.Common().IsInvoke() {
					expectedOpen = coro.UnknownManagedInterfaceDispatch
				}
				if callPlan.Open && callPlan.Unresolved != expectedOpen {
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
					if _, err := plan.ResolveManagedSpawn(spawn); err != nil {
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
		(targetPlan.FuncRep != coro.DirectCoro && targetPlan.FuncRep != coro.Dispatch) ||
		targetPlan.Demand != coro.AsyncDemand ||
		!targetPlan.Effect.Contains(coro.YieldOnly) {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"spawn method target %q is not one demanded preemptible direct-callable coroutine (external=%s emission=%s primary=%s representation=%s demand=%s effect=%s)",
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
					if _, err := plan.ResolveManagedSpawn(spawn); err != nil {
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
							if effect.IsOpaque() {
								trace = plan.OpaqueEffectTrace(targetFunction)
								if target.Exec.Contains(coro.OpaqueExec) {
									trace += "; opaque exec path: " +
										coroProgramOpaqueExecPath(plan, targetFunction)
								}
							} else {
								trace = plan.SuspensionEffectTrace(targetFunction, effect&^allowed)
							}
						}
						return fmt.Errorf(
							"validate coroutine spawn in %q (%s): target %q (%s) effect %s (local=%s declared=%s exec=%s) is outside the production main-return cancellation subset %s; effect trace: %s",
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
	Goos               string
	Goarch             string
	Target             string // target name (e.g., "rp2040", "wasi") - takes precedence over Goos/Goarch
	ImportCfg          string // go tool compile importcfg; packagefile archives may carry LLGo producer metadata
	OptLevel           optlevel.Level
	LTO                lto.Mode
	LTOPlugin          lto.PassPlugin
	BinPath            string
	AppExt             string  // ".exe" on Windows, empty on Unix
	OutFile            string  // only valid for ModeBuild when len(pkgs) == 1
	OutFmts            OutFmts // Output format specifications (only for Target != "")
	CompileOnly        bool    // compile test binary but do not run it (only valid for ModeTest)
	Emulator           bool    // run in emulator mode
	Port               string  // target port for flashing
	BaudRate           int     // baudrate for serial communication
	RunArgs            []string
	Mode               Mode
	BuildMode          BuildMode // Build mode: exe, c-archive, c-shared
	AbiMode            AbiMode
	GenExpect          bool // only valid for ModeCmpTest
	Verbose            bool
	PrintPackages      bool // print package paths as they are compiled, like go build -v
	PrintCommands      bool
	GenLL              bool // generate pkg .ll files
	DeadcodeDrop       bool // enable Go dead code drop (development builds only)
	CollectPackageMeta bool // collect package metadata without enabling dead code drop
	CheckLLFiles       bool // check .ll files valid
	CheckLinkArgs      bool // check linkargs valid
	ForceEspClang      bool // force to use esp-clang
	ForceRebuild       bool // force rebuilding of packages that are already up-to-date
	Tags               string
	SizeReport         bool   // print size report after successful build
	SizeFormat         string // size report format: text,json (default text)
	SizeLevel          string // size aggregation level: full,module,package (default module)
	CompilerHash       string // metadata hash for the running compiler (development builds only)
	GoVersion          string // Go language version accepted by the frontend (for example, "go1.22")
	NoErrorColumn      bool   // omit source columns from frontend diagnostics
	// GoBuildFlags contains normalized raw Go build flags forwarded to
	// go/packages. Callers use internal/goflags to parse supported compiler and
	// linker semantics into typed Config fields before calling Do.
	GoBuildFlags []string
	// BuildParallelism is the package-level concurrency requested by Go's -p
	// build flag for llgo test. Zero uses the Go default, GOMAXPROCS.
	BuildParallelism int
	LinkOptions      LinkOptions
	// OmitDWARFByDefault controls linked builds only when -w was not
	// explicitly specified. Explicit -w and -w=false always win.
	OmitDWARFByDefault bool
	PCLNMode           PCLNMode
	// PCLNModeSet marks PCLNMode as authoritative. Command flags set it for
	// explicit requests; Do sets it after resolving the legacy environment
	// default.
	PCLNModeSet bool
	AllowNoBody bool // allow declarations without bodies, as go tool compile does
	// DisableBoundsChecks disables index, slice, and slice-to-array conversion
	// bounds checks while retaining required integer conversions and nil checks.
	DisableBoundsChecks bool

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
	Overlay          map[string][]byte

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

func (conf *Config) coroHostOperationSupported() bool {
	return conf != nil && hostCoroPullRuntimeABI(conf)
}

func (conf *Config) coroNativeFleetSupported() bool {
	return conf != nil && nativeCoroTimerRuntimeABI(conf) && nativeCoroWorkerRuntimeABI(conf)
}

func (conf *Config) coroTargetCapabilities() coro.TargetCapabilities {
	return coro.NewTargetCapabilities(
		conf.coroWorkerSupported(),
		conf.coroNativeFleetSupported(),
		conf.coroHostOperationSupported(),
	)
}

type Rewrites map[string]string

// clone returns an independent copy of c for use by a single build. Do
// resolves defaults and target-specific values on this copy so callers can
// safely reuse their input configuration after Do returns.
func (c *Config) clone() *Config {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.RunArgs = slices.Clone(c.RunArgs)
	cloned.GoBuildFlags = slices.Clone(c.GoBuildFlags)
	cloned.Overlay = cloneOverlay(c.Overlay)
	if c.GlobalRewrites != nil {
		cloned.GlobalRewrites = make(map[string]Rewrites, len(c.GlobalRewrites))
		for pkgPath, rewrites := range c.GlobalRewrites {
			if rewrites == nil {
				cloned.GlobalRewrites[pkgPath] = nil
				continue
			}
			copied := make(Rewrites, len(rewrites))
			for name, value := range rewrites {
				copied[name] = value
			}
			cloned.GlobalRewrites[pkgPath] = copied
		}
	}
	return &cloned
}

// resolveBuildConfig validates and fills build-local defaults without
// modifying the caller's Config. Target-derived GOOS/GOARCH values are
// resolved later, after crosscompile.Use has selected the toolchain.
func resolveBuildConfig(input *Config) (*Config, error) {
	if input == nil {
		return nil, errors.New("build config must not be nil")
	}
	conf := input.clone()
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
	if conf.BuildMode != BuildModeExe {
		conf.DeadcodeDrop = false
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
	return conf, nil
}

func NewDefaultConf(mode Mode) *Config {
	bin := os.Getenv("GOBIN")
	if bin == "" {
		gopath, err := envGOPATH()
		if err != nil {
			panic(fmt.Errorf("cannot get GOPATH: %v", err))
		}
		bin = filepath.Join(gopath, "bin")
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

func (c *Config) deadcodeDropEnabled() bool {
	return buildenv.Dev && c.DeadcodeDrop && !c.goGlobalDCEEnabled()
}

func (c *Config) packageMetaEnabled() bool {
	return c.CollectPackageMeta || c.deadcodeDropEnabled()
}

func (c *Config) parallelism() int {
	if c != nil && c.BuildParallelism > 0 {
		return c.BuildParallelism
	}
	return max(1, runtime.GOMAXPROCS(0))
}

// -----------------------------------------------------------------------------

const (
	loadFiles   = packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles
	loadImports = loadFiles | packages.NeedImports
	loadTypes   = loadImports | packages.NeedTypes | packages.NeedTypesSizes
	loadSyntax  = loadTypes | packages.NeedSyntax | packages.NeedTypesInfo
)

var llssaInitOnce sync.Once

func Do(args []string, conf *Config) ([]Package, error) {
	return Build(Invocation{Args: args, Config: conf})
}

// Build executes one build invocation.
func Build(inv Invocation) ([]Package, error) {
	dir := inv.Dir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	environ := os.Environ()
	commands := commandEnv{dir: dir, environ: environ}
	conf, err := resolveBuildConfig(inv.Config)
	if err != nil {
		return nil, err
	}
	llgoRuntimeDir := env.LLGoRuntimeDir()
	if llgoRuntimeDir == "" {
		return nil, fmt.Errorf("cannot locate the LLGo runtime source tree; set LLGO_ROOT to an LLGo checkout or installation root")
	}
	// Handle crosscompile configuration first to set correct GOOS/GOARCH
	forceEspClang := conf.ForceEspClang || conf.Target != ""
	export, err := crosscompile.Use(conf.Goos, conf.Goarch, conf.Target, wasiThreadsForBuild(conf), forceEspClang, conf.OptLevel, conf.ltoMode(), conf.goGlobalDCEEnabled())
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
	verbose := conf.Verbose
	patterns := slices.Clone(inv.Args)
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
		Dir:        dir,
		Fset:       token.NewFileSet(),
		Tests:      conf.Mode == ModeTest,
		Env:        withEnv(environ, "GOOS="+conf.Goos, "GOARCH="+conf.Goarch),
	}
	if conf.Mode == ModeTest {
		cfg.Mode |= packages.NeedForTest
	}
	emitDebugInfo := shouldEmitDebugInfo(conf, &export)
	frontendOptions := cl.Options{
		Debug:        emitDebugInfo,
		DebugSymbols: emitDebugInfo,
		Trace:        IsTraceEnabled(),
		ExportRename: conf.Target != "",
		ShadowStack:  isEnvOn(llgoShadowStack, false),
	}
	preloadOptions := frontendOptions
	llssaInitOnce.Do(func() {
		llssa.Initialize(llssa.InitAll)
	})

	target := newLLSSATarget(conf, export)

	prog := llssa.NewProgram(target)
	prog.DisableBoundsChecks(conf.DisableBoundsChecks)
	programOwnershipTransferred := false
	defer func() {
		if !programOwnershipTransferred {
			prog.Dispose()
		}
	}()
	prog.EnableGoGlobalDCE(conf.goGlobalDCEEnabled())
	prog.EnableDeadcodeDrop(conf.deadcodeDropEnabled())
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
		return prog.TypeSizes(llgoTargetTypeSizes(sizes, compiler, arch, export.LLVMTarget))
	}
	dedup := packages.NewDeduper()
	var syntaxErr error
	var syntaxErrMu sync.Mutex
	recordSyntaxErr := func(err error) {
		syntaxErrMu.Lock()
		defer syntaxErrMu.Unlock()
		if syntaxErr == nil {
			syntaxErr = err
		}
	}
	loadSyntaxErr := func() error {
		syntaxErrMu.Lock()
		defer syntaxErrMu.Unlock()
		return syntaxErr
	}
	dedup.SetPreload(func(pkg *types.Package, files []*ast.File) {
		if llruntime.SkipToBuild(pkg.Path()) {
			return
		}
		if err := cl.ParsePkgSyntaxWithOptions(prog, cfg.Fset, pkg, files, preloadOptions); err != nil {
			recordSyntaxErr(err)
		}
	})

	if patterns == nil {
		patterns = []string{"."}
	}
	sourcePatchGOROOT, sourcePatchGoVersion, err := env.GOROOTAndGOVERSIONWithEnv(cfg.Env)
	if err != nil {
		return nil, err
	}
	var llgoFiles map[string][]string
	conf.Overlay, llgoFiles, err = buildSourcePatchOverlayForGOROOT(conf.Overlay, llgoRuntimeDir, sourcePatchGOROOT, sourcePatchBuildContext{
		goos:       conf.Goos,
		goarch:     conf.Goarch,
		goversion:  sourcePatchGoVersion,
		buildFlags: cfg.BuildFlags,
	})
	if err != nil {
		return nil, err
	}
	dedup.SetLLGoFiles(llgoFiles)
	cfg.ParseFile = func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
		if data, ok := conf.Overlay[filename]; ok {
			src = data
		}
		// We implicitly promise to keep doing ast.Object resolution. :(
		const mode = parser.AllErrors | parser.ParseComments
		return parser.ParseFile(fset, filename, src, mode)
	}

	initial, err := packages.LoadExWithGoVersion(dedup, sizes, cfg, conf.GoVersion, patterns...)
	if err != nil {
		return nil, err
	}
	if err := loadSyntaxErr(); err != nil {
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
	altCfg.Dir = llgoRuntimeDir
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
	if err := loadSyntaxErr(); err != nil {
		return nil, err
	}

	runtimeTypes := altPkgs[0].Types
	pythonPkg := dedup.Check(llssa.PkgPython)
	var pythonTypes *types.Package
	if pythonPkg != nil {
		pythonTypes = pythonPkg.Types
	}
	prog.SetRuntime(func() *types.Package {
		return runtimeTypes
	})
	prog.SetPython(func() *types.Package {
		return pythonTypes
	})
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
	prog.SetDebugInfoOptimized(passOpt && conf.OptLevel != optlevel.O0)
	progSSA := ssa.NewProgram(initial[0].Fset, buildMode)
	patches := make(cl.Patches, len(altPkgPaths))
	altEntries := registerAltSSAPkgs(progSSA, patches, altPkgs[1:], conf, verbose)
	if err := preloadPatchedPackageSyntax(prog, patches, dedup, preloadOptions); err != nil {
		return nil, err
	}
	if err := prepareLocalVariables(prog, initial, altPkgs); err != nil {
		return nil, err
	}
	frontendOptions.PreloadedSyntax = true

	output := conf.OutFile != ""
	ctx := &context{conf: cfg, progSSA: progSSA, prog: prog, dedup: dedup,
		patches: patches, callerTracking: cl.NewCallerTracking(),
		goRoot: sourcePatchGOROOT,
		built:  make(map[string]none), initial: initial, mode: mode,
		fingerprinting:  make(map[string]bool),
		pkgs:            map[*packages.Package]Package{},
		pkgByID:         map[string]Package{},
		output:          output,
		passOpt:         passOpt,
		buildConf:       conf,
		crossCompile:    export,
		commands:        commands,
		frontendOptions: frontendOptions,
		cTransformer:    cabi.NewTransformer(prog, export.LLVMTarget, export.TargetABI, conf.AbiMode, cabiOptimize),
	}
	defer ctx.closePackageMetas()
	defer ctx.closePackageArchiveBuffers()
	defer ctx.cleanupStagedBitcodeFiles()

	// default runtime globals must be registered before packages are built
	addGlobalString(conf, "runtime.defaultGOROOT="+runtime.GOROOT(), nil)
	addGlobalString(conf, "runtime.buildVersion="+runtime.Version(), nil)
	pkgs, pkgEntries, err := registerSSAPkgs(ctx, initial, verbose)
	if err != nil {
		return nil, err
	}
	depPkgs, depEntries, err := registerSSAPkgs(ctx, altPkgs, verbose)
	if err != nil {
		return nil, err
	}
	buildSSAPkgs(ctx, append(append(altEntries, pkgEntries...), depEntries...))
	ctx.callerTracking.Precompute(ctx.progSSA.AllPackages())

	allPkgs := append([]*aPackage{}, pkgs...)
	allPkgs = append(allPkgs, depPkgs...)
	if err := buildCoroPlan(ctx, allPkgs...); err != nil {
		return nil, err
	}
	if shouldStageNativeExecutableBackend(ctx) {
		releaseCoroPlanningScratchBeforeEmission(ctx)
		progSSA = nil
		dedup = nil
		altPkgs = nil
		pkgs = nil
	}
	// Whole-program planning deliberately stays live through package emission,
	// but package loading, canonicalization fixed points, and identity sorting
	// leave substantial temporary Go storage behind. Return those dead pages
	// before LLVM starts its per-package peak rather than overlapping both
	// phases in standard-library-sized builds.
	debug.FreeOSMemory()
	allPkgs, err = buildAllPkgs(ctx, allPkgs, verbose)
	if err != nil {
		return nil, err
	}
	if shouldStageNativeExecutableBackend(ctx) {
		if err := stageMainEntryBitcodes(ctx, initial, allPkgs); err != nil {
			return nil, err
		}
		releaseCoroFrontendForStagedBackend(ctx, allPkgs)
		progSSA = nil
		patches = nil
		dedup = nil
		altPkgs = nil
		pkgs = nil
		cfg = nil
		// Package and entry bitcode are now complete and self-describing.
		// Disposing the compilation context releases LLVM's context-wide type,
		// constant, and metadata uniquing tables before any backend process is
		// allowed to start.
		prog.Dispose()
		debug.FreeOSMemory()
		if err := materializeStagedPackageBackends(ctx, allPkgs, verbose); err != nil {
			return nil, err
		}
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
			resolveOutputs(ctx.commands.dir, outFmts)

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
					err = runInEmulator(ctx.commands, ctx.crossCompile.Emulator, envMap, pkg.Dir, pkg.PkgPath, conf, mode, verbose)
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

func llgoTargetTypeSizes(sizes types.Sizes, _, arch, llvmTarget string) types.Sizes {
	if arch == "wasm" || strings.HasPrefix(llvmTarget, "wasm32-") {
		// LLGo's wasm targets use 32-bit words and pointers, while LLVM's wasm
		// data layout gives i64/f64 an 8-byte ABI alignment. Named TinyGo-style
		// targets retain GOARCH=arm for source selection, so the resolved LLVM
		// triple—not only GOARCH—must select this layout. Keep the frontend
		// layout identical so unsafe constants and LLVM GEP/alloc sizes cannot
		// disagree.
		return &types.StdSizes{WordSize: 4, MaxAlign: 8}
	}
	return sizes
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
	coroWasmGCBuildTag      = "llgo_wasm_gc"
	closureEnvNestBuildTag  = "llgo_closure_env_nest"
	closureEnvSwiftBuildTag = "llgo_closure_env_swiftself"
	closureEnvExplicitTag   = "llgo_closure_env_explicit"
)

func targetWasmGCBuildTags(conf *Config, export crosscompile.Export) ([]string, error) {
	if !strings.HasPrefix(export.LLVMTarget, "wasm32-") {
		return nil, nil
	}
	target, goos, goarch := "", "", ""
	var buildMode BuildMode
	if conf != nil {
		target = conf.Target
		goos = conf.Goos
		goarch = conf.Goarch
		buildMode = conf.BuildMode
	}
	switch export.GC {
	case "precise":
		return nil, fmt.Errorf("WebAssembly target %q requests precise GC, but only the non-moving conservative frame profile is implemented", target)
	case "conservative":
		if conf != nil && conf.Goos == "wasip1" && conf.Goarch == "wasm" && wasiThreadsForBuild(conf) {
			return nil, fmt.Errorf("WebAssembly conservative GC requires a serialized executor; WASI threads are enabled for target %q", target)
		}
		if conf == nil || goos != "wasip1" || goarch != "wasm" || buildMode != BuildModeExe {
			return nil, fmt.Errorf("WebAssembly conservative GC is only implemented for the serialized WASI Preview 1 command profile; target %q uses GOOS=%q GOARCH=%q buildmode=%q", target, goos, goarch, buildMode)
		}
		return []string{coroWasmGCBuildTag}, nil
	default:
		return nil, nil
	}
}

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

	target := newLLSSATarget(conf, export)
	tags := []string{"llgo", "math_big_pure_go", "purego", target.ClosureEnvBuildTag()}
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
	wasmGCTags, err := targetWasmGCBuildTags(conf, export)
	if err != nil {
		return "", err
	}
	tags = append(tags, wasmGCTags...)
	tags = append(tags, splitSourcePatchBuildTags(conf.Tags)...)
	tags = append(tags, goFlagTags...)
	tags = append(tags, targetTags...)
	return strings.Join(tags, ","), nil
}

func rejectCompilerReservedBuildTags(source string, tags []string) error {
	for _, tag := range tags {
		switch tag {
		case coroNativePipeBuildTag, coroNativeTimerBuildTag, coroWasmGCBuildTag,
			closureEnvNestBuildTag, closureEnvSwiftBuildTag, closureEnvExplicitTag:
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
	if ctx.progSSA == nil && len(packages) == 0 && ctx.buildConf.CoroPlanBuilder == nil {
		// A configuration-only caller has no program to analyze. Production Do
		// always installs progSSA before this phase; retaining the no-op keeps
		// validation helpers total without manufacturing an empty SSA plan.
		return nil
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
	var importedLibraryEffects map[*ssa.Function]coro.LibraryEffectFunction
	var libraryForeign map[*ssa.Function]coro.LibraryEffectForeignCallable
	var importedLibraryMetadata coro.LibraryEffectMetadata
	if ctx.coroEmission != nil && ctx.buildConf.ImportCfg != "" {
		index, metadata, err := loadCoroLibraryEffectIndex(ctx)
		if err != nil {
			return fmt.Errorf("load coroutine library effects: %w", err)
		}
		importedLibraryMetadata = metadata
		importedLibraryEffects, err = prepareCoroImportedLibraryEffects(ctx, index, metadata)
		if err != nil {
			return fmt.Errorf("prepare coroutine library effects: %w", err)
		}
		libraryForeign, err =
			prepareCoroImportedLibraryForeignCallables(ctx, index, metadata)
		if err != nil {
			return fmt.Errorf("prepare coroutine library foreign callables: %w", err)
		}
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
		requiredRoots, requiredPlain, requiredDirectPlain, requiredClosedDynamic, err =
			requiredCoroProgramRuntimePlanWithLibrary(ctx, libraryForeign)
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
		directPlain, plainClosure, err := requiredCoroDirectPlainCallArgumentsWithLibrary(
			ctx, requiredClosedDynamic, libraryForeign,
		)
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
		importedLibraryEffects:      importedLibraryEffects,
		libraryForeign:              libraryForeign,
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
		input.managedFunctionValueTarget = ctx.coroEmission.CoroManagedFunctionValueTarget
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
		input.erasedFunctionInterface = ctx.coroEmission.CoroErasedFunctionInterface
		input.demandReferences = ctx.coroEmission.CoroDemandReferences
		input.syncDemandReferences = ctx.coroEmission.CoroSyncDemandReferences
		input.managedValueReferences = ctx.coroEmission.CoroManagedValueReferences
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
		CoroPlan:                    plan,
		CoroPlanObserver:            ctx.buildConf.CoroPlanObserver,
		CoroTargetCapabilities:      ctx.buildConf.coroTargetCapabilities(),
		CoroFrameRetentionABI:       frameRetentionABI,
		CoroPlanDigest:              digest,
		CoroPlanMetadata:            metadata,
		CoroLoweringFacts:           loweringFacts,
		CoroLoweringFactsDigest:     loweringFactsDigest,
		CoroABI:                     metadata.CoroABI,
		SchedulerABI:                metadata.SchedulerABI,
		PanicABI:                    metadata.PanicABI,
		FuncRepABI:                  metadata.FuncRepABI,
		EmissionUniverse:            ctx.coroEmission,
		CoroLibraryEffectMetadata:   importedLibraryMetadata,
		CoroLibraryEffects:          maps.Clone(importedLibraryEffects),
		CoroLibraryForeignCallables: maps.Clone(libraryForeign),
	}
	if ctx.coroEmission != nil && ctx.coroEmission.CompleteRuntimeABI() {
		programCapabilities, err := ctx.clCompilation.CoroProgramCapabilities()
		if err != nil {
			ctx.coroPlan = nil
			ctx.coroPlanDigest = ""
			ctx.coroPlanMetadata = coro.PlanDigestMetadata{}
			ctx.coroLoweringFacts = coro.LoweringFacts{}
			ctx.coroLoweringFactsDigest = ""
			ctx.clCompilation = nil
			return fmt.Errorf("freeze coroutine program capabilities: %w", err)
		}
		ctx.coroProgramCapabilities = programCapabilities
	}
	if ctx.prog != nil {
		ctx.prog.SetLogicalLocality(true)
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

// requiredCoroProgramManagedEntryRoots injects every compiler-scheduled Go
// package initializer, followed by the exact main-package initializer and main
// body, as managed async-capable roots for the runnable startup program.
// Duplicate builder roots are harmless: AnalyzeSSA joins demand by canonical
// function. Descriptor-only builds keep their historical explicit-root
// contract and legacy native entry.
func requiredCoroProgramManagedEntryRoots(ctx *context) (coro.Roots, error) {
	if ctx == nil || ctx.buildConf == nil {
		return nil, nil
	}
	// Isolated planner tests and analysis-only callers may have no linked
	// program packages at all. There is then no main/public-runtime entry to
	// inject and no emission universe is required merely to build an empty
	// plan. Real Do builds install initial packages before this phase.
	if len(ctx.initial) == 0 && ctx.coroEmission == nil {
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
	patchInits, err := coroProgramPatchInitializersByOwner(ctx)
	if err != nil {
		return nil, err
	}
	scheduled := make(map[*ssa.Function]struct{})
	if hasPublicRuntimeInit {
		scheduled[publicRuntimeInit] = struct{}{}
	}
	appendManaged := func(fn *ssa.Function, label string) error {
		if fn == nil {
			return fmt.Errorf("coroutine managed program root %s: exact SSA function is missing", label)
		}
		resolved, ok := ctx.coroEmission.Resolve(fn)
		if !ok || resolved == nil || resolved != fn {
			return fmt.Errorf("coroutine managed program root %s: exact function is absent from the frozen emission universe", label)
		}
		goBody, err := frozenGoEmittedBody(ctx.coroEmission, fn)
		if err != nil {
			return fmt.Errorf("classify coroutine managed program root %s: %w", label, err)
		}
		if !goBody {
			return fmt.Errorf("coroutine managed program root %s has no emitted Go body", label)
		}
		roots = append(roots, coro.Root{Function: fn, Demand: coro.AsyncDemand})
		return nil
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
		// Package identity must use the source types path. llssa.PathOf is an
		// ABI-symbol projection and intentionally returns "main" for a main
		// package.
		if aPkg == nil || aPkg.SSA == nil || aPkg.SSA.Pkg == nil || aPkg.SSA.Pkg.Path() != pkg.PkgPath {
			return nil, fmt.Errorf("coroutine managed program roots: linked main package %q has no exact SSA package", pkg.ID)
		}
		packageInits, err := packageInitEntries(pkg, func(imported *packages.Package) Package {
			return contextPackage(ctx, imported)
		})
		if err != nil {
			return nil, fmt.Errorf("coroutine managed program roots: %w", err)
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
			if err := appendManaged(function, "package initializer "+entry.pkg.PkgPath); err != nil {
				return nil, err
			}
		}
		for _, name := range []string{"init", "main"} {
			if err := appendManaged(aPkg.SSA.Func(name), name); err != nil {
				return nil, err
			}
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
		managedCgoErrno := 0
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				site, frozen, siteErr := ctx.coroEmission.CoroCallSitePlan(call)
				if siteErr != nil {
					return nil, nil, fmt.Errorf(
						"classify coroutine raw ABI entry %q managed C2 transaction: %w",
						fn.Name(), siteErr,
					)
				}
				if frozen && site.Elision == cl.CoroCallElidedIntrinsic &&
					site.Intrinsic &&
					site.IntrinsicSemantics == cl.CoroIntrinsicCallInlineForeignSuspend {
					managedCgoErrno++
				}
			}
		}
		if managedCgoErrno != 0 {
			if directive != "//go:cgo_unsafe_args" || managedCgoErrno != 1 {
				return nil, nil, fmt.Errorf(
					"managed C2 adapter %q has directive %q and %d foreign-suspend transactions",
					fn.Name(), directive, managedCgoErrno,
				)
			}
			// In a generated _C2func_* wrapper this directive describes the
			// source argument-frame convention; it does not publish the Go
			// wrapper as an independently callable raw entry. The exact typed
			// worker transaction frozen above is its sole physical crossing.
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
	if conf != nil && conf.coroHostOperationSupported() {
		return coro.SchedulerProgramBootstrapChannelHostOperationClosedStaticSpawnABIV0
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
// implementation and remains excluded. Generic profiles detach after the
// initial slice and require an embedding to consume the pull ABI; the narrower
// WASI Preview 1 command selector below owns a built-in consumer.
func hostCoroPullRuntimeABI(conf *Config) bool {
	if conf == nil ||
		nativeCoroDoorbellRuntimeABI(conf) || configHasBuildTag(conf, "coro_runtime_adapter_test") {
		return false
	}
	return conf.Goarch == "wasm" || configHasBuildTag(conf, "tinygo.wasm") ||
		configHasBuildTag(conf, "baremetal") ||
		configHasBuildTag(conf, "llgo_coro_host")
}

// wasiCoroCommandRuntimeABI is the one host-pull profile whose ordinary
// command entry owns a built-in reactor. WASI Preview 1 has a synchronous
// poll_oneoff boundary, so the compiler can call a plain platform pump after
// every managed RunSlice has returned. Preview 2 remains a component/layout
// target until its pollable bindings and component entry lifecycle exist.
func wasiCoroCommandRuntimeABI(conf *Config) bool {
	if conf == nil || conf.BuildMode != BuildModeExe ||
		(conf.Goos != "wasip1" && conf.Goos != "wasi") ||
		conf.Goarch != "wasm" || configHasBuildTag(conf, "wasip2") {
		return false
	}
	return hostCoroPullRuntimeABI(conf)
}

func validateCoroHostPullEntryConfig(conf *Config, pyInit bool) error {
	if pyInit && hostCoroPullRuntimeABI(conf) && !wasiCoroCommandRuntimeABI(conf) {
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

// coroTimerRuntimeABI is the target-neutral timer/event capability consumed by
// compiler lowering. Native targets provide it with the POSIX timer reactor;
// host-pull targets provide the same Timer/Park protocol through Alarm actions
// and host-published monotonic time. Poll and blocking-worker capabilities stay
// native-only and are intentionally not implied by this predicate.
func coroTimerRuntimeABI(conf *Config) bool {
	return nativeCoroTimerRuntimeABI(conf) || hostCoroPullRuntimeABI(conf)
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

func validCoroHostOperationActionPointerV1(typ types.Type) bool {
	pointer, ok := types.Unalias(typ).(*types.Pointer)
	if !ok {
		return false
	}
	action, ok := pointer.Elem().Underlying().(*types.Struct)
	if !ok || action.NumFields() != 7 {
		return false
	}
	for index := 0; index < 6; index++ {
		if !types.Identical(action.Field(index).Type(), types.Typ[types.Uint32]) || action.Tag(index) != "" {
			return false
		}
	}
	args, ok := action.Field(6).Type().Underlying().(*types.Array)
	return ok && args.Len() == 18 && types.Identical(args.Elem(), types.Typ[types.Uint32]) &&
		action.Tag(6) == ""
}

func validateCoroHostPullRuntimeFunctionV1(name string, fn *ssa.Function) (bool, error) {
	switch name {
	case coroHostNextActionSymbolV1, coroHostProfileSymbolV1, coroHostNextDeadlineSymbolV1,
		coroHostPublishTimeSymbolV1, coroHostPublishWallTimeSymbolV1, coroHostAckCancelSymbolV1, coroHostContinueSliceSymbolV1,
		coroHostNextOperationSymbolV1, coroHostCompleteOperationSymbolV1:
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
	case coroHostPublishWallTimeSymbolV1:
		if allUint32Params(3) && oneResult(boolType) {
			return true, nil
		}
		return true, fmt.Errorf("coroutine host publish-wall-time ABI %q must have exact func(uint32, uint32, uint32) bool signature", name)
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
	case coroHostNextOperationSymbolV1:
		if sig.Params().Len() == 1 &&
			validCoroHostOperationActionPointerV1(sig.Params().At(0).Type()) &&
			oneResult(uint32Type) {
			return true, nil
		}
		return true, fmt.Errorf("coroutine host next-operation ABI %q must have exact func(*host-operation-v1) uint32 signature", name)
	case coroHostCompleteOperationSymbolV1:
		if allUint32Params(10) && oneResult(uint32Type) {
			return true, nil
		}
		return true, fmt.Errorf("coroutine host complete-operation ABI %q must have exact func(10 x uint32) uint32 signature", name)
	default:
		panic("unreachable host-pull runtime ABI validator")
	}
}

// requiredCoroProgramRuntimePlan returns the Go bodies referenced only by
// compiler-generated entry/coroutine IR and their exact static call closure.
// They are not visible from the application's source roots. The closure is a
// trusted raw host/scheduler-stack island: CFG loops do not turn its fixed C
// ABI into a coroutine. Exact ordinary C leaves receive a temporary
// compatible-known summary. Compatible may-block leaves retain their managed
// unknown-foreign/blocking summary and are admitted only for an invocation
// proven to belong to this raw-host closure.
// Fallback SSA stubs remain ignored, and ordinary C declarations outside this
// compiler-owned closure stay unknown foreign.
func requiredCoroProgramRuntimePlan(
	ctx *context,
) (coro.Roots, map[*ssa.Function]struct{}, []requiredCoroDirectPlainCallArgument, map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate, error) {
	return requiredCoroProgramRuntimePlanWithLibrary(ctx, nil)
}

func requiredCoroProgramRuntimePlanWithLibrary(
	ctx *context,
	importedForeign map[*ssa.Function]coro.LibraryEffectForeignCallable,
) (coro.Roots, map[*ssa.Function]struct{}, []requiredCoroDirectPlainCallArgument, map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate, error) {
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
	if ctx.prog != nil && ctx.prog.NeedsLogicalLocalContext() {
		// defineEntryFunction emits these calls after a successful coroutine
		// plan commits logical locality. They have no source SSA instruction,
		// so retain their exact direct-plain runtime bodies here rather than
		// depending on an unrelated //export wrapper to make them reachable.
		names = append(names, "EnterLocalContext", "LeaveLocalContext")
	}
	names = append(names,
		coroFrameAllocatorBootstrapSymbolV1,
		coroProgramBeginSymbolV1,
	)
	if nativeCoroDoorbellRuntimeABI(ctx.buildConf) || hostCoroPullRuntimeABI(ctx.buildConf) {
		names = append(names, coroProgramRunSliceSymbolV2, coroProgramContinueSliceSymbolV2)
		if nativeCoroDoorbellRuntimeABI(ctx.buildConf) {
			names = append(names, coroProgramReportPanicSymbolV1)
		}
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
		names = append(names,
			coroNativeFleetOwnerSymbolV2,
			coroForeignReentryAcquireSymbolV1,
			coroForeignReentryRunSymbolV1,
			coroForeignReentryFailureSymbolV1,
			coroSameMForeignCallSymbolV1,
		)
	}
	if hostCoroPullRuntimeABI(ctx.buildConf) {
		names = append(names,
			coroHostNextActionSymbolV1,
			coroHostProfileSymbolV1,
			coroHostNextDeadlineSymbolV1,
			coroHostPublishTimeSymbolV1,
			coroHostPublishWallTimeSymbolV1,
			coroHostAckCancelSymbolV1,
			coroHostContinueSliceSymbolV1,
			coroHostNextOperationSymbolV1,
			coroHostCompleteOperationSymbolV1,
			coroHostOperationParkSymbolV1,
			coroHostOperationResumeSymbolV1,
		)
	}
	if coroTimerRuntimeABI(ctx.buildConf) {
		names = append(names,
			coroTimerParkSymbolV2,
			coroTimerParkControlledSymbolV2,
			coroTimerResumeSymbolV2,
			coroTimerRequestControlledSymbolV2,
			coroKeyedParkSymbolV2,
			coroKeyedResumeSymbolV2,
			coroSemaphorePrepareOrAbortSymbolV2,
			coroSemaphoreReleaseOrAbortSymbolV2,
			coroNotifyPrepareOrAbortSymbolV2,
			coroNotifyOneOrAbortSymbolV2,
			coroNotifyAllOrAbortSymbolV2,
		)
	}
	if nativeCoroTimerRuntimeABI(ctx.buildConf) {
		names = append(names,
			coroPollParkSymbolV2,
			coroPollResumeSymbolV2,
			coroPollUpdateDeadlineOrAbortSymbolV1,
			coroPollPostClosingOrAbortSymbolV1,
		)
	}
	names = append(names,
		"__llgo_coro_frame_alloc_v1",
		"__llgo_coro_frame_publish_v1",
		"__llgo_coro_frame_publish_v3",
		"__llgo_coro_await_prepare_v1",
		"__llgo_coro_preempt_poll_v1",
		"__llgo_coro_yield_prepare_v1",
		coroRunDecisionTakeSymbolV1,
		coroRunDecisionTakeZeroSymbolV1,
		"__llgo_coro_complete_prepare_v1",
		"__llgo_coro_frame_free_v1",
		"__llgo_coro_await_prepare_inline_v4",
		"__llgo_coro_await_inline_destroy_consume_v4",
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
		"CoroChanTryCloseTask",
		"CoroChanSelectTry",
		"CoroChanSelectPark",
		"CoroChanSelectResume",
		coroChanSendTryParkSymbolV2,
		coroChanRecvTryParkSymbolV2,
		coroChanResumeSymbolV2,
		"__llgo_coro_fault_prepare_v1",
		"__llgo_coro_fault_prepare_v2",
	)
	names = append(names,
		"__llgo_coro_panic_prepare_v1",
		coroPanicTraceReplaceSymbolV1,
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
				!types.Identical(sig.Params().At(4).Type(), types.Typ[types.Uint64]) ||
				!types.Identical(sig.Params().At(5).Type(), types.Typ[types.Uintptr]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("parameterized coroutine fault prepare ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32, uint64, uintptr) signature", name)
			}
		}
		if name == "__llgo_coro_fault_payload_v2" {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 5 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uint64]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.Uintptr]) ||
				!types.Identical(sig.Params().At(3).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(4).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("parameterized coroutine fault payload ABI %q must have exact func(uint32, uint64, uintptr, unsafe.Pointer, unsafe.Pointer) signature", name)
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
				sig.Params().Len() == 16 && sig.Results().Len() == 1 &&
				types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) &&
				types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uintptr]) &&
				types.Identical(sig.Params().At(2).Type(), types.Typ[types.Uintptr]) &&
				types.Identical(sig.Params().At(3).Type(), types.Typ[types.Uint32]) &&
				types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32])
			for index := 4; valid && index < 13; index++ {
				valid = types.Identical(sig.Params().At(index).Type(), types.Typ[types.Uintptr])
			}
			wordPointer := types.NewPointer(types.Typ[types.Uintptr])
			for index := 13; valid && index < 16; index++ {
				valid = types.Identical(sig.Params().At(index).Type(), wordPointer)
			}
			if !valid || typeParamLen(sig.TypeParams()) != 0 ||
				typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine locked-thread foreign ABI %q must have exact func(unsafe.Pointer, uintptr, uintptr, uint32, [9]uintptr, *uintptr, *uintptr, *uintptr) uint32 signature",
					name,
				)
			}
		}
		if name == coroForeignReentryAcquireSymbolV1 {
			sig := fn.Signature
			pointerPointer := types.NewPointer(types.Typ[types.UnsafePointer])
			if sig == nil || sig.Recv() != nil || sig.Variadic() ||
				sig.Params().Len() != 1 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), pointerPointer) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 ||
				typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine foreign-reentry acquire ABI %q must have exact func(*unsafe.Pointer) unsafe.Pointer signature",
					name,
				)
			}
		}
		if name == coroForeignReentryRunSymbolV1 {
			sig := fn.Signature
			pointerPointer := types.NewPointer(types.Typ[types.UnsafePointer])
			if sig == nil || sig.Recv() != nil || sig.Variadic() ||
				sig.Params().Len() != 3 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), pointerPointer) ||
				!types.Identical(sig.Params().At(2).Type(), pointerPointer) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 ||
				typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine foreign-reentry run ABI %q must have exact func(unsafe.Pointer, *unsafe.Pointer, *unsafe.Pointer) uint32 signature",
					name,
				)
			}
		}
		if name == coroForeignReentryFailureSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() ||
				sig.Params().Len() != 3 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 ||
				typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine foreign-reentry failure ABI %q must have exact func(uint32, unsafe.Pointer, unsafe.Pointer) signature",
					name,
				)
			}
		}
		if name == coroSameMForeignCallSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() ||
				sig.Params().Len() != 3 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uintptr]) ||
				!types.Identical(sig.Params().At(2).Type(), types.Typ[types.Uintptr]) ||
				typeParamLen(sig.TypeParams()) != 0 ||
				typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine same-M foreign-call ABI %q must have exact func(unsafe.Pointer, uintptr, uintptr) signature",
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
		if name == coroProgramReportPanicSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 1 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine program panic reporter ABI %q must have exact func(unsafe.Pointer) signature", name)
			}
		}
		if name == coroPanicTraceReplaceSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() ||
				sig.Params().Len() != 2 || sig.Results().Len() != 0 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.UnsafePointer]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
				typeParamLen(sig.TypeParams()) != 0 ||
				typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine panic trace replacement ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer) signature",
					name,
				)
			}
		}
		if name == coroNativeWorkerCompleteSymbolV1 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 8 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Params().At(1).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine native worker completion %q must have exact func(uint32, uint32, [6]uintptr) uint32 signature", name)
			}
			for parameter := 2; parameter < sig.Params().Len(); parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.Uintptr]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine native worker completion %q must have exact func(uint32, uint32, [6]uintptr) uint32 signature", name)
				}
			}
		}
		if name == coroNativeFleetOwnerSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 1 ||
				sig.Results().Len() != 1 ||
				!types.Identical(sig.Params().At(0).Type(), types.Typ[types.Uint32]) ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine native fleet owner %q must have exact func(uint32) uint32 signature", name)
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
		if name == coroChanSendTryParkSymbolV2 || name == coroChanRecvTryParkSymbolV2 {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 9 || sig.Results().Len() != 1 ||
				!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Uint32]) ||
				typeParamLen(sig.TypeParams()) != 0 || typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf("coroutine channel try-or-park ABI %q must have exact func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr, uint32, uint32) uint32 signature", name)
			}
			for parameter := 0; parameter < 6; parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.UnsafePointer]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine channel try-or-park ABI %q must use unsafe.Pointer for parameter %d", name, parameter)
				}
			}
			if !types.Identical(sig.Params().At(6).Type(), types.Typ[types.Uintptr]) {
				return nil, nil, nil, nil, fmt.Errorf("coroutine channel try-or-park ABI %q must use uintptr element size", name)
			}
			for parameter := 7; parameter < 9; parameter++ {
				if !types.Identical(sig.Params().At(parameter).Type(), types.Typ[types.Uint32]) {
					return nil, nil, nil, nil, fmt.Errorf("coroutine channel try-or-park ABI %q must use uint32 for parameter %d", name, parameter)
				}
			}
		}
		if name == coroChanResumeSymbolV2 {
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
		if name == "EnterLocalContext" {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() ||
				sig.Params().Len() != 1 || sig.Results().Len() != 1 ||
				!coroRuntimeLocalContextPointer(sig.Params().At(0).Type()) ||
				!types.Identical(sig.Results().At(0).Type(), sig.Params().At(0).Type()) ||
				typeParamLen(sig.TypeParams()) != 0 ||
				typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine program local-context entry ABI %q must have exact func(*LocalContext) *LocalContext signature",
					name,
				)
			}
		}
		if name == "LeaveLocalContext" {
			sig := fn.Signature
			if sig == nil || sig.Recv() != nil || sig.Variadic() ||
				sig.Params().Len() != 2 || sig.Results().Len() != 0 ||
				!coroRuntimeLocalContextPointer(sig.Params().At(0).Type()) ||
				!types.Identical(sig.Params().At(1).Type(), sig.Params().At(0).Type()) ||
				typeParamLen(sig.TypeParams()) != 0 ||
				typeParamLen(sig.RecvTypeParams()) != 0 || len(fn.FreeVars) != 0 {
				return nil, nil, nil, nil, fmt.Errorf(
					"coroutine program local-context exit ABI %q must have exact func(*LocalContext, *LocalContext) signature",
					name,
				)
			}
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
				requirePlainCallee := true
				calleeGoBody, bodyErr := frozenGoEmittedBody(ctx.coroEmission, callee)
				if bodyErr != nil {
					return nil, nil, nil, nil, fmt.Errorf(
						"classify compiler runtime ABI callee %q in %q: %w",
						callee.Name(), fn.Name(), bodyErr,
					)
				}
				if !calleeGoBody && !provenCoroTLSDirectPlainClosureRoot(ctx, fn, closedDynamic) {
					callable, certified, certificateErr :=
						coroCallableContractWithLibrary(
							ctx.coroEmission.CoroCallableContractCertificate,
							importedForeign, callee,
						)
					if certificateErr != nil {
						return nil, nil, nil, nil, fmt.Errorf(
							"classify raw compiler-runtime foreign call %q in %q: %w",
							callee.Name(), fn.Name(), certificateErr,
						)
					}
					if certified && callable.Scope == coro.CallableContractScopeDeclaration &&
						coroRawPlainDirectForeignContractCompatible(callable.Contract) {
						// This call executes on the already-foreign/raw caller
						// stack. Keep the declaration's conservative may-block
						// policy instead of turning requiredPlain membership into
						// a false ExternalKnown/no-suspend certificate. The exact
						// raw closure validator consumes this same contract after
						// fixed-point planning.
						requirePlainCallee = false
					}
				}
				if requirePlainCallee {
					if _, seen := plain[callee]; !seen {
						queue = append(queue, callee)
					}
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
					closure, ok, err := provenCoroDirectPlainStaticClosureWithLibrary(
						ctx, target, closedDynamic, importedForeign,
					)
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

func coroRuntimeLocalContextPointer(typ types.Type) bool {
	pointer, ok := types.Unalias(typ).(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Name() != "LocalContext" ||
		named.Obj().Pkg() == nil {
		return false
	}
	return llssa.PathOf(named.Obj().Pkg()) == llssa.PkgRuntime
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
// callback-free exact C declaration—or to an independently //llgo:type C
// parameter—is a raw synchronous entry, never a Go descriptor. An exact
// managed-callback declaration is deliberately excluded: ordinary Go
// reference/value-flow analysis must retain and color that target, and the
// physical ForeignReentry recipe later freezes its typed C adapter. The exact
// raw target closure is independently revalidated after fixed-point planning
// as NoSuspend/DirectPlain.
func requiredCoroDirectPlainCallArguments(
	ctx *context,
	closedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate,
) ([]requiredCoroDirectPlainCallArgument, map[*ssa.Function]struct{}, error) {
	return requiredCoroDirectPlainCallArgumentsWithLibrary(ctx, closedDynamic, nil)
}

func requiredCoroDirectPlainCallArgumentsWithLibrary(
	ctx *context,
	closedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate,
	importedForeign map[*ssa.Function]coro.LibraryEffectForeignCallable,
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
				rawCDeclaration := false
				managedReentryDeclaration := false
				if callee, resolved := ctx.coroEmission.Resolve(call.Common().StaticCallee()); resolved && callee != nil {
					background, classified, backgroundErr := ctx.coroEmission.FunctionBackground(callee)
					if backgroundErr != nil {
						return nil, nil, fmt.Errorf(
							"classify static callback declaration %q in %q: %w",
							callee.Name(), function.Name(), backgroundErr,
						)
					}
					rawCDeclaration = classified && background == llssa.InC
					if rawCDeclaration {
						callable, certified, certificateErr :=
							coroCallableContractWithLibrary(
								ctx.coroEmission.CoroCallableContractCertificate,
								importedForeign, callee,
							)
						if certificateErr != nil {
							return nil, nil, fmt.Errorf(
								"classify static callback contract %q in %q: %w",
								callee.Name(), function.Name(), certificateErr,
							)
						}
						managedReentryDeclaration = certified &&
							callable.Scope == coro.CallableContractScopeDeclaration &&
							callable.Contract.Reentry == coro.ReentryManagedCallback
					}
				}
				for argument, value := range call.Common().Args {
					parameter, ok := staticCallArgumentParameterType(call, argument)
					if !ok || !rawCDeclaration && ctx.prog.TypeBackground(parameter) != llssa.InC {
						continue
					}
					if _, signature := types.Unalias(parameter).Underlying().(*types.Signature); !signature {
						continue
					}
					if managedReentryDeclaration {
						continue
					}
					target, ok := exactCoroStaticFunctionValue(ctx, value)
					if !ok {
						continue
					}
					closure, proved, err := provenCoroDirectPlainStaticClosureWithLibrary(
						ctx, target, closedDynamic, importedForeign,
					)
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
// a frontend-owned legacy noblock/sync certificate or a generic direct
// executor-safe contract. A conservative non-reentrant may-block callable
// contract is also valid without entering requiredPlain: this raw callback
// already executes on its foreign caller's native stack, so an inline blocking
// leaf cannot occupy a managed executor. The final raw closure validator
// independently joins and revalidates that exact contract.
// Dynamic managed calls, go/defer, other bodyless leaves, captured closures,
// and unresolved aliases remain on the ordinary Dispatch path. Effect and
// representation are independently checked after fixed-point analysis; this
// prefilter only establishes that it is sound to seed the candidate's exact raw
// host/scheduler-stack island.
func provenCoroDirectPlainStaticClosure(
	ctx *context,
	target *ssa.Function,
	closedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate,
) ([]*ssa.Function, bool, error) {
	return provenCoroDirectPlainStaticClosureWithLibrary(
		ctx, target, closedDynamic, nil,
	)
}

func provenCoroDirectPlainStaticClosureWithLibrary(
	ctx *context,
	target *ssa.Function,
	closedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate,
	importedForeign map[*ssa.Function]coro.LibraryEffectForeignCallable,
) ([]*ssa.Function, bool, error) {
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
					callable, callableCertified, err :=
						coroCallableContractWithLibrary(
							ctx.coroEmission.CoroCallableContractCertificate,
							importedForeign, callee,
						)
					if err != nil {
						return nil, false, err
					}
					if callableCertified && callable.Scope == coro.CallableContractScopeDeclaration &&
						coroRawPlainDirectForeignContractCompatible(callable.Contract) {
						// Keep the declaration's conservative managed policy.
						// Only this raw callback variant invokes it inline.
						continue
					}
					directExecutor := callableCertified &&
						callable.Scope == coro.CallableContractScopeDeclaration &&
						coro.CallableContractDirectExecutorCompatible(
							callable.Contract,
						)
					if !tlsCallback && !noBlock && !synchronous &&
						!directExecutor {
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

func coroCallableContractWithLibrary(
	local func(*ssa.Function) (cl.CoroCallableContractCertificate, bool, error),
	imported map[*ssa.Function]coro.LibraryEffectForeignCallable,
	function *ssa.Function,
) (cl.CoroCallableContractCertificate, bool, error) {
	if fact, ok := imported[function]; ok {
		if !fact.HasContract {
			return cl.CoroCallableContractCertificate{}, false, nil
		}
		return fact.Contract, true, nil
	}
	if local == nil {
		return cl.CoroCallableContractCertificate{}, false, fmt.Errorf(
			"missing local callable contract lookup",
		)
	}
	return local(function)
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
		LogicalLocality:        true,
		CoroForeignExecutorLeafProofs: ctx.coroRawGlobalSymbols.
			frozenForeignExecutorLeafProofs(),
		GOROOT: ctx.goRoot,
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
		GOOS:       conf.Goos,
		GOARCH:     conf.Goarch,
		Target:     conf.Target,
		LLVMTarget: export.LLVMTarget,
		OptLevel:   conf.OptLevel,
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

// DefaultBuildTags returns the build tags LLGo always enables for a target.
func DefaultBuildTags(goarch, target string) string {
	return defaultBuildTags(goarch, target)
}

func defaultBuildTags(goarch, target string) string {
	tags := "llgo,math_big_pure_go,purego"
	// Raw GOOS/GOARCH wasm builds do not have a target configuration that
	// selects a collector. BDWGC is not available in either wasm host, so use
	// the supported collector-free runtime unless a named target supplies its
	// own runtime configuration.
	if goarch == "wasm" && target == "" {
		tags += ",nogc"
	}
	return tags
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
		if pkg.Types != nil && pkg.Types.Name() == "main" {
			pkg.Types.SetName("main.test")
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
	conf           *packages.Config
	progSSA        *ssa.Program
	prog           llssa.Program
	goRoot         string
	dedup          packages.Deduper
	patches        cl.Patches
	callerTracking *cl.CallerTracking
	built          map[string]none
	fingerprinting map[string]bool
	cacheDisabled  map[string]none
	initial        []*packages.Package
	pkgs           map[*packages.Package]Package // cache for lookup
	pkgByID        map[string]Package            // cache for lookup by pkg.ID
	mode           Mode
	nLibdir        int32
	output         bool
	passOpt        bool

	buildConf       *Config
	crossCompile    crosscompile.Export
	commands        commandEnv
	frontendOptions cl.Options

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
	// coroProgramCapabilities is the closed-world projection of optional
	// physical runtime services. It is frozen by cl preflight before bootstrap
	// selection and survives only as hashed entry-module flags.
	coroProgramCapabilities coro.ProgramCapabilities
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

	// stagedBitcodeFiles owns temporary package bitcode until the isolated
	// backend phase consumes it. It is also the error-path cleanup authority.
	stagedBitcodeFiles    map[string]none
	stagedCacheAuthorized bool
	stagedMainEntries     map[string]*stagedMainEntry
	stagedBackendDetached bool
	stagedTargetTriple    string
	stagedDataLayout      string
	stagedFuncInfoSites   bool
	stagedFuncInfoMeta    bool
	stagedPointerSize     int
	stagedMachOSites      bool
}

type stagedMainEntry struct {
	pkgPath      string
	exportFile   string
	bitcode      string
	object       string
	pclnExternal *pclnmap.Data
}

// closePackageMetas releases metadata mappings owned by this build. Metadata
// remains available to hooks and whole-program consumers until Do returns.
func (c *context) closePackageMetas() {
	for _, pkg := range c.pkgs {
		if pkg.Meta == nil {
			continue
		}
		_ = pkg.Meta.Close()
		pkg.Meta = nil
	}
}

func (c *context) cleanupStagedBitcodeFiles() {
	for file := range c.stagedBitcodeFiles {
		_ = os.Remove(file)
	}
	c.stagedBitcodeFiles = nil
}

// backendSession owns all LLVM state used to lower one package. The Program
// shares only the coordinator's already-prepared Go metadata.
type backendSession struct {
	prog        llssa.Program
	transformer *cabi.Transformer
}

func (c *context) newBackendSession() backendSession {
	prog := c.prog.NewBackendProgram()
	return backendSession{
		prog: prog,
		transformer: cabi.NewTransformer(
			prog,
			c.crossCompile.LLVMTarget,
			c.crossCompile.TargetABI,
			c.buildConf.AbiMode,
			!shouldEmitDebugInfo(c.buildConf, &c.crossCompile),
		),
	}
}

// preloadPatchedPackageSyntax prepares the effective types.Package used by
// patched lowering. Normal and alternate packages are already covered by the
// packages loader's preload callback, but patch.Types has a distinct identity.
func preloadPatchedPackageSyntax(prog llssa.Program, patches cl.Patches, dedup packages.Deduper, options cl.Options) error {
	paths := make([]string, 0, len(patches))
	for pkgPath := range patches {
		paths = append(paths, pkgPath)
	}
	slices.Sort(paths)
	for _, pkgPath := range paths {
		patch := patches[pkgPath]
		alt := dedup.Check(altPkgPathPrefix + pkgPath)
		if alt == nil || len(alt.Syntax) == 0 || patch.Types == nil {
			continue
		}
		fset := alt.Fset
		files := slices.Clone(alt.Syntax)
		if original := dedup.Check(pkgPath); original != nil {
			fset = original.Fset
			files = append(slices.Clone(original.Syntax), files...)
		}
		if err := cl.ParsePkgSyntaxWithOptions(prog, fset, patch.Types, files, options); err != nil {
			return err
		}
	}
	return nil
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
	cmd.Dir = c.commands.dir
	cmd.Env = slices.Clone(c.commands.environ)
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
	cmd.Dir = c.commands.dir
	cmd.Env = slices.Clone(c.commands.environ)
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

// normalizeToArchive creates an archive from file and memory members and sets ArchiveFile.
// This ensures the link step always consumes .a archives regardless of cache state.
func normalizeToArchive(ctx *context, aPkg *aPackage, verbose bool) error {
	if len(aPkg.ObjFiles) == 0 && len(aPkg.ObjBuffers) == 0 {
		return nil
	}
	defer aPkg.disposeArchiveBuffers()

	archiveFile, err := os.CreateTemp("", "pkg-*.a")
	if err != nil {
		return fmt.Errorf("create temp archive: %w", err)
	}
	archiveFile.Close()
	archivePath := archiveFile.Name()

	if err := ctx.createPackageArchiveFile(archivePath, aPkg, verbose); err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("create archive for %s: %w", aPkg.PkgPath, err)
	}

	aPkg.ObjFiles = nil
	aPkg.ArchiveFile = archivePath
	return nil
}

func buildAllPkgs(ctx *context, pkgs []*aPackage, verbose bool) ([]*aPackage, error) {
	// Split packages into runtime tree vs others so we can defer runtime build.
	var runtimePkgs []*packageBuildTask
	var normalPkgs []*packageBuildTask
	for _, p := range pkgs {
		task := newPackageBuildTask(p)
		if task.isRuntime() {
			runtimePkgs = append(runtimePkgs, task)
		} else {
			normalPkgs = append(normalPkgs, task)
		}
	}

	var needRuntime, needPyInit bool

	// Build non-runtime packages first, so we know whether runtime is actually needed.
	for _, task := range normalPkgs {
		result, err := buildOnePackage(ctx, task, verbose)
		if err != nil {
			return nil, err
		}
		needRuntime = needRuntime || result.needRuntime
		needPyInit = needPyInit || result.needPyInit
	}

	// Active coroutine planning freezes and validates one exact compilation-wide
	// universe before LLVM codegen. Its prepared universe includes the runtime
	// tree, so emit that tree as well even when target lowering would otherwise
	// discover no runtime dependency. Report-only planning preserves the legacy
	// lazy-runtime behavior and package-cache/IR output.
	if shouldBuildRuntimePackages(ctx.buildConf, needRuntime, needPyInit) {
		for _, task := range runtimePkgs {
			if _, err := buildOnePackage(ctx, task, verbose); err != nil {
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

// buildOnePackage is the serial package pipeline. Its explicit stages are the
// contract used by later package workers; this commit deliberately preserves
// serial LLVM execution.
func buildOnePackage(ctx *context, task *packageBuildTask, verbose bool) (packageBuildResult, error) {
	if err := prePackageBuild(ctx, task, verbose); err != nil || task.skip {
		return packageBuildResultFor(task), err
	}
	if err := executePackageBuild(ctx, task, verbose); err != nil {
		return packageBuildResultFor(task), err
	}
	return finalizePackageBuild(ctx, task, verbose)
}

// prePackageBuild performs classification, fingerprinting, and cache
// lookup without creating or transforming an LLVM module.
func prePackageBuild(ctx *context, task *packageBuildTask, verbose bool) error {
	aPkg := task.pkg
	pkg := aPkg.Package
	if _, ok := ctx.built[pkg.ID]; ok {
		task.skip = true
		return nil
	}
	ctx.built[pkg.ID] = none{}
	if task.isDeclOnly() {
		pkg.ExportFile = ""
		task.skip = true
		return nil
	}
	if task.isLinkOnly() && !task.hasSource() {
		pkg.ExportFile = ""
		if task.kind == cl.PkgLinkExtern {
			appendExternalLinkArgs(ctx, aPkg, task.kindParam)
		}
		task.skip = true
		return nil
	}
	if err := ctx.collectFingerprint(aPkg); err != nil {
		return err
	}
	usePackageCache := ctx.canUsePackageCache()
	if usePackageCache {
		ctx.tryLoadFromCache(aPkg)
	}
	if verbose {
		status := "DISABLED (coroutine entry resolution)"
		if usePackageCache && aPkg.CacheHit {
			status = "HIT"
		} else if usePackageCache {
			status = "MISS"
		}
		fmt.Fprintf(os.Stderr, "CACHE %s: %s\n", status, pkg.PkgPath)
	}
	return nil
}

// executePackageBuild creates the package module and runs its LLVM backend.
func executePackageBuild(ctx *context, task *packageBuildTask, verbose bool) error {
	aPkg := task.pkg
	if err := buildPkg(ctx, aPkg, verbose); err != nil {
		return err
	}
	if task.needsRuntimeSignals() && !aPkg.CacheHit && aPkg.LPkg != nil {
		aPkg.setNeedRuntimeOrPyInit(aPkg.LPkg.NeedRuntime, aPkg.LPkg.NeedPyInit)
	}
	return nil
}

// finalizePackageBuild publishes the archive and cache metadata. Cache hits
// already carry both and therefore require no publication.
func finalizePackageBuild(ctx *context, task *packageBuildTask, verbose bool) (packageBuildResult, error) {
	aPkg := task.pkg
	if aPkg.CacheHit {
		return packageBuildResultFor(task), nil
	}
	if task.kind == cl.PkgLinkExtern {
		appendExternalLinkArgs(ctx, aPkg, task.kindParam)
	}
	usePackageCache := ctx.canUsePackageCache()
	if aPkg.StagedBitcode != "" {
		aPkg.PendingCacheSave = usePackageCache
	} else {
		if err := normalizeToArchive(ctx, aPkg, verbose); err != nil {
			return packageBuildResultFor(task), err
		}
		if usePackageCache {
			if err := ctx.saveToCache(aPkg); err != nil && verbose {
				fmt.Fprintf(os.Stderr, "warning: failed to save cache for %s: %v\n", aPkg.PkgPath, err)
			}
		}
	}
	if shouldStageNativeExecutableBackend(ctx) {
		releaseBuiltPackageSource(aPkg)
	}
	return packageBuildResultFor(task), nil
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

func linkedPackagesForMain(ctx *context, pkg *packages.Package, pkgs []*aPackage) []Package {
	allPkgs := []*packages.Package{pkg}
	for _, v := range pkgs {
		if v.PkgPath != pkg.PkgPath && v.Name == "main" {
			continue
		}
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
	return linkedOrder
}

type mainEntryRequirements struct {
	needRuntime   bool
	needPyInit    bool
	linkRuntime   bool
	needAbiInit   int
	methodByIndex map[int]none
	methodByName  map[string]none
}

func collectMainEntryRequirements(ctx *context, linkedOrder []Package) (mainEntryRequirements, error) {
	req := mainEntryRequirements{
		methodByIndex: make(map[int]none),
		methodByName:  make(map[string]none),
	}
	for _, pkg := range linkedOrder {
		if pkg == nil || isRuntimePkg(pkg.PkgPath) {
			continue
		}
		needRuntime, needPyInit := pkg.isNeedRuntimeOrPyInit()
		req.needRuntime = req.needRuntime || needRuntime
		req.needPyInit = req.needPyInit || needPyInit
		req.needAbiInit |= packageNeedAbiInit(pkg)
		for _, index := range packageMethodIndexes(pkg) {
			req.methodByIndex[index] = none{}
		}
		for _, name := range packageMethodNames(pkg) {
			req.methodByName[name] = none{}
		}
	}
	req.needRuntime, req.linkRuntime = runtimeLinkRequirements(ctx.buildConf, req.needRuntime, req.needPyInit)
	if err := validateCoroHostPullEntryConfig(ctx.buildConf, req.needPyInit); err != nil {
		return mainEntryRequirements{}, err
	}
	return req, nil
}

func generateMainEntryPackage(ctx *context, pkg *packages.Package, linkedOrder []Package, req mainEntryRequirements) (Package, error) {
	var funcInfo []funcInfoRecord
	var pcLineInfo []pcLineRecord
	if ctx.buildConf.PCLNMode != PCLNNone {
		funcInfo = prepareFuncInfoTableRecords(collectFuncInfo(linkedOrder), nil)
		pcLineInfo = collectPCLineInfo(linkedOrder)
	}
	coroRootAnchors, err := collectLinkedCoroRootAnchors(linkedOrder)
	if err != nil {
		return nil, err
	}
	packageInits, err := linkedPackageInitNames(pkg, linkedOrder)
	if err != nil {
		return nil, err
	}
	var coroBootstrap *coroProgramBootstrapV1
	if ctx.buildConf.BuildMode == BuildModeExe {
		coroBootstrap = ctx.coroProgramBootstraps[pkg.ID]
		if coroBootstrap == nil {
			return nil, fmt.Errorf("coroutine program bootstrap: no pre-codegen table was frozen for linked package %q", pkg.ID)
		}
		coroBootstrap, err = bindCoroProgramBootstrapV2(coroBootstrap, linkedOrder)
		if err != nil {
			return nil, fmt.Errorf("bind coroutine program bootstrap: %w", err)
		}
	}
	coroManifestHash, err := coroProgramManifestHashV1(ctx, coroRootAnchors, coroBootstrap)
	if err != nil {
		return nil, err
	}
	entryPkg := genMainModule(ctx, llssa.PkgRuntime, pkg, &genConfig{
		rtInit:           req.needRuntime,
		pyInit:           req.needPyInit,
		abiInit:          req.needAbiInit,
		coroRootAnchors:  coroRootAnchors,
		coroManifestHash: coroManifestHash,
		coroBootstrap:    coroBootstrap,
		packageInits:     packageInits,
		methodByIndex:    req.methodByIndex,
		methodByName:     req.methodByName,
		abiSymbols:       linkedModuleGlobals(linkedOrder),
		funcInfo:         funcInfo,
		pcLineInfo:       pcLineInfo,
	})
	// Native coroutine builds stage the entry module before releasing the
	// frontend and LLVM context. Apply method-liveness overrides at this shared
	// generation boundary so staged and direct backends both freeze the same
	// entry bitcode before it is exported exactly once.
	if ctx.buildConf.deadcodeDropEnabled() {
		if err := applyDeadcodeDropOverrides(ctx, linkedOrder, entryPkg, req.needRuntime, ctx.buildConf.Verbose); err != nil {
			return nil, err
		}
	}
	if err := lowerCoroControlWrappers(ctx, entryPkg.LPkg); err != nil {
		return nil, err
	}
	return entryPkg, nil
}

func stageMainEntryBitcodes(ctx *context, roots []*packages.Package, pkgs []*aPackage) error {
	if !shouldStageNativeExecutableBackend(ctx) {
		return nil
	}
	ctx.stagedMainEntries = make(map[string]*stagedMainEntry)
	for _, root := range roots {
		if !needLink(root, ctx.mode) {
			continue
		}
		linkedOrder := linkedPackagesForMain(ctx, root, pkgs)
		req, err := collectMainEntryRequirements(ctx, linkedOrder)
		if err != nil {
			return err
		}
		ctx.pclnExternal = nil
		entryPkg, err := generateMainEntryPackage(ctx, root, linkedOrder, req)
		if err != nil {
			return err
		}
		if err := stagePackageBitcode(ctx, entryPkg); err != nil {
			return err
		}
		entryPkg.LPkg.Module().Dispose()
		entryPkg.LPkg = nil
		ctx.stagedMainEntries[root.ID] = &stagedMainEntry{
			pkgPath:      "entry_main",
			exportFile:   entryPkg.ExportFile,
			bitcode:      entryPkg.StagedBitcode,
			pclnExternal: ctx.pclnExternal,
		}
	}
	ctx.pclnExternal = nil
	return nil
}

func linkMainPkg(ctx *context, pkg *packages.Package, pkgs []*aPackage, outputPath string, verbose bool) error {
	ctx.pclnExternal = nil
	linkedOrder := linkedPackagesForMain(ctx, pkg, pkgs)
	req, err := collectMainEntryRequirements(ctx, linkedOrder)
	if err != nil {
		return err
	}

	// packages.Visit with a post callback yields dependencies before importers.
	// Reverse that order so static archives are linked after the objects that use them.
	var archiveInputs []string
	var linkArgs []string
	var rtLinkInputs []string
	var rtLinkArgs []string
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
		linkArgs = append(linkArgs, aPkg.LinkArgs...)
		if aPkg.ArchiveFile != "" {
			archiveInputs = append(archiveInputs, aPkg.ArchiveFile)
		}
	}

	// Only link runtime objects when needed (or for host builds where runtime is
	// always required). The child-await requirement above participates through
	// the same NeedRuntime path as ordinary runtime calls.
	if req.linkRuntime {
		linkArgs = append(linkArgs, rtLinkArgs...)
		archiveInputs = append(archiveInputs, rtLinkInputs...)
	}

	// Generate main module file (needed for global variables even in library modes)
	// This is compiled directly to .o and added to linkInputs (not cached)
	// Use a stable synthetic name to avoid confusing it with the real main package in traces/logs.
	var entryObjFile string
	if staged := ctx.stagedMainEntries[pkg.ID]; staged != nil {
		if staged.object == "" {
			return fmt.Errorf("staged main entry for %q has no materialized object", pkg.ID)
		}
		entryObjFile = staged.object
		ctx.pclnExternal = staged.pclnExternal
	} else {
		entryPkg, err := generateMainEntryPackage(ctx, pkg, linkedOrder, req)
		if err != nil {
			return err
		}
		entryObjFile, err = exportObject(ctx, "entry_main", entryPkg.ExportFile, entryPkg.LPkg)
		if err != nil {
			return err
		}
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

func linkedPackageMetas(pkgs []Package) ([]*meta.PackageMeta, error) {
	metas := make([]*meta.PackageMeta, 0, len(pkgs))
	for _, pkg := range pkgs {
		if pkg == nil || pkg.Meta == nil {
			pkgPath := "<nil>"
			if pkg != nil && pkg.Package != nil {
				pkgPath = pkg.PkgPath
			}
			return nil, fmt.Errorf("deadcode drop: linked package %q has no semantic metadata", pkgPath)
		}
		metas = append(metas, pkg.Meta)
	}
	return metas, nil
}

func applyDeadcodeDropOverrides(ctx *context, pkgs []Package, entryPkg Package, needRuntime bool, verbose bool) error {
	metas, err := linkedPackageMetas(pkgs)
	if err != nil {
		return err
	}
	summary, err := meta.NewGlobalSummary(metas)
	if err != nil {
		return err
	}

	roots, err := dceEntryRootCandidates(ctx, pkgs, needRuntime)
	if err != nil {
		return err
	}
	liveSlots := deadcode.Analyze(summary, roots)
	sourceModules, err := dceSourceModules(pkgs)
	if err != nil {
		return err
	}
	dcepass.EmitStrongTypeOverrides(entryPkg.LPkg.Module(), sourceModules, liveSlots, verbose)
	return nil
}

func dceSourceModules(pkgs []Package) ([]gllvm.Module, error) {
	mods := make([]gllvm.Module, 0, len(pkgs))
	for _, pkg := range pkgs {
		if pkg == nil || pkg.LPkg == nil {
			pkgPath := "<nil>"
			if pkg != nil && pkg.Package != nil {
				pkgPath = pkg.PkgPath
			}
			return nil, fmt.Errorf("deadcode drop: linked package %q has no live LLVM module", pkgPath)
		}
		mods = append(mods, pkg.LPkg.Module())
	}
	return mods, nil
}

func dceEntryRootCandidates(ctx *context, pkgs []Package, needRuntime bool) ([]string, error) {
	roots := []string{"main.init", "main.main"}
	physical, err := coroDCEEntryRootCandidates(ctx)
	if err != nil {
		return nil, err
	}
	roots = append(roots, physical...)
	// C code can call //export functions without an ordinary edge from a Go
	// root, so their final linker names must seed the analysis explicitly.
	var exports []string
	for _, pkg := range pkgs {
		exports = append(exports, packageExportFunctionNames(pkg)...)
	}
	slices.Sort(exports)
	roots = append(roots, exports...)
	if needRuntime {
		roots = append(roots, llssa.PkgRuntime+".init")
	}
	return roots, nil
}

// coroDCEEntryRootCandidates projects the already-frozen coroutine entry
// capabilities into the physical symbols used as metadata fact owners. SSA
// metadata is collected after physical lowering, so logical roots such as
// main.main cannot reach facts owned by main.main$coro. Raw ABI roots are
// independent: a dual-entry function may need both its managed primary and its
// ordinary base symbol rooted.
func coroDCEEntryRootCandidates(ctx *context) ([]string, error) {
	if ctx == nil || ctx.coroPlan == nil && ctx.coroEmission == nil {
		return nil, nil
	}
	if ctx.coroPlan == nil || ctx.coroEmission == nil {
		return nil, fmt.Errorf("deadcode drop: coroutine roots require a complete frozen plan and emission universe")
	}
	view := ctx.coroEmission.CoroLibraryEffects()
	seen := make(map[string]none)
	var roots []string
	add := func(symbol string) {
		if symbol == "" {
			return
		}
		if _, duplicate := seen[symbol]; duplicate {
			return
		}
		seen[symbol] = none{}
		roots = append(roots, symbol)
	}
	for _, root := range ctx.coroPlan.Roots() {
		if root.Function == nil {
			return nil, fmt.Errorf("deadcode drop: coroutine root %q has no SSA function", root.ID)
		}
		plan, ok := ctx.coroPlan.FunctionPlan(root.Function)
		if !ok || plan.ID != root.ID {
			return nil, fmt.Errorf("deadcode drop: coroutine root %q has no matching frozen function plan", root.ID)
		}
		if root.ManagedDemand != coro.NoDemand {
			switch plan.Emission {
			case coro.EmitPlain, coro.EmitCoroutine, coro.EmitOutcomePlain:
				symbol, err := view.FunctionEmittedPrimarySymbol(root.Function, plan.Emission)
				if err != nil {
					return nil, fmt.Errorf("deadcode drop: resolve managed coroutine root %q: %w", root.ID, err)
				}
				add(symbol)
			case coro.EmitNone, coro.EmitExternal:
				// No local metadata owner exists for an omitted or imported body.
			case coro.EmitRawPlain:
				return nil, fmt.Errorf("deadcode drop: managed coroutine root %q has raw-only emission", root.ID)
			default:
				return nil, fmt.Errorf("deadcode drop: coroutine root %q has unknown emission %d", root.ID, uint8(plan.Emission))
			}
		}
		if root.RawPlainDemand {
			symbol, err := view.FunctionBaseSymbol(root.Function)
			if err != nil {
				return nil, fmt.Errorf("deadcode drop: resolve raw coroutine root %q: %w", root.ID, err)
			}
			add(symbol)
		}
	}
	slices.Sort(roots)
	return roots, nil
}

func linkedModuleGlobals(pkgs []Package) map[string]none {
	if len(pkgs) == 0 {
		return nil
	}
	seen := make(map[string]none)
	for _, pkg := range pkgs {
		for _, name := range packageDefinedGlobals(pkg) {
			seen[name] = none{}
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
	if dir := filepath.Dir(app); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output directory %s: %w", dir, err)
		}
	}
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

// cSharedExportArgs keeps //export functions and synthetic test entry points as
// shared-library link roots. They live in package archives and otherwise remain
// unreferenced, so the linker can omit both their object files and symbols.
func cSharedExportArgs(ctx *context, pkgs []*aPackage) []string {
	if ctx == nil || ctx.buildConf == nil || ctx.buildConf.BuildMode != BuildModeCShared {
		return nil
	}
	exports := make(map[string]none)
	for _, pkg := range pkgs {
		for _, name := range packageExportFunctionNames(pkg) {
			if name != "" {
				exports[name] = none{}
			}
		}
		if ctx.mode == ModeTest && pkg.Package != nil && pkg.Name == "main" && strings.HasSuffix(pkg.PkgPath, ".test") {
			exports[pkg.PkgPath+".init"] = none{}
			exports[pkg.PkgPath+".main"] = none{}
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
	cmd := c.commands.configure(exec.Command(arCmd, "-M"))
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
	cmd := c.commands.configure(exec.Command(arCmd, args...))
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
	externs, err := preparePackageModule(ctx, aPkg, verbose)
	if err != nil || aPkg.CacheHit || aPkg.LPkg == nil {
		return err
	}
	return compilePackageModule(ctx, aPkg, externs, verbose)
}

// preparePackageModule runs the frontend and creates the package LLVM module.
func preparePackageModule(ctx *context, aPkg *aPackage, verbose bool) ([]string, error) {
	pkg := aPkg.Package
	pkgPath := pkg.PkgPath
	if debugBuild || verbose {
		fmt.Fprintln(os.Stderr, pkgPath)
	} else {
		printCompiledPackage(ctx.buildConf, aPkg)
	}
	if llruntime.SkipToBuild(pkgPath) {
		pkg.ExportFile = ""
		return nil, nil
	}
	var syntax = pkg.Syntax
	if altPkg := aPkg.AltPkg; altPkg != nil {
		syntax = append(syntax, altPkg.Syntax...)
	}
	showDetail := verbose && pkgExists(ctx.initial, pkg)
	needMeta := !aPkg.CacheHit && ctx.buildConf.packageMetaEnabled()
	if showDetail {
		fmt.Fprintf(os.Stderr, "==> Compile %s\n", pkgPath)
	}
	embedMap, err := goembed.LoadDirectives(ctx.conf.Fset, syntax)
	if err != nil {
		return nil, fmt.Errorf("load go:embed directives for %s failed: %w", pkgPath, err)
	}
	ret, externs, err := cl.NewPackageExWithEmbedOptions(ctx.prog, ctx.callerTracking, ctx.patches, aPkg.rewriteVars, aPkg.SSA, syntax, embedMap, cl.PackageOptions{
		Compilation:        ctx.clCompilation,
		CacheHit:           aPkg.CacheHit,
		MetaCollect:        needMeta,
		FrontendOptions:    ctx.frontendOptions,
		FrontendOptionsSet: true,
	})
	if err != nil {
		return nil, fmt.Errorf("compile package %s: %w", pkgPath, err)
	}

	aPkg.LPkg = ret
	coroLibraryEffectRecords, err := cl.CoroLibraryEffectSummaryRecords(ret)
	if err != nil {
		return nil, fmt.Errorf("collect coroutine library effect records for %s: %w", pkgPath, err)
	}
	aPkg.CoroLibraryEffectRecords = coroLibraryEffectRecords
	emittedCoroRootAnchor := ret.CoroRootPackageAnchor()
	if aPkg.CacheHit {
		if aPkg.CoroRootAnchorV1 != emittedCoroRootAnchor {
			return nil, fmt.Errorf(
				"cached package %s coroutine root anchor %q does not match frontend registration %q",
				pkgPath, aPkg.CoroRootAnchorV1, emittedCoroRootAnchor,
			)
		}
	} else {
		aPkg.CoroRootAnchorV1 = emittedCoroRootAnchor
		aPkg.Meta = ret.Meta
	}
	if hook := ctx.buildConf.ModuleHook; hook != nil {
		hook(aPkg)
	}

	// A cache hit reconstructed frontend registrations and link-time metadata;
	// the archived module already owns C ABI transformation, optimization, and
	// object emission, so discard this transient frontend module here.
	if aPkg.CacheHit {
		freezePackageLinkSnapshot(aPkg)
		return nil, nil
	}
	return externs, nil
}

// compilePackageModule applies LLVM transforms and emits package objects.
func compilePackageModule(ctx *context, aPkg *aPackage, externs []string, verbose bool) error {
	pkg := aPkg.Package
	pkgPath := pkg.PkgPath
	ret := aPkg.LPkg

	// The C ABI transformer assumes every call's operand count and physical
	// function type already agree. Verify the frontend module before handing it
	// to LLVM's unchecked C mutation APIs; otherwise one malformed call can turn
	// an out-of-range LLVMGetOperand into a host-process segmentation fault.
	if err := gllvm.VerifyModule(ret.Module(), gllvm.ReturnStatusAction); err != nil {
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
	stageBackend := shouldStageNativeExecutableBackend(ctx)
	wholeProgramCoroLTO := shouldDeferCoroLoweringToFullLTO(ctx)
	// Coroutine splitting is a mandatory correctness pass, not an optimization.
	// In particular, native debug builds intentionally skip the default
	// optimization pipeline, but TargetMachine cannot select unresolved
	// llvm.coro.* operators. ModeGen deliberately retains frontend coroutine IR
	// for golden/LIT inspection and never reaches object emission here.
	if ctx.mode != ModeGen && !stageBackend && !wholeProgramCoroLTO {
		if err := lowerCoroPackageModule(ctx, pkgPath, mod); err != nil {
			return err
		}
	}

	// Run the default LLVM optimization pipeline selected by the requested -O level.
	if ctx.passOpt && !stageBackend && !wholeProgramCoroLTO {
		pbo := gllvm.NewPassBuilderOptions()
		defer pbo.Dispose()
		if err := gllvm.VerifyModule(mod, gllvm.ReturnStatusAction); err != nil {
			var broken []string
			for fn := mod.FirstFunction(); !fn.IsNil(); fn = gllvm.NextFunction(fn) {
				if gllvm.VerifyFunction(fn, gllvm.ReturnStatusAction) != nil {
					broken = append(broken, fn.Name())
				}
			}
			return fmt.Errorf("verify LLVM module for package %s (invalid functions %v): %w", pkgPath, broken, err)
		}
		if err := mod.RunPasses(llvmPassPipeline(ctx.buildConf.OptLevel, ctx.buildConf.ltoMode()), ctx.prog.TargetMachine(), pbo); err != nil {
			return fmt.Errorf("run LLVM passes failed for %v: %w", pkgPath, err)
		}
	}
	if !stageBackend {
		emitFuncInfoEntrySites(ctx, ret)
		if ctx.mode != ModeGen {
			verifyAtomicCost := llssa.VerifyCoroAtomicCostModule
			if ctx.passOpt && !wholeProgramCoroLTO {
				verifyAtomicCost = llssa.VerifyOptimizedCoroAtomicCostModule
			}
			if _, err := verifyAtomicCost(mod); err != nil {
				return fmt.Errorf("verify package %s final atomic-cost certificates: %w", pkgPath, err)
			}
		}
	}
	freezePackageLinkSnapshot(aPkg)
	// ModeGen callers consume the in-memory LLVM module directly. They do not
	// need cgo/link objects or a package archive for a later link step.
	if ctx.mode == ModeGen {
		return nil
	}

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
	// instruction selection. Object-producing modes cross the mandatory
	// lowering boundary either above or in the detached staged backend before
	// entering the common archive path.
	if pkg.ExportFile != "" {
		if shouldStageNativeExecutableBackend(ctx) {
			if err := stagePackageBitcode(ctx, aPkg); err != nil {
				return err
			}
			ret.Module().Dispose()
			aPkg.LPkg = nil
		} else {
			exportFile, exportBuffer, err := exportPackageObject(ctx, pkg.PkgPath, pkg.ExportFile, ret)
			if err != nil {
				return fmt.Errorf("export object of %v failed: %v", pkgPath, err)
			}
			if exportFile != "" {
				aPkg.ObjFiles = append(aPkg.ObjFiles, exportFile)
			} else {
				aPkg.ObjBuffers = append(aPkg.ObjBuffers, exportBuffer)
			}
		}
		if debugBuild || verbose {
			fmt.Fprintf(os.Stderr, "==> Export %s: %s\n", aPkg.PkgPath, pkg.ExportFile)
		}
	}
	return nil
}

const coroPackageLoweringPipeline = "coro-early,cgscc(coro-split),coro-cleanup"

// shouldDeferCoroLoweringToFullLTO preserves presplit coroutine bodies and
// exact static call edges until LLVM owns the complete program. Package-level
// coro-cleanup irreversibly erases the coro.id information required by both
// HALO and LLVM 22's coro_elide_safe/.noalloc protocol; the full-LTO backend's
// mandatory pipeline performs CoroEarly, CoroSplit, annotation elision, and
// CoroCleanup after all package bitcode has been combined.
func shouldDeferCoroLoweringToFullLTO(ctx *context) bool {
	return ctx != nil && ctx.mode != ModeGen && ctx.buildConf != nil &&
		ctx.buildConf.ltoMode() == lto.Full
}

func lowerCoroPackageModule(ctx *context, pkgPath string, mod gllvm.Module) error {
	if ctx == nil || ctx.prog == nil || mod.IsNil() {
		return fmt.Errorf("lower package coroutine IR for %s: missing build context or module", pkgPath)
	}
	return lowerCoroPackageModuleWithProgram(ctx.prog, pkgPath, mod)
}

func lowerCoroPackageModuleWithProgram(prog llssa.Program, pkgPath string, mod gllvm.Module) error {
	if prog == nil || mod.IsNil() {
		return fmt.Errorf("lower package coroutine IR for %s: missing program or module", pkgPath)
	}
	if err := gllvm.VerifyModule(mod, gllvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("verify package %s before coroutine lowering: %w", pkgPath, err)
	}
	if err := validateCoroPackageStaticAllocas(pkgPath, mod); err != nil {
		return err
	}
	if _, err := llssa.VerifyCoroAtomicCostModule(mod); err != nil {
		return fmt.Errorf("verify package %s atomic-cost certificates before coroutine lowering: %w", pkgPath, err)
	}
	options := gllvm.NewPassBuilderOptions()
	defer options.Dispose()
	if err := mod.RunPasses(coroPackageLoweringPipeline, prog.TargetMachine(), options); err != nil {
		return fmt.Errorf("lower package coroutine IR for %s: %w", pkgPath, err)
	}
	llssa.RemoveKeepAliveCallsAfterCoroSplit(mod)
	if err := gllvm.VerifyModule(mod, gllvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("verify package %s after coroutine lowering: %w", pkgPath, err)
	}
	if _, err := llssa.VerifyCoroAtomicCostModule(mod); err != nil {
		return fmt.Errorf("verify package %s atomic-cost certificates after coroutine lowering: %w", pkgPath, err)
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

// shouldStageNativeExecutableBackend selects the memory-bounded native
// executable pipeline. LTO already emits bitcode rather than machine objects,
// while cross/wasm builds already use an external backend, so neither needs
// this native instruction-selection isolation. Development-only method DCE
// needs every source module live while it constructs strong type overrides;
// keep that opt-in mode on the direct upstream-compatible path.
func shouldStageNativeExecutableBackend(ctx *context) bool {
	return ctx != nil &&
		ctx.mode != ModeGen &&
		ctx.buildConf != nil &&
		ctx.buildConf.BuildMode == BuildModeExe &&
		!ctx.buildConf.deadcodeDropEnabled() &&
		ctx.buildConf.ltoMode() == lto.Off &&
		useInMemoryNativeCodegen(ctx)
}

func stagePackageBitcode(ctx *context, pkg *aPackage) error {
	if ctx == nil || pkg == nil || pkg.LPkg == nil {
		return fmt.Errorf("stage package bitcode: missing build context or package module")
	}
	base := filepath.Base(pkg.ExportFile)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "pkg"
	}
	file, err := os.CreateTemp("", base+"-*.bc")
	if err != nil {
		return fmt.Errorf("create staged bitcode for %s: %w", pkg.PkgPath, err)
	}
	name := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(name)
		}
	}()
	if err := gllvm.WriteBitcodeToFile(pkg.LPkg.Module(), file); err != nil {
		return fmt.Errorf("write staged bitcode for %s: %w", pkg.PkgPath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close staged bitcode for %s: %w", pkg.PkgPath, err)
	}
	if ctx.stagedBitcodeFiles == nil {
		ctx.stagedBitcodeFiles = make(map[string]none)
	}
	ctx.stagedBitcodeFiles[name] = none{}
	pkg.StagedBitcode = name
	keep = true
	return nil
}

func exportStagedPackageObject(ctx *context, pkg *aPackage) (string, error) {
	if ctx == nil || pkg == nil || pkg.StagedBitcode == "" {
		return "", fmt.Errorf("emit staged package: missing build context or bitcode")
	}
	backend := llssa.NewProgram(newLLSSATarget(ctx.buildConf, ctx.crossCompile))
	defer backend.Dispose()
	mod, err := backend.ParseBitcodeFile(pkg.StagedBitcode)
	if err != nil {
		return "", fmt.Errorf("parse staged bitcode for %s: %w", pkg.PkgPath, err)
	}
	if err := lowerCoroPackageModuleWithProgram(backend, pkg.PkgPath, mod); err != nil {
		return "", err
	}
	if ctx.passOpt {
		options := gllvm.NewPassBuilderOptions()
		defer options.Dispose()
		if err := mod.RunPasses(llvmPassPipeline(ctx.buildConf.OptLevel, ctx.buildConf.ltoMode()), backend.TargetMachine(), options); err != nil {
			return "", fmt.Errorf("run detached LLVM passes for %s: %w", pkg.PkgPath, err)
		}
	}
	if ctx.stagedFuncInfoSites && ctx.stagedFuncInfoMeta {
		emitFuncInfoEntrySitesForModule(mod, ctx.stagedPointerSize, ctx.stagedMachOSites)
	}
	verifyAtomicCost := llssa.VerifyCoroAtomicCostModule
	if ctx.passOpt {
		verifyAtomicCost = llssa.VerifyOptimizedCoroAtomicCostModule
	}
	if _, err := verifyAtomicCost(mod); err != nil {
		return "", fmt.Errorf("verify package %s final detached atomic-cost certificates: %w", pkg.PkgPath, err)
	}
	if ctx.buildConf.CheckLLFiles {
		if err := dumpLLVMIRIfNeeded(ctx, pkg.PkgPath, pkg.ExportFile, mod.String()); err != nil {
			return "", err
		}
	}
	object, err := backend.TargetMachine().EmitToMemoryBuffer(mod, gllvm.ObjectFile)
	if err != nil {
		return "", err
	}
	defer object.Dispose()
	base := filepath.Base(pkg.ExportFile)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "pkg"
	}
	file, err := os.CreateTemp("", base+"-*.o")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if ctx.shouldPrintCommands(false) {
		fmt.Fprintf(os.Stderr, "# detached LLVM object emission %s for pkg: %s\n", name, pkg.PkgPath)
	}
	if _, err := file.Write(object.Bytes()); err != nil {
		file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func exportStagedBitcodePathWithClang(ctx *context, pkgPath, exportFile, bitcode string) (string, error) {
	if ctx == nil || bitcode == "" {
		return "", fmt.Errorf("compile staged bitcode: missing build context or artifact")
	}
	base := filepath.Base(exportFile)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "pkg"
	}
	object, err := os.CreateTemp("", base+"-*.o")
	if err != nil {
		return "", err
	}
	objectName := object.Name()
	if err := object.Close(); err != nil {
		_ = os.Remove(objectName)
		return "", err
	}
	args := []string{"-o", objectName, "-c", bitcode, "-Wno-override-module"}
	if ctx.shouldPrintCommands(false) {
		fmt.Fprintf(os.Stderr, "# compiling staged bitcode %s for pkg: %s\n", bitcode, pkgPath)
		fmt.Fprintln(os.Stderr, "clang", args)
	}
	if err := ctx.compiler().Compile(args...); err != nil {
		_ = os.Remove(objectName)
		return "", err
	}
	return objectName, nil
}

func materializeStagedPackageBackends(ctx *context, pkgs []*aPackage, verbose bool) error {
	if !shouldStageNativeExecutableBackend(ctx) {
		return nil
	}
	for _, pkg := range pkgs {
		if pkg == nil || pkg.StagedBitcode == "" {
			continue
		}
		object, err := exportStagedPackageObject(ctx, pkg)
		if err != nil {
			return fmt.Errorf("export staged object of %s failed: %w", pkg.PkgPath, err)
		}
		pkg.ObjFiles = append(pkg.ObjFiles, object)
		_ = os.Remove(pkg.StagedBitcode)
		delete(ctx.stagedBitcodeFiles, pkg.StagedBitcode)
		pkg.StagedBitcode = ""
		if err := normalizeToArchive(ctx, pkg, verbose); err != nil {
			return err
		}
		if pkg.PendingCacheSave {
			if err := ctx.saveToCache(pkg); err != nil && verbose {
				fmt.Fprintf(os.Stderr, "warning: failed to save cache for %s: %v\n", pkg.PkgPath, err)
			}
			pkg.PendingCacheSave = false
		}
	}
	entryIDs := make([]string, 0, len(ctx.stagedMainEntries))
	for id := range ctx.stagedMainEntries {
		entryIDs = append(entryIDs, id)
	}
	slices.Sort(entryIDs)
	for _, id := range entryIDs {
		entry := ctx.stagedMainEntries[id]
		if entry == nil || entry.bitcode == "" {
			return fmt.Errorf("staged main entry %q has no bitcode", id)
		}
		object, err := exportStagedBitcodePathWithClang(ctx, entry.pkgPath, entry.exportFile, entry.bitcode)
		if err != nil {
			return fmt.Errorf("export staged main entry %q failed: %w", id, err)
		}
		entry.object = object
		_ = os.Remove(entry.bitcode)
		delete(ctx.stagedBitcodeFiles, entry.bitcode)
		entry.bitcode = ""
	}
	ctx.stagedCacheAuthorized = false
	return nil
}

// releaseCoroPlanningScratchBeforeEmission drops construction-only indexes.
// The immutable plan and cl emission universe remain the sole codegen inputs;
// none of these auxiliary authorities may survive into package emission.
func releaseCoroPlanningScratchBeforeEmission(ctx *context) {
	if ctx == nil {
		return
	}
	ctx.progSSA = nil
	ctx.dedup = nil
	ctx.coroSSAEmission = nil
	ctx.coroRawGlobalSymbols = nil
	ctx.coroGlobalFunctionSlots = nil
}

// releaseBuiltPackageSource removes source-only ownership as soon as a staged
// package has been serialized. Link traversal needs only the package identity
// and Imports graph; all AST/type-checking inputs would otherwise accumulate
// behind the final, usually largest, package. A compact basename-only receipt
// preserves target source-selection evidence without retaining either source
// package graph.
func releaseBuiltPackageSource(pkg *aPackage) {
	if pkg == nil {
		return
	}
	if pkg.sourceSelection == nil {
		pkg.sourceSelection = freezePackageSourceSelection(pkg)
	}
	pkg.SSA = nil
	pkg.rewriteVars = nil
	releaseLoadedPackageAnalysis(pkg.Package)
	if alt := pkg.AltPkg; alt != nil {
		alt.Types = nil
		alt.TypesInfo = nil
		alt.Syntax = nil
		releaseLoadedPackageAnalysis(alt.Package)
	}
	pkg.AltPkg = nil
	if source := pkg.Package; source != nil {
		source.GoFiles = nil
		source.CompiledGoFiles = nil
		source.OtherFiles = nil
		source.EmbedFiles = nil
		source.EmbedPatterns = nil
		source.IgnoredFiles = nil
	}
}

func releaseLoadedPackageAnalysis(source *packages.Package) {
	if source == nil {
		return
	}
	source.Syntax = nil
	source.TypesInfo = nil
	source.TypesSizes = nil
	source.Types = nil
	source.Fset = nil
	source.Errors = nil
	source.TypeErrors = nil
}

type packageSourceSelection struct {
	goFiles    []string
	altGoFiles []string
}

func freezePackageSourceSelection(pkg *aPackage) *packageSourceSelection {
	if pkg == nil {
		return nil
	}
	selection := &packageSourceSelection{}
	if source := pkg.Package; source != nil {
		selection.goFiles = selectedSourceBasenames(source.GoFiles, source.CompiledGoFiles)
	}
	if alt := pkg.AltPkg; alt != nil && alt.Package != nil {
		selection.altGoFiles = selectedSourceBasenames(alt.GoFiles, alt.CompiledGoFiles)
	}
	return selection
}

func selectedSourceBasenames(groups ...[]string) []string {
	unique := make(map[string]none)
	for _, paths := range groups {
		for _, path := range paths {
			name := filepath.Base(path)
			if name == "." || name == string(filepath.Separator) || name == "" {
				continue
			}
			unique[strings.Clone(name)] = none{}
		}
	}
	names := slices.Collect(maps.Keys(unique))
	slices.Sort(names)
	return names
}

func releaseCoroFrontendForStagedBackend(ctx *context, pkgs []*aPackage) {
	if !shouldStageNativeExecutableBackend(ctx) {
		return
	}
	// Cache eligibility is proven only while the complete immutable plan is
	// still resident. Preserve that verdict for publication after object
	// emission; do not retain the plan merely to repeat the same proof.
	ctx.stagedCacheAuthorized = ctx.canUsePackageCache()
	ctx.stagedTargetTriple = ctx.prog.TargetSpec().Triple
	ctx.stagedDataLayout = ctx.prog.DataLayout()
	ctx.stagedFuncInfoSites = shouldEmitRuntimeSites(ctx)
	ctx.stagedFuncInfoMeta = ctx.prog.FuncInfoMetadataEnabled()
	ctx.stagedPointerSize = ctx.prog.PointerSize()
	ctx.stagedMachOSites = shouldEmitRuntimeMachOSites(ctx)
	ctx.stagedBackendDetached = true
	for _, pkg := range pkgs {
		if pkg == nil {
			continue
		}
		if pkg.LPkg != nil {
			pkg.LPkg.Module().Dispose()
			pkg.LPkg = nil
		}
		releaseBuiltPackageSource(pkg)
	}
	ctx.conf = nil
	ctx.progSSA = nil
	ctx.dedup = nil
	ctx.patches = nil
	ctx.callerTracking = nil
	ctx.built = nil
	ctx.fingerprinting = nil
	ctx.initial = nil
	ctx.cTransformer = nil
	ctx.coroPlan = nil
	ctx.coroEmission = nil
	ctx.coroSSAEmission = nil
	ctx.coroRawGlobalSymbols = nil
	ctx.coroGlobalFunctionSlots = nil
	ctx.coroLoweringFacts = coro.LoweringFacts{}
	ctx.clCompilation = nil
}

func exportObject(ctx *context, pkgPath string, exportFile string, pkg llssa.Package) (string, error) {
	if useInMemoryNativeCodegen(ctx) {
		return exportObjectInMemory(ctx, pkgPath, exportFile, pkg)
	}
	return exportObjectWithClang(ctx, pkgPath, exportFile, []byte(pkg.String()))
}

func exportPackageObject(ctx *context, pkgPath string, exportFile string, pkg llssa.Package) (string, packageArchiveBuffer, error) {
	if !useInMemoryNativeCodegen(ctx) {
		path, err := exportObjectWithClang(ctx, pkgPath, exportFile, []byte(pkg.String()))
		return path, packageArchiveBuffer{}, err
	}
	if ctx.buildConf.CheckLLFiles || ctx.buildConf.GenLL {
		if err := dumpLLVMIRIfNeeded(ctx, pkgPath, exportFile, pkg.String()); err != nil {
			return "", packageArchiveBuffer{}, err
		}
	}
	buf, kind, err := emitObjectToMemoryBuffer(ctx, pkg)
	if err != nil {
		return "", packageArchiveBuffer{}, err
	}
	name := filepath.Base(exportFile) + ".o"
	if ctx.shouldPrintCommands(false) {
		fmt.Fprintf(os.Stderr, "# compiling archive member %s for pkg: %s\n", name, pkgPath)
		fmt.Fprintf(os.Stderr, "# using %s\n", kind)
	}
	return "", packageArchiveBuffer{name: name, buffer: buf}, nil
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
		if msg, err := llcCheck(ctx.commands, f.Name()); err != nil {
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
	buf, kind, err := emitObjectToMemoryBuffer(ctx, pkg)
	if err != nil {
		return "", err
	}
	defer buf.Dispose()
	return writeObjectBufferToFile(ctx, pkgPath, exportFile, buf, kind)
}

func emitObjectToMemoryBuffer(ctx *context, pkg llssa.Package) (gllvm.MemoryBuffer, string, error) {
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
			return gllvm.MemoryBuffer{}, "", err
		}
	}
	return buf, kind, nil
}

func writeObjectBufferToFile(ctx *context, pkgPath, exportFile string, buf gllvm.MemoryBuffer, kind string) (string, error) {
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
		if msg, err := llcCheck(ctx.commands, f.Name()); err != nil {
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

func llcCheck(commands commandEnv, exportFile string) (msg string, err error) {
	cmd := commands.configure(exec.Command("llc", "-filetype=null", exportFile))
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

func prepareLocalVariables(prog llssa.Program, groups ...[]*packages.Package) error {
	seen := make(map[*types.Package]bool)
	var firstErr error
	for _, roots := range groups {
		packages.Visit(roots, nil, func(p *packages.Package) {
			if firstErr != nil || p.Types == nil || p.IllTyped || seen[p.Types] {
				return
			}
			seen[p.Types] = true
			firstErr = cl.PrepareInactiveLocalVariables(prog, p.Fset, p.Types, p.TypesInfo, p.Syntax)
		})
		if firstErr != nil {
			return firstErr
		}
	}

	if len(groups) == 0 {
		return nil
	}
	active := make(map[string]bool)
	activate := func(p *packages.Package) {
		if p.Types == nil || p.IllTyped {
			return
		}
		active[llssa.PathOf(p.Types)] = true
		prog.ActivateLocalitiesFor(p.Types)
	}
	packages.Visit(groups[0], nil, activate)
	for _, roots := range groups[1:] {
		for _, root := range roots {
			if root == nil || root.Types == nil || !active[llssa.PathOf(root.Types)] {
				continue
			}
			packages.Visit([]*packages.Package{root}, nil, activate)
		}
	}
	return nil
}

type ssaBuildEntry struct {
	pkg      *ssa.Package
	syntax   []*ast.File
	fixOrder bool
}

func registerAltSSAPkgs(prog *ssa.Program, patches cl.Patches, alts []*packages.Package, conf *Config, verbose bool) []ssaBuildEntry {
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
	entries := make([]ssaBuildEntry, 0, len(collected))
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
		entries = append(entries, ssaBuildEntry{pkg: pkgSSA, syntax: p.Syntax})
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
		pkgSSA := prog.CreatePackage(p.Types, p.Syntax, p.TypesInfo, true)
		created[p] = none{}
		entries = append(entries, ssaBuildEntry{pkg: pkgSSA, syntax: p.Syntax})
	}
	return entries
}

type aPackage struct {
	*packages.Package
	SSA    *ssa.Package
	AltPkg *packages.Cached
	LPkg   llssa.Package

	NeedRt     bool
	NeedPyInit bool

	LinkArgs    []string
	ObjFiles    []string               // file-backed archive members: .o or .ll
	ObjBuffers  []packageArchiveBuffer // LLVM-produced in-memory archive members
	ArchiveFile string                 // archive file: .a (output of archiver, used for linking)
	Meta        *meta.PackageMeta
	rewriteVars map[string]string

	// LinkSnapshot survives disposal of the package LLVM module. StagedBitcode
	// is the verified presplit frontend result; coroutine/default lowering
	// consumes it only after compilation-wide SSA state has been released.
	LinkSnapshot    *packageLinkSnapshot
	sourceSelection *packageSourceSelection
	StagedBitcode   string

	// CoroLibraryEffectRecords is producer-owned package metadata copied from
	// cl before object emission. The package archiver publishes it through a
	// metadata-only native member even when ObjFiles contain LTO bitcode.
	CoroLibraryEffectRecords []byte

	// Cache related fields
	Fingerprint      string // fingerprint digest
	Manifest         string // manifest text content
	CoroRootAnchorV1 string // linker-visible coroutine root package anchor
	CacheHit         bool   // whether cache was hit
	PendingCacheSave bool   // staged backend must archive before publishing cache
}

type Package = *aPackage

func registerSSAPkgs(ctx *context, initial []*packages.Package, verbose bool) ([]*aPackage, []ssaBuildEntry, error) {
	prog := ctx.progSSA
	var all []*aPackage
	var entries []ssaBuildEntry
	var errs []*packages.Package
	packages.Visit(initial, nil, func(p *packages.Package) {
		if p.Types != nil && !p.IllTyped {
			pkgPath := p.PkgPath
			// Use p.ID to check duplicates since same pkgPath may have different IDs
			if _, ok := ctx.pkgByID[p.ID]; ok || strings.HasPrefix(pkgPath, altPkgPathPrefix) {
				return
			}
			var altPkg *packages.Cached
			ssaPkg, created := createSSAPkg(ctx, prog, p, verbose)
			if created {
				entries = append(entries, ssaBuildEntry{pkg: ssaPkg, syntax: p.Syntax, fixOrder: true})
			}
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
		return nil, nil, fmt.Errorf("cannot build SSA for packages")
	}
	return all, entries, nil
}

// buildSSAPkgs builds registered packages with the requested bound, then
// performs ordering repair serially because it mutates instruction slices.
func buildSSAPkgs(ctx *context, entries []ssaBuildEntry) {
	if len(entries) == 0 {
		return
	}
	unique := make([]ssaBuildEntry, 0, len(entries))
	index := make(map[*ssa.Package]int, len(entries))
	for _, entry := range entries {
		if entry.pkg == nil {
			continue
		}
		if i, ok := index[entry.pkg]; ok {
			unique[i].fixOrder = unique[i].fixOrder || entry.fixOrder
			continue
		}
		index[entry.pkg] = len(unique)
		unique = append(unique, entry)
	}
	jobs := make(chan ssaBuildEntry, len(unique))
	var wg sync.WaitGroup
	for range min(ctx.buildConf.parallelism(), len(unique)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range jobs {
				entry.pkg.Build()
			}
		}()
	}
	for _, entry := range unique {
		jobs <- entry
	}
	close(jobs)
	wg.Wait()
	for _, entry := range unique {
		if entry.fixOrder {
			fixSSAOrder(entry.pkg, entry.syntax)
		}
	}
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

func createSSAPkg(ctx *context, prog *ssa.Program, p *packages.Package, verbose bool) (*ssa.Package, bool) {
	pkgSSA := prog.ImportedPackage(p.ID)
	if pkgSSA == nil {
		if debugBuild || verbose {
			log.Println("==> BuildSSA", p.ID)
		}
		applyPatches(ctx, p, verbose)
		pkgSSA = prog.CreatePackage(p.Types, p.Syntax, p.TypesInfo, true)
		return pkgSSA, true
	}
	return pkgSSA, false
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
const llgoShadowStack = "LLGO_SHADOW_STACK"

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
// metadata but drops entry and PC-line inline-asm sites). Useful for
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

func shouldRunLLVMPasses(mode Mode) bool {
	return mode != ModeGen
}

func IsWasiThreadsEnabled() bool {
	return isEnvOn(llgoWasiThreads, true)
}

// wasiThreadsForBuild keeps the embedding-owned WASI threads profile available
// to libraries while making an ordinary command self-contained. The built-in
// Preview 1 command reactor owns one scheduler and one linear memory; it does
// not provide the host thread launcher required by wasm32-wasip1-threads.
//
// crosscompile.Use ignores this value for non-WASI targets, so BuildMode is the
// only input needed before a named target has resolved GOOS/GOARCH.
func wasiThreadsForBuild(conf *Config) bool {
	return IsWasiThreadsEnabled() && (conf == nil || conf.BuildMode != BuildModeExe)
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
