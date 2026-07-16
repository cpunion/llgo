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

package cl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/goplus/llgo/cl/ssawrap"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/typepatch"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// EmissionPackage is one source package that may be passed to cl during a
// compilation. Files must be the exact combined syntax slice used by codegen:
// original package files followed by enabled alternate-package files.
type EmissionPackage struct {
	SSA          *ssa.Package
	Files        []*ast.File
	Identity     string // stable build package identity; required for same-path variants
	MetadataOnly bool   // freeze frontend directives/ownership without selecting definitions
}

// EmissionUniverseOptions selects construction contracts that are available
// only to a complete whole-program frontend. Report-only and single-package
// callers should use the zero value.
type EmissionUniverseOptions struct {
	// CompleteRuntimeABI requires the exact LLGo runtime package and freezes
	// every compiler-inserted runtime helper edge. Missing runtime helpers fail
	// construction instead of being left to the legacy LLVM symbol resolver.
	CompleteRuntimeABI bool
}

type preparedEmissionPackage struct {
	order        int
	identity     string
	ssa          *ssa.Package
	files        []*ast.File
	pkgPath      string
	oldTypes     *types.Package
	altTypes     *types.Package
	pkgTypes     *types.Package
	patch        Patch
	hasPatch     bool
	skips        map[string]none
	skipall      bool
	winners      map[string]*ssa.Function
	selected     map[*ssa.Function]none
	fromPatch    map[*ssa.Function]bool
	metadataOnly bool
}

// EmissionUniverse is an immutable set of canonical exact SSA functions and
// the aliases that codegen may use to reach them. Its public accessors return
// copies; construction completes all permitted lazy SSA materialization.
type EmissionUniverse struct {
	prog               llssa.Program
	goProg             *ssa.Program
	patches            Patches
	completeRuntimeABI bool
	packages           map[*ssa.Package]*preparedEmissionPackage
	byTypes            map[*types.Package]*preparedEmissionPackage
	typesDup           map[*types.Package]bool
	byPath             map[string]*preparedEmissionPackage
	pathDup            map[string]bool

	functions           []*ssa.Function
	required            map[*ssa.Function]none
	aliases             map[*ssa.Function]*ssa.Function
	fnOwners            map[*ssa.Function]*preparedEmissionPackage
	fnStates            map[*ssa.Function]emissionFunctionState
	functionKinds       map[emissionFunctionOwnerKey]int
	intrinsicOps        map[emissionFunctionOwnerKey]int
	finalKeys           map[emissionFunctionOwnerKey]string
	physicalNames       map[emissionFunctionOwnerKey]string
	linkOnceNames       map[*ssa.Function]string
	callWraps           map[intrinsicWrapperKey]*ssa.Function
	callWrapInfo        map[*ssa.Function]intrinsicWrapperKey
	syntheticKeys       map[*ssa.Function]string
	linkIdentities      map[*ssa.Function]string
	excluded            map[*ssa.Function]none
	materialized        map[*ssa.Function]none
	useOwners           map[*ssa.Function]map[*preparedEmissionPackage]none
	ownerStates         map[*ssa.Function]map[*preparedEmissionPackage]emissionFunctionState
	materializedOwners  map[*ssa.Function]map[*preparedEmissionPackage]none
	ownerStateErr       error
	abiMethodReferences map[*ssa.Function]map[*ssa.Function]none
	loweredCalls        map[*ssa.Function]map[string]coroLoweredCallTarget
	normalReturnBlocks  map[*ssa.Function]map[*ssa.BasicBlock]none
	foreignNoBlock      map[*ssa.Function]CoroForeignNoBlockCertificate

	localGenericMu     sync.Mutex
	localGenericTypes  map[*types.Named]emissionLocalGenericType
	localGenericOwners map[*types.Named]*ssa.Function
	genericNamedTypes  map[*types.Named]*types.Named
}

// CoroForeignNoBlockCertificate is the immutable frontend proof attached to
// one exact C declaration by //llgo:coro noblock. ID is domain-separated and
// includes the frozen owner, physical symbol, and structural ABI signature.
// PhysicalSymbol and ABISignature are exposed only for diagnostics and audit;
// consumers must compare/use ID rather than reclassifying a declaration from
// either display field.
type CoroForeignNoBlockCertificate struct {
	ID             string
	PhysicalSymbol string
	ABISignature   string
}

type coroLoweredCallTarget struct {
	target     *ssa.Function
	unwindOnly bool
}

// CoroIntrinsicCallSemantics is the frozen physical call-edge behavior of an
// llgo compiler intrinsic. It deliberately says nothing about ordinary C/Go
// functions and does not expose cl's private intrinsic opcode/name table.
type CoroIntrinsicCallSemantics uint8

const (
	// CoroIntrinsicCallUnsupported keeps the conservative managed-call edge.
	// Blocking, allocating, and otherwise unproved intrinsics use this value.
	CoroIntrinsicCallUnsupported CoroIntrinsicCallSemantics = iota
	// CoroIntrinsicCallInlineNoSuspend means cl lowers the operation directly
	// in the caller and the operation cannot suspend. There is no callable
	// coroutine edge, although the exact SSA call site remains in the plan
	// digest and the intrinsic operation is still emitted by cl.
	CoroIntrinsicCallInlineNoSuspend
	// CoroIntrinsicCallInlineWithLoweredCalls means cl erases the intrinsic
	// declaration call, but the operation emits one or more ordinary runtime
	// helper calls. Those calls are frozen separately in CoroLoweredCalls and
	// therefore retain their own suspension and unwind effects. Consumers may
	// elide only the intrinsic declaration edge, never the lowered helper edges.
	CoroIntrinsicCallInlineWithLoweredCalls
	// CoroIntrinsicCallInlineSuspend means cl erases the declaration call and
	// emits a structured suspension in the current physical coroutine frame.
	// The build analyzer seeds the owner with MayPark; there is no callable sync
	// helper and no managed callee edge.
	CoroIntrinsicCallInlineSuspend
)

// ElidesManagedCall reports whether cl removes the original SSA call to the
// intrinsic declaration. It does not imply that the complete lowered
// operation is no-suspend: InlineWithLoweredCalls carries its physical effects
// through the owner's exact frozen lowered-call set.
func (s CoroIntrinsicCallSemantics) ElidesManagedCall() bool {
	return s == CoroIntrinsicCallInlineNoSuspend || s == CoroIntrinsicCallInlineWithLoweredCalls ||
		s == CoroIntrinsicCallInlineSuspend
}

// SuspendsCurrentFrame reports the one intrinsic semantic that requires its
// owner to have a coroutine primary even though the declaration call itself is
// erased by frontend lowering.
func (s CoroIntrinsicCallSemantics) SuspendsCurrentFrame() bool {
	return s == CoroIntrinsicCallInlineSuspend
}

type intrinsicWrapperKey struct {
	owner     *ssa.Package
	intrinsic *ssa.Function
}

type emissionFunctionOwnerKey struct {
	function *ssa.Function
	owner    *preparedEmissionPackage
}

type emissionFunctionState struct {
	state     pkgState
	fromPatch bool
}

type emissionLocalGenericType struct {
	name string
	typ  *types.Named
}

// PrepareEmissionUniverse freezes package patch/skip selection and
// materializes the exact SSA functions that cl can later request. It creates
// no LLVM package or function. This compatibility entry point prepares an
// incomplete/report universe and therefore does not claim that the complete
// compiler-to-runtime ABI is available.
func PrepareEmissionUniverse(prog llssa.Program, patches Patches, inputs []EmissionPackage) (*EmissionUniverse, error) {
	return PrepareEmissionUniverseWithOptions(prog, patches, inputs, EmissionUniverseOptions{})
}

// PrepareEmissionUniverseWithOptions is PrepareEmissionUniverse with explicit
// whole-program construction contracts. Production active coroutine builds
// set CompleteRuntimeABI; unit/report universes deliberately leave it false.
func PrepareEmissionUniverseWithOptions(prog llssa.Program, patches Patches, inputs []EmissionPackage, options EmissionUniverseOptions) (*EmissionUniverse, error) {
	pathCounts := make(map[string]int, len(inputs))
	for _, input := range inputs {
		if input.SSA != nil && input.SSA.Pkg != nil {
			pathCounts[llssa.PathOf(input.SSA.Pkg)]++
		}
	}
	identities := make(map[string]*ssa.Package, len(inputs))
	u := &EmissionUniverse{
		prog:                prog,
		patches:             patches,
		completeRuntimeABI:  options.CompleteRuntimeABI,
		packages:            make(map[*ssa.Package]*preparedEmissionPackage, len(inputs)),
		byTypes:             make(map[*types.Package]*preparedEmissionPackage, len(inputs)*3),
		typesDup:            make(map[*types.Package]bool),
		byPath:              make(map[string]*preparedEmissionPackage, len(inputs)),
		pathDup:             make(map[string]bool),
		required:            make(map[*ssa.Function]none),
		aliases:             make(map[*ssa.Function]*ssa.Function),
		fnOwners:            make(map[*ssa.Function]*preparedEmissionPackage),
		fnStates:            make(map[*ssa.Function]emissionFunctionState),
		functionKinds:       make(map[emissionFunctionOwnerKey]int),
		intrinsicOps:        make(map[emissionFunctionOwnerKey]int),
		finalKeys:           make(map[emissionFunctionOwnerKey]string),
		physicalNames:       make(map[emissionFunctionOwnerKey]string),
		linkOnceNames:       make(map[*ssa.Function]string),
		callWraps:           make(map[intrinsicWrapperKey]*ssa.Function),
		callWrapInfo:        make(map[*ssa.Function]intrinsicWrapperKey),
		syntheticKeys:       make(map[*ssa.Function]string),
		abiMethodReferences: make(map[*ssa.Function]map[*ssa.Function]none),
		loweredCalls:        make(map[*ssa.Function]map[string]coroLoweredCallTarget),
		normalReturnBlocks:  make(map[*ssa.Function]map[*ssa.BasicBlock]none),
		foreignNoBlock:      make(map[*ssa.Function]CoroForeignNoBlockCertificate),
		linkIdentities:      make(map[*ssa.Function]string),
		excluded:            make(map[*ssa.Function]none),
		materialized:        make(map[*ssa.Function]none),
		useOwners:           make(map[*ssa.Function]map[*preparedEmissionPackage]none),
		ownerStates:         make(map[*ssa.Function]map[*preparedEmissionPackage]emissionFunctionState),
		materializedOwners:  make(map[*ssa.Function]map[*preparedEmissionPackage]none),
		localGenericTypes:   make(map[*types.Named]emissionLocalGenericType),
		localGenericOwners:  make(map[*types.Named]*ssa.Function),
		genericNamedTypes:   make(map[*types.Named]*types.Named),
	}
	for i, input := range inputs {
		if input.SSA == nil || input.SSA.Prog == nil || input.SSA.Pkg == nil {
			return nil, fmt.Errorf("prepare emission universe: package %d is incomplete", i)
		}
		if u.goProg == nil {
			u.goProg = input.SSA.Prog
		} else if input.SSA.Prog != u.goProg {
			return nil, fmt.Errorf("prepare emission universe: package %q belongs to another SSA program", input.SSA.Pkg.Path())
		}
		if _, exists := u.packages[input.SSA]; exists {
			return nil, fmt.Errorf("prepare emission universe: duplicate SSA package %q", input.SSA.Pkg.Path())
		}

		pkgPath := llssa.PathOf(input.SSA.Pkg)
		if pathCounts[pkgPath] > 1 {
			if _, patched := patches[pkgPath]; patched {
				return nil, fmt.Errorf("prepare emission universe: patched same-path variants for %q require independent patch type packages", pkgPath)
			}
		}
		identity := input.Identity
		if identity == "" {
			if pathCounts[pkgPath] > 1 {
				return nil, fmt.Errorf("prepare emission universe: same-path package %q requires a stable variant identity", pkgPath)
			}
			identity = pkgPath
		}
		if previous := identities[identity]; previous != nil && previous != input.SSA {
			return nil, fmt.Errorf("prepare emission universe: duplicate stable package identity %q", identity)
		}
		identities[identity] = input.SSA
		scan := &context{prog: prog, skips: make(map[string]none)}
		scan.initFiles(pkgPath, input.Files, input.SSA.Pkg.Name() == "C")
		prepared := &preparedEmissionPackage{
			order:        i,
			identity:     identity,
			ssa:          input.SSA,
			files:        append([]*ast.File(nil), input.Files...),
			pkgPath:      pkgPath,
			oldTypes:     input.SSA.Pkg,
			pkgTypes:     input.SSA.Pkg,
			skips:        cloneNoneMap(scan.skips),
			skipall:      scan.skipall,
			winners:      make(map[string]*ssa.Function),
			selected:     make(map[*ssa.Function]none),
			fromPatch:    make(map[*ssa.Function]bool),
			metadataOnly: input.MetadataOnly,
		}
		if patch, ok := patches[pkgPath]; ok {
			if patch.Alt == nil || patch.Types == nil {
				return nil, fmt.Errorf("prepare emission universe: package %q has incomplete patch", pkgPath)
			}
			prepared.patch, prepared.hasPatch, prepared.pkgTypes = patch, true, patch.Types
			prepared.altTypes = patch.Alt.Pkg
			typepatch.Merge(prepared.pkgTypes, prepared.oldTypes, prepared.skips, prepared.skipall)
			patch.Alt.Pkg = prepared.pkgTypes
		}
		u.packages[input.SSA] = prepared
		for _, pkgTypes := range []*types.Package{prepared.oldTypes, prepared.altTypes, prepared.pkgTypes} {
			if pkgTypes == nil {
				continue
			}
			if previous := u.byTypes[pkgTypes]; previous != nil && previous != prepared {
				// A shared alternate package can serve more than one same-path
				// test variant. Exact function ownership is retained in fnOwners;
				// the shared types package is intentionally not a fallback.
				delete(u.byTypes, pkgTypes)
				u.typesDup[pkgTypes] = true
				continue
			}
			if !u.typesDup[pkgTypes] {
				u.byTypes[pkgTypes] = prepared
			}
		}
		if previous := u.byPath[pkgPath]; previous != nil && previous.ssa != input.SSA {
			delete(u.byPath, pkgPath)
			u.pathDup[pkgPath] = true
		} else if !u.pathDup[pkgPath] {
			u.byPath[pkgPath] = prepared
		}
	}
	if options.CompleteRuntimeABI {
		if prog == nil {
			return nil, fmt.Errorf("prepare emission universe: complete runtime ABI requires an LLVM SSA program")
		}
		if u.pathDup[llssa.PkgRuntime] {
			return nil, fmt.Errorf("prepare emission universe: complete runtime ABI has ambiguous package path %q", llssa.PkgRuntime)
		}
		runtimePkg := u.byPath[llssa.PkgRuntime]
		if runtimePkg == nil {
			return nil, fmt.Errorf("prepare emission universe: complete runtime ABI requires package %q", llssa.PkgRuntime)
		}
		if runtimePkg.metadataOnly {
			return nil, fmt.Errorf("prepare emission universe: complete runtime ABI package %q cannot be metadata-only", llssa.PkgRuntime)
		}
	}

	// Link directives of every frontend package are now registered. Select
	// definitions in exactly the same alt-first order as
	// newPackageEx/processPkg. Declaration-only packages participate above so
	// calls into them have exact frozen C/Python ownership, but they never add
	// their fallback SSA declarations unless an emitted body actually reaches
	// one.
	for _, input := range inputs {
		if input.MetadataOnly {
			continue
		}
		prepared := u.packages[input.SSA]
		if prepared.hasPatch {
			if err := u.selectPackage(prepared, prepared.patch.Alt, pkgInPatch, nil, true); err != nil {
				return nil, err
			}
		}
		if !prepared.skipall {
			state := pkgNormal
			if prepared.hasPatch {
				state = pkgHasPatch
			}
			if err := u.selectPackage(prepared, prepared.ssa, state, prepared.skips, false); err != nil {
				return nil, err
			}
		}
	}
	// Map skipped/replaced original declarations to the alt definition that
	// owns their final managed symbol. Ambiguous or missing managed replacements
	// remain unaliased and are rejected if an effective body reaches them.
	for _, input := range inputs {
		if input.MetadataOnly {
			continue
		}
		prepared := u.packages[input.SSA]
		if prepared.hasPatch {
			if err := u.aliasPackageMembers(prepared, prepared.ssa); err != nil {
				return nil, err
			}
		}
	}

	if u.ownerStateErr != nil {
		return nil, u.ownerStateErr
	}

	u.functions = filterRequiredFunctions(u.functions, u.required)
	for {
		progress := false
		functions := stableUniqueFunctions(append([]*ssa.Function(nil), u.functions...))
		sort.SliceStable(functions, func(i, j int) bool {
			return u.functionSortKey(functions[i]) < u.functionSortKey(functions[j])
		})
		for _, fn := range functions {
			materialized, err := u.materializeFunction(fn)
			if err != nil {
				return nil, err
			}
			progress = progress || materialized
		}
		if u.ownerStateErr != nil {
			return nil, u.ownerStateErr
		}
		if !progress {
			break
		}
	}
	u.functions = stableUniqueFunctions(filterRequiredFunctions(u.functions, u.required))
	sort.SliceStable(u.functions, func(i, j int) bool {
		return u.functionSortKey(u.functions[i]) < u.functionSortKey(u.functions[j])
	})
	if err := u.freezeFunctionIdentities(); err != nil {
		return nil, err
	}
	if err := u.freezeCoroForeignNoBlockCertificates(); err != nil {
		return nil, err
	}
	return u, nil
}

// CompleteRuntimeABI reports whether construction froze the complete set of
// compiler-inserted runtime ABI edges. A false result is valid only for
// report-only or isolated frontend compilation.
func (u *EmissionUniverse) CompleteRuntimeABI() bool {
	return u != nil && u.completeRuntimeABI
}

// Functions returns canonical required functions in deterministic order.
func (u *EmissionUniverse) Functions() []*ssa.Function {
	if u == nil {
		return nil
	}
	return append([]*ssa.Function(nil), u.functions...)
}

// CoroDemandReferences returns the exact functions whose addresses are
// embedded in runtime ABI method tables emitted while lowering owner. These
// are demand-only references: a demanded owner must materialize the selected
// tfn/ifn bodies, but taking their addresses does not inherit their effects.
//
// The map is completed together with the emission universe, before coroutine
// analysis or LLVM codegen. Results are sorted by the frozen frontend identity
// and defensively copied so callers cannot change the universe after freezing.
func (u *EmissionUniverse) CoroDemandReferences(owner *ssa.Function) ([]*ssa.Function, error) {
	if u == nil {
		return nil, fmt.Errorf("coroutine ABI method references require a prepared emission universe")
	}
	if owner == nil {
		return nil, fmt.Errorf("coroutine ABI method references require an exact owner function")
	}
	canonical := u.canonicalAlias(owner)
	if canonical == nil {
		return nil, fmt.Errorf("coroutine ABI method reference owner %q has cyclic canonical aliases", owner.Name())
	}
	if canonical != owner {
		return nil, fmt.Errorf("coroutine ABI method reference owner %q is not the exact canonical function", owner.Name())
	}
	if _, frozen := u.required[owner]; !frozen {
		return nil, fmt.Errorf("coroutine ABI method reference owner %q is outside the frozen emission universe", owner.Name())
	}
	targets := make([]*ssa.Function, 0, len(u.abiMethodReferences[owner]))
	for target := range u.abiMethodReferences[owner] {
		if target == nil {
			return nil, fmt.Errorf("coroutine ABI method reference owner %q has a nil target", owner.Name())
		}
		if canonicalTarget := u.canonicalAlias(target); canonicalTarget == nil || canonicalTarget != target {
			return nil, fmt.Errorf("coroutine ABI method reference owner %q has a non-canonical target %q", owner.Name(), target.Name())
		}
		if _, frozen := u.required[target]; !frozen {
			return nil, fmt.Errorf("coroutine ABI method reference owner %q targets method %q outside the frozen emission universe", owner.Name(), target.Name())
		}
		targets = append(targets, target)
	}
	sort.SliceStable(targets, func(i, j int) bool {
		return u.functionSortKey(targets[i]) < u.functionSortKey(targets[j])
	})
	return targets, nil
}

func (u *EmissionUniverse) recordABIMethodReferences(owner *ssa.Function, targets []*ssa.Function) error {
	if owner == nil {
		return fmt.Errorf("prepare emission universe: ABI method references have no owner")
	}
	owner = u.canonicalAlias(owner)
	if owner == nil {
		return fmt.Errorf("prepare emission universe: ABI method reference owner has cyclic canonical aliases")
	}
	if _, frozen := u.required[owner]; !frozen {
		return fmt.Errorf("prepare emission universe: ABI method reference owner %q is outside the emission universe", owner.Name())
	}
	if len(targets) == 0 {
		return nil
	}
	references := u.abiMethodReferences[owner]
	if references == nil {
		references = make(map[*ssa.Function]none)
		u.abiMethodReferences[owner] = references
	}
	for _, target := range targets {
		if target == nil {
			return fmt.Errorf("prepare emission universe: ABI method reference owner %q has a nil target", owner.Name())
		}
		target = u.canonicalAlias(target)
		if target == nil {
			return fmt.Errorf("prepare emission universe: ABI method reference owner %q reached a cyclic target alias", owner.Name())
		}
		if _, frozen := u.required[target]; !frozen {
			return fmt.Errorf("prepare emission universe: ABI method reference owner %q targets method %q outside the emission universe", owner.Name(), target.Name())
		}
		references[target] = none{}
	}
	return nil
}

// CoroLoweredCalls returns the exact managed helper calls that frontend
// lowering inserts into owner without a corresponding source SSA call. Records
// are sorted by logical helper identity and defensively copied. The mapping is
// frozen together with the emission universe, before coroutine analysis and
// LLVM codegen.
func (u *EmissionUniverse) CoroLoweredCalls(owner *ssa.Function) ([]coro.SSALoweredCall, error) {
	if u == nil {
		return nil, fmt.Errorf("coroutine lowered calls require a prepared emission universe")
	}
	if owner == nil {
		return nil, fmt.Errorf("coroutine lowered calls require an exact owner function")
	}
	canonical := u.canonicalAlias(owner)
	if canonical == nil {
		return nil, fmt.Errorf("coroutine lowered-call owner %q has cyclic canonical aliases", owner.Name())
	}
	if canonical != owner {
		return nil, fmt.Errorf("coroutine lowered-call owner %q is not the exact canonical function", owner.Name())
	}
	if _, frozen := u.required[owner]; !frozen {
		return nil, fmt.Errorf("coroutine lowered-call owner %q is outside the frozen emission universe", owner.Name())
	}
	byName := u.loweredCalls[owner]
	calls := make([]coro.SSALoweredCall, 0, len(byName))
	for logicalName, frozen := range byName {
		target := frozen.target
		if logicalName == "" || !utf8.ValidString(logicalName) || strings.IndexByte(logicalName, 0) >= 0 {
			return nil, fmt.Errorf("coroutine lowered-call owner %q has invalid logical name %q", owner.Name(), logicalName)
		}
		if target == nil {
			return nil, fmt.Errorf("coroutine lowered call %q in %q has a nil target", logicalName, owner.Name())
		}
		if canonicalTarget := u.canonicalAlias(target); canonicalTarget == nil || canonicalTarget != target {
			return nil, fmt.Errorf("coroutine lowered call %q in %q has a non-canonical target %q", logicalName, owner.Name(), target.Name())
		}
		if _, frozen := u.required[target]; !frozen {
			return nil, fmt.Errorf("coroutine lowered call %q in %q targets helper %q outside the frozen emission universe", logicalName, owner.Name(), target.Name())
		}
		calls = append(calls, coro.SSALoweredCall{
			LogicalName: logicalName,
			Target:      target,
			UnwindOnly:  frozen.unwindOnly,
		})
	}
	sort.Slice(calls, func(i, j int) bool {
		return calls[i].LogicalName < calls[j].LogicalName
	})
	return calls, nil
}

// ResolveCoroLoweredCall resolves one exact frozen helper mapping. It is used
// by codegen to recover the same canonical target that analysis projected as a
// real call edge, without rediscovering it from an LLVM symbol name.
func (u *EmissionUniverse) ResolveCoroLoweredCall(owner *ssa.Function, logicalName string) (*ssa.Function, bool, error) {
	calls, err := u.CoroLoweredCalls(owner)
	if err != nil {
		return nil, false, err
	}
	index := sort.Search(len(calls), func(index int) bool {
		return calls[index].LogicalName >= logicalName
	})
	if index == len(calls) || calls[index].LogicalName != logicalName {
		return nil, false, nil
	}
	return calls[index].Target, true, nil
}

// recordCoroLoweredCall freezes one compiler-inserted helper mapping while the
// emission universe is being materialized. Repeated uses of the same logical
// helper in one owner are idempotent; resolving that identity to two exact
// targets fails closed.
func (u *EmissionUniverse) recordCoroLoweredCall(owner *ssa.Function, logicalName string, target *ssa.Function) error {
	return u.recordCoroLoweredCallSite(owner, logicalName, target, false)
}

// recordCoroLoweredCallSite freezes one physical helper-use class. A logical
// helper is unwind-only only when every occurrence in the owner is proven to
// be unwind-only; one normal-return-reachable occurrence conservatively wins.
func (u *EmissionUniverse) recordCoroLoweredCallSite(owner *ssa.Function, logicalName string, target *ssa.Function, unwindOnly bool) error {
	if owner == nil {
		return fmt.Errorf("prepare emission universe: lowered call has no owner")
	}
	if logicalName == "" || !utf8.ValidString(logicalName) || strings.IndexByte(logicalName, 0) >= 0 {
		return fmt.Errorf("prepare emission universe: lowered call in %q has invalid logical name %q", owner.Name(), logicalName)
	}
	owner = u.canonicalAlias(owner)
	if owner == nil {
		return fmt.Errorf("prepare emission universe: lowered-call owner has cyclic canonical aliases")
	}
	if _, frozen := u.required[owner]; !frozen {
		return fmt.Errorf("prepare emission universe: lowered-call owner %q is outside the emission universe", owner.Name())
	}
	if target == nil {
		return fmt.Errorf("prepare emission universe: lowered call %q in %q has a nil target", logicalName, owner.Name())
	}
	target = u.canonicalAlias(target)
	if target == nil {
		return fmt.Errorf("prepare emission universe: lowered call %q in %q reached a cyclic target alias", logicalName, owner.Name())
	}
	if _, frozen := u.required[target]; !frozen {
		return fmt.Errorf("prepare emission universe: lowered call %q in %q targets helper %q outside the emission universe", logicalName, owner.Name(), target.Name())
	}
	if u.loweredCalls == nil {
		u.loweredCalls = make(map[*ssa.Function]map[string]coroLoweredCallTarget)
	}
	byName := u.loweredCalls[owner]
	if byName == nil {
		byName = make(map[string]coroLoweredCallTarget)
		u.loweredCalls[owner] = byName
	}
	if previous, ok := byName[logicalName]; ok {
		if previous.target != target {
			return fmt.Errorf("prepare emission universe: lowered call %q in %q resolves to both %q and %q", logicalName, owner.Name(), previous.target.Name(), target.Name())
		}
		previous.unwindOnly = previous.unwindOnly && unwindOnly
		byName[logicalName] = previous
		return nil
	}
	byName[logicalName] = coroLoweredCallTarget{target: target, unwindOnly: unwindOnly}
	return nil
}

// Contains reports whether fn is an exact canonical required function.
func (u *EmissionUniverse) Contains(fn *ssa.Function) bool {
	if u == nil || fn == nil {
		return false
	}
	_, ok := u.required[fn]
	return ok
}

// Resolve maps a function pointer that codegen may encounter to the exact
// canonical function stored in the coroutine plan.
func (u *EmissionUniverse) Resolve(fn *ssa.Function) (*ssa.Function, bool) {
	if u == nil || fn == nil {
		return nil, false
	}
	fn = u.canonicalAlias(fn)
	if fn == nil {
		return nil, false
	}
	_, ok := u.required[fn]
	return fn, ok
}

// FunctionBackground reports the frozen frontend ABI background of fn's exact
// canonical emission function. The classification comes only from the final
// per-owner function-kind and managed-symbol metadata recorded while preparing
// the universe; it never reclassifies a function from its name or package.
// llgo intrinsics and deliberately ignored declarations are valid but
// unclassified and return classified=false.
func (u *EmissionUniverse) FunctionBackground(fn *ssa.Function) (background llssa.Background, classified bool, err error) {
	if u == nil {
		return 0, false, fmt.Errorf("emission universe function background: nil universe")
	}
	if fn == nil {
		return 0, false, fmt.Errorf("emission universe function background: nil function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return 0, false, fmt.Errorf("emission universe function background: function has cyclic canonical aliases")
	}
	if _, required := u.required[canonical]; !required {
		return 0, false, fmt.Errorf("emission universe function background: function %q is absent from the frozen emission universe", canonical.Name())
	}
	ownerSet := u.useOwners[canonical]
	if len(ownerSet) == 0 {
		return 0, false, fmt.Errorf("emission universe function background: canonical function %q has no frozen use owner", canonical.Name())
	}
	owners := make([]*preparedEmissionPackage, 0, len(ownerSet))
	for owner := range ownerSet {
		if owner == nil {
			return 0, false, fmt.Errorf("emission universe function background: canonical function %q has a nil frozen use owner", canonical.Name())
		}
		owners = append(owners, owner)
	}
	sort.SliceStable(owners, func(i, j int) bool {
		if owners[i].order != owners[j].order {
			return owners[i].order < owners[j].order
		}
		if owners[i].identity != owners[j].identity {
			return owners[i].identity < owners[j].identity
		}
		return owners[i].pkgPath < owners[j].pkgPath
	})
	states := u.ownerStates[canonical]
	var frozenKind int
	haveKind := false
	for _, owner := range owners {
		if _, ok := states[owner]; !ok {
			return 0, false, fmt.Errorf("emission universe function background: canonical function %q has no frozen provenance for owner %q", canonical.Name(), owner.identity)
		}
		ownerKey := emissionFunctionOwnerKey{function: canonical, owner: owner}
		kind, ok := u.functionKinds[ownerKey]
		if !ok {
			return 0, false, fmt.Errorf("emission universe function background: canonical function %q has no frozen frontend function kind for owner %q", canonical.Name(), owner.identity)
		}
		finalKey := u.finalKeys[ownerKey]
		if finalKey != "" {
			finalKind, _, _, valid := splitManagedSymbolKey(finalKey)
			if !valid {
				return 0, false, fmt.Errorf("emission universe function background: canonical function %q has malformed frozen managed-symbol metadata for owner %q", canonical.Name(), owner.identity)
			}
			if finalKind != kind {
				return 0, false, fmt.Errorf("emission universe function background: canonical function %q has inconsistent frozen frontend kinds %d and %d for owner %q", canonical.Name(), kind, finalKind, owner.identity)
			}
		} else if kind == llgoInstr {
			if _, ok := u.intrinsicOps[ownerKey]; !ok {
				return 0, false, fmt.Errorf("emission universe function background: canonical intrinsic %q has no frozen compiler opcode for owner %q", canonical.Name(), owner.identity)
			}
		} else if kind != ignoredFunc {
			// Intrinsic function-value wrappers are exact synthetic Go functions.
			// Their frozen synthetic provenance replaces a managed declaration key.
			_, intrinsicWrapper := u.callWrapInfo[canonical]
			if kind != goFunc || !intrinsicWrapper || u.syntheticKeys[canonical] == "" {
				return 0, false, fmt.Errorf("emission universe function background: canonical function %q has no frozen managed-symbol metadata for owner %q", canonical.Name(), owner.identity)
			}
		}
		if haveKind && frozenKind != kind {
			return 0, false, fmt.Errorf("emission universe function background: canonical function %q has inconsistent frozen frontend kinds %d and %d across owners", canonical.Name(), frozenKind, kind)
		}
		frozenKind = kind
		haveKind = true
	}
	if !haveKind {
		return 0, false, fmt.Errorf("emission universe function background: canonical function %q has no frozen frontend function kind", canonical.Name())
	}
	switch frozenKind {
	case goFunc:
		return llssa.InGo, true, nil
	case cFunc:
		return llssa.InC, true, nil
	case pyFunc:
		return llssa.InPython, true, nil
	case ignoredFunc, llgoInstr:
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("emission universe function background: canonical function %q has unknown frozen frontend function kind %d", canonical.Name(), frozenKind)
	}
}

// CoroForeignNoBlockCertificate returns the frozen declaration certificate for
// fn. The proof exists only for an exact emitted C declaration carrying the
// //llgo:coro noblock directive. Ordinary C declarations remain unclassified
// and therefore retain the conservative BlockForeign/WaitForeign boundary.
func (u *EmissionUniverse) CoroForeignNoBlockCertificate(fn *ssa.Function) (certificate CoroForeignNoBlockCertificate, certified bool, err error) {
	if u == nil {
		return CoroForeignNoBlockCertificate{}, false, fmt.Errorf("coroutine foreign noblock certificate: nil emission universe")
	}
	if fn == nil {
		return CoroForeignNoBlockCertificate{}, false, fmt.Errorf("coroutine foreign noblock certificate: nil function")
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return CoroForeignNoBlockCertificate{}, false, fmt.Errorf("coroutine foreign noblock certificate: function has cyclic canonical aliases")
	}
	if _, required := u.required[canonical]; !required {
		return CoroForeignNoBlockCertificate{}, false, fmt.Errorf("coroutine foreign noblock certificate: function %q is absent from the frozen emission universe", canonical.Name())
	}
	certificate, certified = u.foreignNoBlock[canonical]
	return certificate, certified, nil
}

// CoroIntrinsicSemantics reports whether fn is an exact frozen llgo compiler
// intrinsic and, if so, its narrow coroutine call-edge semantics. The result
// is recorded during universe construction and never inferred from the Go
// function name at analysis time. This function-level result does not prove
// opcode-specific operand preconditions; consumers deciding whether to elide a
// physical call must use CoroIntrinsicCallSiteSemantics instead.
func (u *EmissionUniverse) CoroIntrinsicSemantics(fn *ssa.Function) (semantics CoroIntrinsicCallSemantics, intrinsic bool, err error) {
	opcode, intrinsic, err := u.coroIntrinsicOpcode(fn)
	if err != nil || !intrinsic {
		return CoroIntrinsicCallUnsupported, intrinsic, err
	}
	return coroIntrinsicCallSemantics(opcode), true, nil
}

// CoroIntrinsicCallSiteSemantics reports the frozen physical semantics of one
// exact SSA call site. A function-level intrinsic opcode is insufficient proof
// that a particular source operation can be elided: opcode-specific lowering
// preconditions are checked here against the same SSA arguments consumed by
// cl. Invalid intrinsic sites fail closed instead of being disguised as an
// ordinary managed call and reaching a later lowering panic.
func (u *EmissionUniverse) CoroIntrinsicCallSiteSemantics(call ssa.CallInstruction) (semantics CoroIntrinsicCallSemantics, intrinsic bool, err error) {
	if call == nil || call.Common() == nil {
		return CoroIntrinsicCallUnsupported, false, fmt.Errorf("emission universe intrinsic call semantics: nil SSA call")
	}
	callee := call.Common().StaticCallee()
	if callee == nil {
		return CoroIntrinsicCallUnsupported, false, nil
	}
	opcode, intrinsic, err := u.coroIntrinsicOpcode(callee)
	if err != nil || !intrinsic {
		return CoroIntrinsicCallUnsupported, intrinsic, err
	}
	semantics = coroIntrinsicCallSemantics(opcode)
	if !semantics.ElidesManagedCall() {
		return semantics, true, nil
	}
	direct, ok := call.(*ssa.Call)
	if !ok || direct.Common() == nil || direct.Common().IsInvoke() {
		return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
			"emission universe intrinsic call semantics: inline intrinsic %q must be an exact direct call", callee.Name(),
		)
	}
	if isCoroAtomicIntrinsic(opcode) {
		if err := validateCoroAtomicIntrinsicCallSite(opcode, direct); err != nil {
			return CoroIntrinsicCallUnsupported, true, err
		}
		return CoroIntrinsicCallInlineNoSuspend, true, nil
	}
	switch opcode {
	case llgoCstr:
		args := direct.Common().Args
		if len(args) != 1 {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.cstr call %q requires exactly one compile-time string constant argument", direct.String(),
			)
		}
		value, ok := args[0].(*ssa.Const)
		if !ok || value.Value == nil || value.Value.Kind() != constant.String {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.cstr call %q requires exactly one compile-time string constant argument", direct.String(),
			)
		}
		return CoroIntrinsicCallInlineNoSuspend, true, nil
	case llgoAdvance:
		args := direct.Common().Args
		if len(args) != 2 {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.advance call %q requires exactly two arguments", direct.String(),
			)
		}
		// context.advance passes these operands directly to Builder.Advance.
		// Builder.Advance accepts an actual Go pointer or unsafe.Pointer and an
		// LLVM integer GEP index; accepting a merely pointer-shaped named value
		// here would disagree with that lowering's raw-type switch.
		pointerType := types.Unalias(args[0].Type())
		switch pointerType := pointerType.(type) {
		case *types.Pointer:
		case *types.Basic:
			if pointerType.Kind() != types.UnsafePointer {
				return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
					"emission universe intrinsic call semantics: llgo.advance call %q requires a pointer first argument", direct.String(),
				)
			}
		default:
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.advance call %q requires a pointer first argument", direct.String(),
			)
		}
		offsetType, ok := types.Unalias(args[1].Type()).Underlying().(*types.Basic)
		if !ok || offsetType.Info()&types.IsInteger == 0 {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.advance call %q requires an integer offset argument", direct.String(),
			)
		}
		results := direct.Common().Signature().Results()
		if results == nil || results.Len() != 1 || !types.Identical(results.At(0).Type(), args[0].Type()) {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.advance call %q requires one result matching its pointer argument", direct.String(),
			)
		}
		return CoroIntrinsicCallInlineNoSuspend, true, nil
	case llgoAllocaCStr:
		args := direct.Common().Args
		if len(args) != 1 {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.allocaCStr call %q requires exactly one string argument", direct.String(),
			)
		}
		argType, ok := types.Unalias(args[0].Type()).Underlying().(*types.Basic)
		if !ok || argType.Kind() != types.String {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.allocaCStr call %q requires exactly one string argument", direct.String(),
			)
		}
		signature := direct.Common().Signature()
		results := signature.Results()
		if signature.Recv() != nil || signature.Variadic() || results == nil || results.Len() != 1 {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.allocaCStr call %q requires one *int8 result", direct.String(),
			)
		}
		resultPointer, ok := types.Unalias(results.At(0).Type()).Underlying().(*types.Pointer)
		if !ok {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.allocaCStr call %q requires one *int8 result", direct.String(),
			)
		}
		resultElem, ok := types.Unalias(resultPointer.Elem()).Underlying().(*types.Basic)
		if !ok || resultElem.Kind() != types.Int8 {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.allocaCStr call %q requires one *int8 result", direct.String(),
			)
		}
		if !u.CompleteRuntimeABI() {
			// Isolated/report compilation retains the legacy rtFunc call and has
			// no complete owner-scoped runtime-helper map. Do not elide the
			// intrinsic declaration in that mode.
			return CoroIntrinsicCallUnsupported, true, nil
		}
		helper, frozen, helperErr := u.ResolveCoroLoweredCall(direct.Parent(), "CStrCopy")
		if helperErr != nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.allocaCStr call %q resolve frozen CStrCopy helper: %w", direct.String(), helperErr,
			)
		}
		if !frozen || helper == nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.allocaCStr call %q has no exact frozen CStrCopy lowered call", direct.String(),
			)
		}
		return CoroIntrinsicCallInlineWithLoweredCalls, true, nil
	case llgoDeferData:
		if len(direct.Common().Args) != 0 {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.deferData call %q requires no arguments", direct.String(),
			)
		}
		signature := direct.Common().Signature()
		if signature == nil || signature.Recv() != nil || signature.Variadic() || (signature.Params() != nil && signature.Params().Len() != 0) {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.deferData call %q requires the exact func() unsafe.Pointer shape", direct.String(),
			)
		}
		results := signature.Results()
		if results == nil || results.Len() != 1 {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.deferData call %q requires the exact func() unsafe.Pointer shape", direct.String(),
			)
		}
		result, ok := types.Unalias(results.At(0).Type()).Underlying().(*types.Basic)
		if !ok || result.Kind() != types.UnsafePointer {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.deferData call %q requires the exact func() unsafe.Pointer shape", direct.String(),
			)
		}
		if !u.CompleteRuntimeABI() {
			return CoroIntrinsicCallUnsupported, true, nil
		}
		helper, frozen, helperErr := u.ResolveCoroLoweredCall(direct.Parent(), "GetThreadDefer")
		if helperErr != nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.deferData call %q resolve frozen GetThreadDefer helper: %w", direct.String(), helperErr,
			)
		}
		if !frozen || helper == nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.deferData call %q has no exact frozen GetThreadDefer lowered call", direct.String(),
			)
		}
		return CoroIntrinsicCallInlineWithLoweredCalls, true, nil
	case llgoString:
		owner := u.ownerOf(direct.Parent())
		if owner == nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.string call %q has no exact frozen owner", direct.String(),
			)
		}
		ctx, ctxErr := u.functionABIContext(direct.Parent(), owner)
		if ctxErr != nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.string call %q build exact lowering context: %w", direct.String(), ctxErr,
			)
		}
		helperName, helperShapeErr := emissionStringIntrinsicHelper(ctx, direct)
		if helperShapeErr != nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: %w", helperShapeErr,
			)
		}
		if !u.CompleteRuntimeABI() {
			return CoroIntrinsicCallUnsupported, true, nil
		}
		helper, frozen, helperErr := u.ResolveCoroLoweredCall(direct.Parent(), helperName)
		if helperErr != nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.string call %q resolve frozen %s helper: %w", direct.String(), helperName, helperErr,
			)
		}
		if !frozen || helper == nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: llgo.string call %q has no exact frozen %s lowered call", direct.String(), helperName,
			)
		}
		return CoroIntrinsicCallInlineWithLoweredCalls, true, nil
	case llgoSigjmpbuf:
		if err := validateCoroSigjmpIntrinsicCallSite(opcode, direct); err != nil {
			return CoroIntrinsicCallUnsupported, true, err
		}
		return CoroIntrinsicCallInlineNoSuspend, true, nil
	case llgoSigsetjmp, llgoSiglongjmp:
		if err := validateCoroSigjmpIntrinsicCallSite(opcode, direct); err != nil {
			return CoroIntrinsicCallUnsupported, true, err
		}
		if !u.coroUsesRuntimeSigjmpHelpers() {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: legacy llgo setjmp/longjmp call %q lowers directly to a target C leaf and requires a non-legacy coroutine PanicABI", direct.String(),
			)
		}
		if !u.CompleteRuntimeABI() {
			return CoroIntrinsicCallUnsupported, true, nil
		}
		helperName := "Sigsetjmp"
		if opcode == llgoSiglongjmp {
			helperName = "Siglongjmp"
		}
		helper, frozen, helperErr := u.ResolveCoroLoweredCall(direct.Parent(), helperName)
		if helperErr != nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: legacy %s call %q resolve frozen runtime helper: %w", helperName, direct.String(), helperErr,
			)
		}
		if !frozen || helper == nil {
			return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
				"emission universe intrinsic call semantics: legacy %s call %q has no exact frozen lowered call", helperName, direct.String(),
			)
		}
		return CoroIntrinsicCallInlineWithLoweredCalls, true, nil
	case llgoFuncAddr:
		if _, _, err := u.validateCoroFuncAddrCallSite(direct); err != nil {
			return CoroIntrinsicCallUnsupported, true, err
		}
		return CoroIntrinsicCallInlineNoSuspend, true, nil
	case llgoCoroPark:
		if err := validateCoroParkIntrinsicCallSite(direct); err != nil {
			return CoroIntrinsicCallUnsupported, true, err
		}
		return CoroIntrinsicCallInlineSuspend, true, nil
	default:
		return CoroIntrinsicCallUnsupported, true, fmt.Errorf(
			"emission universe intrinsic call semantics: inline intrinsic %q has no exact call-site verifier", callee.Name(),
		)
	}
}

// CoroRawFunctionAddressCallArgument reports the one exact call argument that
// funcAddr consumes as a raw static entry address. Unlike an ordinary
// MakeInterface, this operand is inspected structurally and no interface value
// is emitted. Consumers use this frozen fact to avoid forcing the target into
// Dispatch representation solely because x/tools SSA inserted the transient
// MakeInterface node.
func (u *EmissionUniverse) CoroRawFunctionAddressCallArgument(call ssa.CallInstruction, argument int) (bool, error) {
	if call == nil || call.Common() == nil || argument < 0 || argument >= len(call.Common().Args) {
		return false, nil
	}
	callee := call.Common().StaticCallee()
	if callee == nil {
		return false, nil
	}
	opcode, intrinsic, err := u.coroIntrinsicOpcode(callee)
	if err != nil || !intrinsic || opcode != llgoFuncAddr {
		return false, err
	}
	direct, ok := call.(*ssa.Call)
	if !ok || direct.Common() == nil || direct.Common().IsInvoke() {
		return false, fmt.Errorf("emission universe raw function address: llgo.funcAddr must be an exact direct call")
	}
	if _, _, err := u.validateCoroFuncAddrCallSite(direct); err != nil {
		return false, err
	}
	return argument == 0, nil
}

func (u *EmissionUniverse) validateCoroFuncAddrCallSite(direct *ssa.Call) (*ssa.MakeInterface, *ssa.Function, error) {
	if direct == nil || direct.Common() == nil || direct.Common().IsInvoke() {
		return nil, nil, fmt.Errorf("emission universe intrinsic call semantics: llgo.funcAddr must be an exact direct call")
	}
	args := direct.Common().Args
	if len(args) != 1 {
		return nil, nil, fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcAddr call %q requires exactly one argument", direct.String(),
		)
	}
	signature := direct.Common().Signature()
	if signature == nil || signature.Recv() != nil || signature.Variadic() || signature.Params() == nil || signature.Params().Len() != 1 {
		return nil, nil, fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcAddr call %q requires the exact func(any) unsafe.Pointer shape", direct.String(),
		)
	}
	parameterInterface, ok := types.Unalias(signature.Params().At(0).Type()).Underlying().(*types.Interface)
	if !ok || !parameterInterface.Empty() {
		return nil, nil, fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcAddr call %q requires the exact func(any) unsafe.Pointer shape", direct.String(),
		)
	}
	results := signature.Results()
	if results == nil || results.Len() != 1 {
		return nil, nil, fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcAddr call %q requires the exact func(any) unsafe.Pointer shape", direct.String(),
		)
	}
	result, ok := types.Unalias(results.At(0).Type()).Underlying().(*types.Basic)
	if !ok || result.Kind() != types.UnsafePointer {
		return nil, nil, fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcAddr call %q requires the exact func(any) unsafe.Pointer shape", direct.String(),
		)
	}
	boxed, ok := args[0].(*ssa.MakeInterface)
	if !ok {
		return nil, nil, fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcAddr call %q requires a direct MakeInterface function operand", direct.String(),
		)
	}
	target, ok := boxed.X.(*ssa.Function)
	if !ok || len(target.FreeVars) != 0 {
		return nil, nil, fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcAddr call %q requires MakeInterface{X:*ssa.Function} without captured state", direct.String(),
		)
	}
	refs := boxed.Referrers()
	if refs == nil || len(*refs) != 1 || (*refs)[0] != direct {
		return nil, nil, fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcAddr call %q requires its MakeInterface operand to have this exact sole consumer", direct.String(),
		)
	}
	if canonical, resolved := u.Resolve(target); !resolved || canonical == nil {
		return nil, nil, fmt.Errorf(
			"emission universe intrinsic call semantics: llgo.funcAddr call %q targets function %q outside the frozen emission universe", direct.String(), target.Name(),
		)
	}
	return boxed, target, nil
}

func (u *EmissionUniverse) coroIntrinsicOpcode(fn *ssa.Function) (opcode int, intrinsic bool, err error) {
	_, classified, err := u.FunctionBackground(fn)
	if err != nil {
		return 0, false, err
	}
	if classified {
		return 0, false, nil
	}
	canonical := u.canonicalAlias(fn)
	if canonical == nil {
		return 0, false, fmt.Errorf("emission universe intrinsic semantics: function has cyclic canonical aliases")
	}
	owners := u.sortedUseOwners(canonical)
	if len(owners) == 0 {
		return 0, false, fmt.Errorf("emission universe intrinsic semantics: canonical function %q has no frozen use owner", canonical.Name())
	}
	for _, owner := range owners {
		ownerKey := emissionFunctionOwnerKey{function: canonical, owner: owner}
		kind, ok := u.functionKinds[ownerKey]
		if !ok {
			return 0, false, fmt.Errorf("emission universe intrinsic semantics: canonical function %q has no frozen frontend function kind for owner %q", canonical.Name(), owner.identity)
		}
		if kind != llgoInstr {
			return 0, false, nil
		}
		ownerOpcode, ok := u.intrinsicOps[ownerKey]
		if !ok {
			return 0, false, fmt.Errorf("emission universe intrinsic semantics: canonical intrinsic %q has no frozen compiler opcode for owner %q", canonical.Name(), owner.identity)
		}
		if opcode != 0 && opcode != ownerOpcode {
			return 0, false, fmt.Errorf("emission universe intrinsic semantics: canonical intrinsic %q has inconsistent compiler opcodes across owners", canonical.Name())
		}
		opcode = ownerOpcode
	}
	return opcode, true, nil
}

func coroIntrinsicCallSemantics(opcode int) CoroIntrinsicCallSemantics {
	if isCoroAtomicIntrinsic(opcode) {
		return CoroIntrinsicCallInlineNoSuspend
	}
	switch opcode {
	case llgoCstr:
		// cstr accepts only a compile-time string literal and lowers directly
		// to an LLVM constant C string pointer.
		return CoroIntrinsicCallInlineNoSuspend
	case llgoAdvance:
		// advance lowers directly to one LLVM GEP after its exact operand and
		// result shape has been verified at the physical call site.
		return CoroIntrinsicCallInlineNoSuspend
	case llgoAllocaCStr:
		// allocaCStr lowers its string length arithmetic and storage directly,
		// then calls runtime.CStrCopy. The intrinsic declaration disappears, but
		// the exact CStrCopy edge is retained in the owner's lowered-call set.
		return CoroIntrinsicCallInlineWithLoweredCalls
	case llgoDeferData:
		// deferData removes the intrinsic declaration but emits the exact
		// runtime.GetThreadDefer call owned by the surrounding function.
		return CoroIntrinsicCallInlineWithLoweredCalls
	case llgoString:
		// string replaces the intrinsic declaration with exactly one of
		// runtime.StringFromCStr or runtime.StringFrom based on the frozen
		// frontend variadic shape.
		return CoroIntrinsicCallInlineWithLoweredCalls
	case llgoSigjmpbuf:
		// sigjmpbuf is a target-sized LLVM alloca and has no callable edge.
		return CoroIntrinsicCallInlineNoSuspend
	case llgoSigsetjmp, llgoSiglongjmp:
		// Native legacy PanicABI replaces these declarations with the exact
		// runtime C-linkname leaves. WASM and explicit embedded targets fail
		// closed until their non-legacy PanicABI is selected.
		return CoroIntrinsicCallInlineWithLoweredCalls
	case llgoFuncAddr:
		// funcAddr structurally unwraps one exact MakeInterface{X:*ssa.Function}
		// and emits the selected raw function entry address directly.
		return CoroIntrinsicCallInlineNoSuspend
	case llgoCoroPark:
		return CoroIntrinsicCallInlineSuspend
	default:
		return CoroIntrinsicCallUnsupported
	}
}

func validateCoroParkIntrinsicCallSite(call *ssa.Call) error {
	if call == nil || call.Common() == nil {
		return fmt.Errorf("llgo.coroPark requires an exact direct call")
	}
	common := call.Common()
	if common.IsInvoke() || len(common.Args) != 2 {
		return fmt.Errorf("llgo.coroPark call %q requires exactly (pointer, uint32) arguments", call.String())
	}
	signature := common.Signature()
	if signature == nil || signature.Recv() != nil || signature.Variadic() ||
		(signature.Results() != nil && signature.Results().Len() != 0) ||
		signature.Params() == nil || signature.Params().Len() != 2 {
		return fmt.Errorf("llgo.coroPark call %q requires the exact func(pointer, uint32) shape", call.String())
	}
	pointerLike := func(typ types.Type) bool {
		typ = types.Unalias(typ)
		if _, ok := typ.Underlying().(*types.Pointer); ok {
			return true
		}
		basic, ok := typ.Underlying().(*types.Basic)
		return ok && basic.Kind() == types.UnsafePointer
	}
	uint32Like := func(typ types.Type) bool {
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		return ok && basic.Kind() == types.Uint32
	}
	if !pointerLike(common.Args[0].Type()) || !pointerLike(signature.Params().At(0).Type()) ||
		!uint32Like(common.Args[1].Type()) || !uint32Like(signature.Params().At(1).Type()) {
		return fmt.Errorf("llgo.coroPark call %q requires the exact func(pointer, uint32) shape", call.String())
	}
	return nil
}

func isCoroAtomicIntrinsic(opcode int) bool {
	return opcode == llgoAtomicLoad || opcode == llgoAtomicStore || opcode == llgoAtomicCmpXchg ||
		opcode == llgoAtomicCmpXchgOK || opcode == llgoAtomicAddReturnNew ||
		opcode >= llgoAtomicOpBase && opcode <= llgoAtomicOpLast
}

func validateCoroAtomicIntrinsicCallSite(opcode int, direct *ssa.Call) error {
	if direct == nil || direct.Common() == nil || direct.Common().IsInvoke() {
		return fmt.Errorf("emission universe intrinsic call semantics: llgo atomic intrinsic must be an exact direct call")
	}
	common := direct.Common()
	signature := common.Signature()
	if signature == nil || signature.Recv() != nil || signature.Variadic() || signature.Params() == nil {
		return fmt.Errorf("emission universe intrinsic call semantics: llgo atomic call %q has an invalid declaration shape", direct.String())
	}
	params := signature.Params()
	results := signature.Results()
	if params.Len() == 0 {
		return fmt.Errorf("emission universe intrinsic call semantics: llgo atomic call %q has no pointer operand", direct.String())
	}
	pointer, ok := types.Unalias(params.At(0).Type()).Underlying().(*types.Pointer)
	if !ok || !emissionIsAtomicScalarType(pointer.Elem()) {
		return fmt.Errorf("emission universe intrinsic call semantics: llgo atomic call %q requires a pointer to an integer or unsafe.Pointer value", direct.String())
	}
	elem := pointer.Elem()
	matchingParam := func(index int) bool {
		return index >= 0 && index < params.Len() && types.Identical(params.At(index).Type(), elem)
	}
	matchingResult := func(index int) bool {
		return results != nil && index >= 0 && index < results.Len() && types.Identical(results.At(index).Type(), elem)
	}
	boolResult := func(index int) bool {
		return results != nil && index >= 0 && index < results.Len() && emissionIsBasicKind(results.At(index).Type(), types.Bool)
	}
	noResults := results == nil || results.Len() == 0

	valid := false
	switch {
	case opcode == llgoAtomicLoad:
		valid = len(common.Args) == 1 && params.Len() == 1 && results != nil && results.Len() == 1 && matchingResult(0)
	case opcode == llgoAtomicStore:
		valid = len(common.Args) == 2 && params.Len() == 2 && matchingParam(1) && noResults
	case opcode == llgoAtomicCmpXchg:
		valid = len(common.Args) == 3 && params.Len() == 3 && matchingParam(1) && matchingParam(2) && results != nil && results.Len() == 2 && matchingResult(0) && boolResult(1)
	case opcode == llgoAtomicCmpXchgOK:
		valid = len(common.Args) == 3 && params.Len() == 3 && matchingParam(1) && matchingParam(2) && results != nil && results.Len() == 1 && boolResult(0)
	case opcode == llgoAtomicAddReturnNew || opcode >= llgoAtomicOpBase && opcode <= llgoAtomicOpLast:
		valid = len(common.Args) == 2 && params.Len() == 2 && matchingParam(1) && results != nil && results.Len() == 1 && matchingResult(0)
	}
	if !valid {
		return fmt.Errorf("emission universe intrinsic call semantics: llgo atomic call %q does not match opcode %d's exact pointer/value/result shape", direct.String(), opcode)
	}
	return nil
}

func emissionIsAtomicScalarType(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && (basic.Info()&types.IsInteger != 0 || basic.Kind() == types.UnsafePointer)
}

func (u *EmissionUniverse) coroUsesRuntimeSigjmpHelpers() bool {
	if u == nil || u.prog == nil || u.prog.Target() == nil {
		return false
	}
	target := u.prog.Target()
	return target.GOARCH != "wasm" && target.Target == ""
}

func validateCoroSigjmpIntrinsicCallSite(opcode int, direct *ssa.Call) error {
	if direct == nil || direct.Common() == nil || direct.Common().IsInvoke() {
		return fmt.Errorf("emission universe intrinsic call semantics: llgo setjmp/longjmp intrinsic must be an exact direct call")
	}
	common := direct.Common()
	signature := common.Signature()
	if signature == nil || signature.Recv() != nil || signature.Variadic() {
		return fmt.Errorf("emission universe intrinsic call semantics: llgo setjmp/longjmp call %q has an invalid declaration shape", direct.String())
	}
	params := signature.Params()
	results := signature.Results()
	switch opcode {
	case llgoSigjmpbuf:
		if len(common.Args) != 0 || params != nil && params.Len() != 0 || results == nil || results.Len() != 1 || !emissionIsUnsafePointerType(results.At(0).Type()) {
			return fmt.Errorf("emission universe intrinsic call semantics: llgo.sigjmpbuf call %q requires the exact func() unsafe.Pointer shape", direct.String())
		}
	case llgoSigsetjmp:
		if len(common.Args) != 2 || params == nil || params.Len() != 2 || results == nil || results.Len() != 1 ||
			!emissionIsUnsafePointerType(params.At(0).Type()) || !emissionIsBasicKind(params.At(1).Type(), types.Int32) || !emissionIsBasicKind(results.At(0).Type(), types.Int32) {
			return fmt.Errorf("emission universe intrinsic call semantics: llgo.sigsetjmp call %q requires the exact func(unsafe.Pointer, int32) int32 shape", direct.String())
		}
	case llgoSiglongjmp:
		if len(common.Args) != 2 || params == nil || params.Len() != 2 || results != nil && results.Len() != 0 ||
			!emissionIsUnsafePointerType(params.At(0).Type()) || !emissionIsBasicKind(params.At(1).Type(), types.Int32) {
			return fmt.Errorf("emission universe intrinsic call semantics: llgo.siglongjmp call %q requires the exact func(unsafe.Pointer, int32) shape", direct.String())
		}
	default:
		return fmt.Errorf("emission universe intrinsic call semantics: unknown llgo setjmp/longjmp opcode %d", opcode)
	}
	return nil
}

func emissionIsUnsafePointerType(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.UnsafePointer
}

func emissionIsBasicKind(typ types.Type, kind types.BasicKind) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == kind
}

func (u *EmissionUniverse) physicalName(ownerSSA *ssa.Package, fn *ssa.Function, legacy string) (string, error) {
	if u == nil || fn == nil {
		return legacy, nil
	}
	fn = u.canonicalAlias(fn)
	if fn == nil {
		return "", fmt.Errorf("coroutine entry resolution: function has cyclic emission aliases")
	}
	owner := u.packages[ownerSSA]
	if name := u.physicalNames[emissionFunctionOwnerKey{function: fn, owner: owner}]; name != "" {
		return name, nil
	}
	if isEmissionGeneratedWrapper(fn) {
		ownerName := "<unknown>"
		if owner != nil {
			ownerName = owner.identity
		}
		return "", fmt.Errorf("coroutine entry resolution: generated wrapper %q has no frozen physical symbol for owner %q", fn.Name(), ownerName)
	}
	return legacy, nil
}

// SSAProgram returns the x/tools SSA program that owns every exact function
// in this universe. Together with Functions it is the input to
// coro.NewSSAEmissionUniverse.
func (u *EmissionUniverse) SSAProgram() *ssa.Program {
	if u == nil {
		return nil
	}
	return u.goProg
}

// FunctionIDConfig returns this universe's complete frozen identity
// configuration: final link identities, package variants, substituted local
// generic type owners, and frontend-defined synthetic functions.
func (u *EmissionUniverse) FunctionIDConfig() coro.FunctionIDConfig {
	return u.AugmentFunctionIDConfig(coro.FunctionIDConfig{})
}

// AugmentFunctionIDConfig augments base with the universe's frozen final link
// identities, exact package variants, substituted local generic type owners,
// and exact-pointer provenance for intrinsic function-value wrappers. Wrapper
// keys include the emitting owner package and wrapped intrinsic identity; they
// never treat ssawrap's diagnostic Synthetic string as identity. Resolvers
// already present in base remain fallbacks for identities outside the universe.
func (u *EmissionUniverse) AugmentFunctionIDConfig(base coro.FunctionIDConfig) coro.FunctionIDConfig {
	previousSynthetic := base.ResolveSynthetic
	previousLink := base.ResolveLinkIdentity
	previousPackage := base.CanonicalPackageKey
	previousLocalType := base.ResolveLocalTypeOwner
	base.ResolveSynthetic = func(fn *ssa.Function) (string, bool, error) {
		if u != nil {
			if key, ok := u.syntheticKeys[fn]; ok {
				return key, true, nil
			}
		}
		if previousSynthetic != nil {
			return previousSynthetic(fn)
		}
		return "", false, nil
	}
	base.ResolveLinkIdentity = func(fn *ssa.Function) (string, error) {
		if u != nil {
			fn = u.canonicalAlias(fn)
			if linkIdentity, ok := u.linkIdentities[fn]; ok {
				return linkIdentity, nil
			}
		}
		if previousLink != nil {
			return previousLink(fn)
		}
		return "", fmt.Errorf("function %q is absent from the frozen emission-universe link identities", fn.Name())
	}
	base.CanonicalPackageKey = func(pkg *types.Package) (string, error) {
		if u != nil {
			if owner := u.ownerOfTypes(pkg); owner != nil {
				return framedEmissionKey("cl-emission-package-v1", owner.identity), nil
			}
			path := llssa.PathOf(pkg)
			if u.pathDup[path] {
				return "", fmt.Errorf("package %q has no exact stable variant identity", path)
			}
		}
		if previousPackage != nil {
			return previousPackage(pkg)
		}
		return llssa.PathOf(pkg), nil
	}
	base.ResolveLocalTypeOwner = func(local *types.Named) (*ssa.Function, bool, error) {
		if u != nil && local != nil {
			u.localGenericMu.Lock()
			if owner := u.localGenericOwners[local]; owner != nil {
				u.localGenericMu.Unlock()
				return owner, true, nil
			}
			for source, canonical := range u.localGenericTypes {
				if canonical.typ == local {
					owner := u.localGenericOwners[source]
					u.localGenericMu.Unlock()
					if owner == nil {
						return nil, false, fmt.Errorf("canonical local type %q has no frozen definition owner", local.Obj().Name())
					}
					return owner, true, nil
				}
			}
			u.localGenericMu.Unlock()
		}
		if previousLocalType != nil {
			return previousLocalType(local)
		}
		return nil, false, nil
	}
	return base
}

// ValidatePlanCoverage verifies that plan contains an entry for every
// canonical exact function that cl may request. It performs no target or
// physical-ABI support checks; Compilation.preflightCoroPlan runs those only
// after this whole-universe coverage check succeeds.
func (u *EmissionUniverse) ValidatePlanCoverage(plan *coro.SSAPlan) error {
	if u == nil {
		return fmt.Errorf("coroutine plan coverage requires a prepared emission universe")
	}
	if plan == nil {
		return fmt.Errorf("coroutine plan coverage requires a compilation CoroPlan")
	}
	for _, fn := range u.functions {
		if _, ok := plan.FunctionPlan(fn); !ok {
			return fmt.Errorf("coroutine plan coverage: required final function %q is absent from the compilation CoroPlan", u.finalIdentity(fn))
		}
	}
	for _, planned := range plan.Functions() {
		if _, ok := u.required[planned.Function]; !ok {
			return fmt.Errorf("coroutine plan coverage: extra function %q is outside the prepared emission universe", emissionFunctionDiagnostic(planned.Function))
		}
	}
	return nil
}

// ValidateCoroPlan is the build-facing name for ValidatePlanCoverage.
func (u *EmissionUniverse) ValidateCoroPlan(plan *coro.SSAPlan) error {
	return u.ValidatePlanCoverage(plan)
}

func (u *EmissionUniverse) selectPackage(prepared *preparedEmissionPackage, pkg *ssa.Package, state pkgState, skips map[string]none, fromPatch bool) error {
	names := make([]string, 0, len(pkg.Members))
	for name := range pkg.Members {
		if _, skip := skips[name]; !skip {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		switch member := pkg.Members[name].(type) {
		case *ssa.Function:
			if strings.HasSuffix(member.Name(), "_trampoline") || member.TypeParams() != nil || member.TypeArgs() != nil {
				continue
			}
			if err := u.selectFunction(prepared, member, state, fromPatch); err != nil {
				return err
			}
		case *ssa.Type:
			if name, ok := member.Object().(*types.TypeName); ok && name.IsAlias() {
				continue
			}
			if err := u.selectTypeMethods(prepared, member.Type(), state, fromPatch, true); err != nil {
				return err
			}
			if err := u.selectTypeMethods(prepared, types.NewPointer(member.Type()), state, fromPatch, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (u *EmissionUniverse) selectTypeMethods(prepared *preparedEmissionPackage, typ types.Type, state pkgState, fromPatch, require bool) error {
	mset := u.goProg.MethodSets.MethodSet(typ)
	for i := 0; i < mset.Len(); i++ {
		fn := u.goProg.MethodValue(mset.At(i))
		if fn == nil {
			continue
		}
		if require {
			if err := u.selectFunction(prepared, fn, state, fromPatch); err != nil {
				return err
			}
		}
	}
	return nil
}

func (u *EmissionUniverse) selectABITypeMethods(prepared *preparedEmissionPackage, typ types.Type, state pkgState, fromPatch bool) ([]*ssa.Function, error) {
	base := types.Unalias(typ)
	for {
		pointer, ok := base.(*types.Pointer)
		if !ok {
			break
		}
		base = types.Unalias(pointer.Elem())
	}
	packageNamed := false
	if named, ok := base.(*types.Named); ok && (named.TypeArgs() == nil || named.TypeArgs().Len() == 0) {
		obj := named.Obj()
		packageNamed = obj != nil && obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope()
	}
	mset := u.goProg.MethodSets.MethodSet(typ)
	methods := make([]*ssa.Function, 0, mset.Len()*2)
	selectMethod := func(selection *types.Selection) error {
		fn := u.goProg.MethodValue(selection)
		if fn == nil {
			return fmt.Errorf("prepare emission universe: ABI method table for %v has no SSA implementation for method %q", typ, selection.Obj().Name())
		}
		if !packageNamed || functionNeedsLinkOnce(fn) {
			if err := u.selectFunction(prepared, fn, state, fromPatch); err != nil {
				return err
			}
		}
		fn = u.canonicalAlias(fn)
		if fn == nil {
			return fmt.Errorf("prepare emission universe: ABI method table for %v reached a cyclic method alias", typ)
		}
		if _, frozen := u.required[fn]; !frozen {
			return fmt.Errorf("prepare emission universe: ABI method table for %v references method %q outside the frozen emission universe", typ, fn.Name())
		}
		methods = append(methods, fn)
		return nil
	}
	for index := 0; index < mset.Len(); index++ {
		selection := mset.At(index)
		if err := selectMethod(selection); err != nil {
			return nil, err
		}

		// abiUncommonMethods uses the pointer-receiver method value as ifn for
		// every value-receiver selection. Freeze that exact wrapper alongside
		// tfn instead of assuming a later pointer descriptor happens to request
		// it as an unrelated side effect.
		sig, ok := selection.Type().(*types.Signature)
		if !ok || sig.Recv() == nil {
			return nil, fmt.Errorf("prepare emission universe: ABI method table for %v has a non-method selection %q", typ, selection.Obj().Name())
		}
		if _, pointerReceiver := selection.Recv().Underlying().(*types.Pointer); pointerReceiver {
			continue
		}
		pointerReceiver := types.NewPointer(sig.Recv().Type())
		pointerSelection := u.goProg.MethodSets.MethodSet(pointerReceiver).Lookup(selection.Obj().Pkg(), selection.Obj().Name())
		if pointerSelection == nil {
			return nil, fmt.Errorf("prepare emission universe: ABI method table for %v cannot resolve pointer ifn for method %q", typ, selection.Obj().Name())
		}
		if err := selectMethod(pointerSelection); err != nil {
			return nil, err
		}
	}
	return stableUniqueFunctions(methods), nil
}

func (u *EmissionUniverse) functionProvenance(prepared *preparedEmissionPackage, fn *ssa.Function) (pkgState, bool) {
	if prepared == nil || !prepared.hasPatch {
		return pkgNormal, false
	}
	if fn != nil && fn.Pkg == prepared.patch.Alt {
		return pkgInPatch, true
	}
	if fn != nil && fn.Signature != nil && fn.Signature.Recv() != nil {
		if named := recvNamedOk(fn.Signature.Recv().Type()); named != nil && named.Obj().Pkg() != nil {
			if state, fromPatch, known := u.packageTypeProvenance(prepared, named.Obj().Pkg()); known {
				return state, fromPatch
			}
		}
	}
	if fn != nil && fn.Parent() != nil {
		return u.functionProvenance(prepared, fn.Parent())
	}
	return pkgHasPatch, false
}

func (u *EmissionUniverse) packageTypeProvenance(prepared *preparedEmissionPackage, pkg *types.Package) (pkgState, bool, bool) {
	if prepared == nil || !prepared.hasPatch || pkg == nil {
		return pkgNormal, false, prepared != nil && !prepared.hasPatch
	}
	switch pkg {
	case prepared.altTypes:
		return pkgInPatch, true, true
	case prepared.oldTypes:
		return pkgHasPatch, false, true
	}
	return pkgNormal, false, false
}

func (u *EmissionUniverse) selectFunction(prepared *preparedEmissionPackage, fn *ssa.Function, state pkgState, fromPatch bool) error {
	if fn == nil {
		return nil
	}
	// A declared cross-package method/callee belongs to its exact SSA package,
	// not to the package whose type walk happened to discover it. Pkg-nil
	// promoted, structural, bound, and thunk wrappers remain use-site-owned,
	// matching context.funcName/codegen.
	if fn.Pkg != nil {
		if exact := u.packages[fn.Pkg]; exact != nil && exact != prepared {
			prepared = exact
			state, fromPatch = u.functionProvenance(exact, fn)
		}
	}
	key, managed, intrinsicName, ftype, err := u.managedSymbolInfo(prepared, fn, state)
	if err != nil {
		return err
	}
	functionKind := ignoredFunc
	intrinsicOpcode := 0
	if ftype == llgoInstr {
		opcode, ok := llgoInstrs[intrinsicName]
		if !ok {
			return fmt.Errorf("prepare emission universe: function %q resolves to unknown llgo intrinsic %q", fn.Name(), intrinsicName)
		}
		functionKind = llgoInstr
		intrinsicOpcode = opcode
	} else if managed {
		functionKind = managedKeyFunctionType(key)
	}
	canonical := fn
	if managed {
		if winner := prepared.winners[key]; winner != nil {
			if winner != fn {
				winnerFromPatch := prepared.fromPatch[winner]
				switch {
				case fromPatch && !winnerFromPatch:
					// Patch provenance wins even when a cross-package or runtime-type
					// walk happened to discover the original first.
					if err := u.replaceManagedWinner(prepared, key, winner, fn); err != nil {
						return err
					}
					canonical = fn
				case !fromPatch && winnerFromPatch:
					canonical = winner
					u.aliases[fn] = winner
				case managedKeyFunctionType(key) != goFunc:
					// C, Python, and llgo-intrinsic functions are declarations of
					// the resolved external operation. cl never emits their Go SSA
					// bodies, so one final kind/name/signature is one exact symbol.
					canonical = winner
					u.aliases[fn] = winner
				case u.samePromotedWrapperLinkIdentity(prepared, winner, fn):
					// Existing cl codegen merges these on the same LLVM symbol: local,
					// structurally identical, or generic promoted wrappers may be synthesized more than once, but
					// have one final name/signature and the same exact static callee.
					// This is a symbol-provenance rule, not a guessed layout/body
					// equivalence rule.
					canonical = winner
					u.aliases[fn] = winner
				default:
					return fmt.Errorf(
						"prepare emission universe: package %q (variant %q) has ambiguous managed symbol %q between %s [%s, patch=%t] and %s [%s, patch=%t]",
						prepared.pkgPath, prepared.identity, key,
						emissionFunctionDiagnostic(winner), u.functionProvenanceDiagnostic(prepared, winner), winnerFromPatch,
						emissionFunctionDiagnostic(fn), u.functionProvenanceDiagnostic(prepared, fn), fromPatch,
					)
				}
			}
		} else {
			prepared.winners[key] = fn
			prepared.fromPatch[fn] = fromPatch
			u.finalKeys[emissionFunctionOwnerKey{function: fn, owner: prepared}] = key
		}
	}
	if err := u.recordFunctionKind(fn, prepared, functionKind); err != nil {
		return err
	}
	if canonical != fn {
		if err := u.recordFunctionKind(canonical, prepared, functionKind); err != nil {
			return err
		}
	}
	if functionKind == llgoInstr {
		for _, target := range []*ssa.Function{fn, canonical} {
			ownerKey := emissionFunctionOwnerKey{function: target, owner: prepared}
			if previous, frozen := u.intrinsicOps[ownerKey]; frozen && previous != intrinsicOpcode {
				return fmt.Errorf("prepare emission universe: function %q has conflicting frozen llgo intrinsic opcodes", target.Name())
			}
			u.intrinsicOps[ownerKey] = intrinsicOpcode
		}
	}
	prepared.selected[fn] = none{}
	if u.fnOwners[fn] == nil {
		u.fnOwners[fn] = prepared
	}
	if _, known := u.fnStates[fn]; !known {
		u.fnStates[fn] = emissionFunctionState{state: state, fromPatch: fromPatch}
	}
	u.addRequired(canonical, prepared)
	return nil
}

func (u *EmissionUniverse) recordFunctionKind(fn *ssa.Function, owner *preparedEmissionPackage, kind int) error {
	if fn == nil || owner == nil {
		return fmt.Errorf("prepare emission universe: cannot record frontend function kind without an exact function and owner")
	}
	switch kind {
	case ignoredFunc, goFunc, cFunc, pyFunc, llgoInstr:
	default:
		return fmt.Errorf("prepare emission universe: function %q has unknown frontend function kind %d", fn.Name(), kind)
	}
	if u.functionKinds == nil {
		u.functionKinds = make(map[emissionFunctionOwnerKey]int)
	}
	key := emissionFunctionOwnerKey{function: fn, owner: owner}
	if previous, exists := u.functionKinds[key]; exists && previous != kind {
		return fmt.Errorf(
			"prepare emission universe: function %q has inconsistent frontend function kinds %d and %d for owner %q",
			fn.Name(), previous, kind, owner.identity,
		)
	}
	u.functionKinds[key] = kind
	return nil
}

func functionNeedsLinkOnce(fn *ssa.Function) bool {
	for current := fn; current != nil; current = current.Parent() {
		if hasGenericInstantiation(current) {
			return true
		}
	}
	return false
}

func (u *EmissionUniverse) samePromotedWrapperLinkIdentity(owner *preparedEmissionPackage, left, right *ssa.Function) bool {
	leftKind, rightKind := wrapperKind(left), wrapperKind(right)
	if leftKind == "" || leftKind != rightKind {
		return false
	}
	if u.structuralWrapperABIKey(owner, left) != u.structuralWrapperABIKey(owner, right) {
		return false
	}
	leftCall, _, leftErr := u.wrapperCallIdentity(owner, left, pkgNormal)
	rightCall, _, rightErr := u.wrapperCallIdentity(owner, right, pkgNormal)
	if leftErr != nil || rightErr != nil || leftCall == "" || leftCall != rightCall {
		return false
	}
	return deterministicSSABody(left) == deterministicSSABody(right)
}

func (u *EmissionUniverse) structuralWrapperABIKey(owner *preparedEmissionPackage, fn *ssa.Function) string {
	fields := []string{"wrapper-abi-v1", structuralEmissionTypeKey(u.effectiveType(owner, fn, fn.Signature))}
	for _, free := range fn.FreeVars {
		fields = append(fields, structuralEmissionTypeKey(u.effectiveType(owner, fn, free.Type())))
	}
	return framedEmissionKey(fields...)
}

func (u *EmissionUniverse) canonicalAlias(fn *ssa.Function) *ssa.Function {
	if fn == nil {
		return nil
	}
	next := u.aliases[fn]
	if next == nil {
		return fn
	}
	seen := map[*ssa.Function]none{fn: {}}
	for next != nil {
		if _, duplicate := seen[next]; duplicate {
			return nil
		}
		seen[next] = none{}
		fn = next
		next = u.aliases[fn]
	}
	return fn
}

// deterministicSSABody describes the complete frozen SSA body without using
// pointer identity or source filenames. Instruction.String includes operand
// structure; Field/FieldAddr indices are framed explicitly because promoted
// wrappers with different embedded-field offsets must never be merged.
func deterministicSSABody(fn *ssa.Function) string {
	if fn == nil {
		return "<nil>"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "blocks=%d;", len(fn.Blocks))
	for _, block := range fn.Blocks {
		if block == nil {
			text.WriteString("block=<nil>;")
			continue
		}
		fmt.Fprintf(&text, "block=%d;preds=", block.Index)
		for _, pred := range block.Preds {
			if pred == nil {
				text.WriteString("nil,")
			} else {
				fmt.Fprintf(&text, "%d,", pred.Index)
			}
		}
		text.WriteString(";succs=")
		for _, succ := range block.Succs {
			if succ == nil {
				text.WriteString("nil,")
			} else {
				fmt.Fprintf(&text, "%d,", succ.Index)
			}
		}
		text.WriteByte(';')
		for index, instr := range block.Instrs {
			fmt.Fprintf(&text, "instr=%d:%T:%s", index, instr, instr)
			switch instr := instr.(type) {
			case *ssa.Field:
				fmt.Fprintf(&text, ":field=%d", instr.Field)
			case *ssa.FieldAddr:
				fmt.Fprintf(&text, ":field=%d", instr.Field)
			}
			text.WriteByte(';')
		}
	}
	return text.String()
}

// structuralEmissionTypeKey expands local named types to their complete ABI
// shape while retaining package-level named types by linkage identity. This is
// used only by the prepared active universe; it does not change global
// funcName or report-only IR naming.
func structuralEmissionTypeKey(typ types.Type) string {
	builder := emissionTypeKeyBuilder{active: make(map[types.Type]int)}
	return builder.key(typ)
}

func structuralEmissionABITypeKey(typ types.Type) string {
	builder := emissionTypeKeyBuilder{active: make(map[types.Type]int), omitTupleNames: true}
	return builder.key(typ)
}

type emissionTypeKeyBuilder struct {
	active         map[types.Type]int
	next           int
	omitTupleNames bool
}

func (b *emissionTypeKeyBuilder) key(typ types.Type) string {
	if typ == nil {
		return framedEmissionKey("nil-type")
	}
	typ = types.Unalias(typ)
	if id, ok := b.active[typ]; ok {
		return framedEmissionKey("type-cycle", strconv.Itoa(id))
	}
	id := b.next
	b.next++
	b.active[typ] = id
	defer delete(b.active, typ)

	pkgKey := func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return llssa.PathOf(pkg)
	}
	switch typ := typ.(type) {
	case *types.Basic:
		return framedEmissionKey("basic", strconv.Itoa(int(typ.Kind())), typ.Name())
	case *types.Pointer:
		return framedEmissionKey("pointer", b.key(typ.Elem()))
	case *types.Array:
		return framedEmissionKey("array", strconv.FormatInt(typ.Len(), 10), b.key(typ.Elem()))
	case *types.Slice:
		return framedEmissionKey("slice", b.key(typ.Elem()))
	case *types.Map:
		return framedEmissionKey("map", b.key(typ.Key()), b.key(typ.Elem()))
	case *types.Chan:
		return framedEmissionKey("chan", strconv.Itoa(int(typ.Dir())), b.key(typ.Elem()))
	case *types.Named:
		obj := typ.Obj()
		fields := []string{"named"}
		packageLevel := false
		if obj != nil {
			fields = append(fields, pkgKey(obj.Pkg()), obj.Name())
			packageLevel = obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope()
		}
		if args := typ.TypeArgs(); args != nil {
			for i := 0; i < args.Len(); i++ {
				fields = append(fields, b.key(args.At(i)))
			}
		}
		if !packageLevel {
			fields = append(fields, "local-underlying", b.key(typ.Underlying()))
		}
		return framedEmissionKey(fields...)
	case *types.Struct:
		fields := []string{"struct", strconv.Itoa(typ.NumFields())}
		for i := 0; i < typ.NumFields(); i++ {
			field := typ.Field(i)
			fields = append(fields,
				pkgKey(field.Pkg()),
				field.Name(),
				strconv.FormatBool(field.Embedded()),
				typ.Tag(i),
				b.key(field.Type()),
			)
		}
		return framedEmissionKey(fields...)
	case *types.Tuple:
		fields := []string{"tuple", strconv.Itoa(typ.Len())}
		for i := 0; i < typ.Len(); i++ {
			variable := typ.At(i)
			if !b.omitTupleNames {
				fields = append(fields, pkgKey(variable.Pkg()), variable.Name())
			}
			fields = append(fields, b.key(variable.Type()))
		}
		return framedEmissionKey(fields...)
	case *types.Signature:
		fields := []string{"signature", strconv.FormatBool(typ.Variadic())}
		if typ.Recv() != nil {
			fields = append(fields, "recv", b.key(typ.Recv().Type()))
		}
		for _, params := range []*types.TypeParamList{typ.RecvTypeParams(), typ.TypeParams()} {
			fields = append(fields, "type-params")
			if params != nil {
				for i := 0; i < params.Len(); i++ {
					fields = append(fields, b.key(params.At(i)))
				}
			}
		}
		fields = append(fields, b.key(typ.Params()), b.key(typ.Results()))
		return framedEmissionKey(fields...)
	case *types.Interface:
		typ.Complete()
		fields := []string{"interface", strconv.Itoa(typ.NumMethods()), strconv.Itoa(typ.NumEmbeddeds())}
		for i := 0; i < typ.NumMethods(); i++ {
			method := typ.Method(i)
			fields = append(fields, pkgKey(method.Pkg()), method.Name(), b.key(method.Type()))
		}
		for i := 0; i < typ.NumEmbeddeds(); i++ {
			fields = append(fields, b.key(typ.EmbeddedType(i)))
		}
		return framedEmissionKey(fields...)
	case *types.TypeParam:
		obj := typ.Obj()
		name, pkg := "", ""
		if obj != nil {
			name, pkg = obj.Name(), pkgKey(obj.Pkg())
		}
		return framedEmissionKey("type-param", pkg, name, b.key(typ.Constraint()))
	case *types.Union:
		fields := []string{"union", strconv.Itoa(typ.Len())}
		for i := 0; i < typ.Len(); i++ {
			term := typ.Term(i)
			fields = append(fields, strconv.FormatBool(term.Tilde()), b.key(term.Type()))
		}
		return framedEmissionKey(fields...)
	default:
		return framedEmissionKey("other-type", types.TypeString(typ, func(pkg *types.Package) string { return pkgKey(pkg) }))
	}
}

func isLocallyMergedPromotedWrapper(fn *ssa.Function) bool {
	if fn == nil || !strings.HasPrefix(fn.Synthetic, "wrapper for ") {
		return false
	}
	if hasGenericInstantiation(fn) {
		return true
	}
	recv := fn.Signature.Recv()
	if recv == nil {
		return false
	}
	typ := types.Unalias(recv.Type())
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	if _, ok := typ.(*types.Struct); ok {
		return true
	}
	named, ok := typ.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Parent() != named.Obj().Pkg().Scope()
}

func soleStaticCallee(fn *ssa.Function) (*ssa.Function, bool) {
	var target *ssa.Function
	calls := 0
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			callee := call.Common().StaticCallee()
			if callee == nil || call.Common().IsInvoke() {
				return nil, false
			}
			target = callee
			calls++
		}
	}
	return target, calls == 1 && target != nil
}

func (u *EmissionUniverse) replaceManagedWinner(prepared *preparedEmissionPackage, key string, old, replacement *ssa.Function) error {
	if _, materialized := u.materialized[old]; materialized {
		return fmt.Errorf("prepare emission universe: cannot replace already-materialized original %s with late patch winner %s", emissionFunctionDiagnostic(old), emissionFunctionDiagnostic(replacement))
	}
	if u.ownerStateErr != nil {
		return u.ownerStateErr
	}
	type ownerMetadata struct {
		owner              *preparedEmissionPackage
		state              emissionFunctionState
		kind               int
		finalKey           string
		intrinsicOpcode    int
		hasIntrinsicOpcode bool
	}
	ownerSet := u.useOwners[old]
	if len(ownerSet) == 0 {
		return fmt.Errorf("prepare emission universe: cannot replace ownerless managed function %q", old.Name())
	}
	owners := make([]*preparedEmissionPackage, 0, len(ownerSet))
	for owner := range ownerSet {
		if owner == nil {
			return fmt.Errorf("prepare emission universe: cannot replace managed function %q with a nil frozen use owner", old.Name())
		}
		owners = append(owners, owner)
	}
	sort.SliceStable(owners, func(i, j int) bool {
		if owners[i].order != owners[j].order {
			return owners[i].order < owners[j].order
		}
		if owners[i].identity != owners[j].identity {
			return owners[i].identity < owners[j].identity
		}
		return owners[i].pkgPath < owners[j].pkgPath
	})
	metadata := make([]ownerMetadata, 0, len(owners))
	currentOwner := false
	for _, owner := range owners {
		state, stateOK := u.ownerStates[old][owner]
		if !stateOK {
			return fmt.Errorf("prepare emission universe: managed function %q has no frozen provenance for owner %q during replacement", old.Name(), owner.identity)
		}
		oldOwnerKey := emissionFunctionOwnerKey{function: old, owner: owner}
		kind, kindOK := u.functionKinds[oldOwnerKey]
		if !kindOK {
			return fmt.Errorf("prepare emission universe: managed function %q has no frozen frontend function kind for owner %q during replacement", old.Name(), owner.identity)
		}
		finalKey, finalKeyOK := u.finalKeys[oldOwnerKey]
		if !finalKeyOK || finalKey == "" {
			return fmt.Errorf("prepare emission universe: managed function %q has no frozen managed-symbol metadata for owner %q during replacement", old.Name(), owner.identity)
		}
		finalKind, _, _, valid := splitManagedSymbolKey(finalKey)
		if !valid || finalKind != kind {
			return fmt.Errorf("prepare emission universe: managed function %q has inconsistent frozen frontend kind and symbol metadata for owner %q during replacement", old.Name(), owner.identity)
		}
		replacementOwnerKey := emissionFunctionOwnerKey{function: replacement, owner: owner}
		intrinsicOpcode, hasIntrinsicOpcode := u.intrinsicOps[oldOwnerKey]
		if kind == llgoInstr && !hasIntrinsicOpcode {
			return fmt.Errorf("prepare emission universe: managed intrinsic %q has no frozen compiler opcode for owner %q during replacement", old.Name(), owner.identity)
		}
		if kind != llgoInstr && hasIntrinsicOpcode {
			return fmt.Errorf("prepare emission universe: non-intrinsic function %q has unexpected frozen compiler opcode for owner %q during replacement", old.Name(), owner.identity)
		}
		if previous, exists := u.functionKinds[replacementOwnerKey]; exists && previous != kind {
			return fmt.Errorf("prepare emission universe: replacement function %q has conflicting frozen frontend kinds %d and %d for owner %q", replacement.Name(), previous, kind, owner.identity)
		}
		if previous, exists := u.finalKeys[replacementOwnerKey]; exists && previous != finalKey {
			return fmt.Errorf("prepare emission universe: replacement function %q has conflicting frozen managed-symbol metadata for owner %q", replacement.Name(), owner.identity)
		}
		if previous, exists := u.intrinsicOps[replacementOwnerKey]; exists && (!hasIntrinsicOpcode || previous != intrinsicOpcode) {
			return fmt.Errorf("prepare emission universe: replacement function %q has conflicting frozen llgo intrinsic opcode for owner %q", replacement.Name(), owner.identity)
		}
		if previous, exists := u.ownerStates[replacement][owner]; exists {
			merged, err := mergeEmissionOwnerState(replacement, owner, previous, state)
			if err != nil {
				return err
			}
			state = merged
		}
		if owner == prepared {
			currentOwner = true
			if finalKey != key {
				return fmt.Errorf("prepare emission universe: patch replacement function %q changes frozen managed-symbol metadata for owner %q", replacement.Name(), owner.identity)
			}
		}
		metadata = append(metadata, ownerMetadata{
			owner: owner, state: state, kind: kind, finalKey: finalKey,
			intrinsicOpcode: intrinsicOpcode, hasIntrinsicOpcode: hasIntrinsicOpcode,
		})
	}
	if !currentOwner {
		return fmt.Errorf("prepare emission universe: managed function %q has no frozen metadata for replacement owner %q", old.Name(), prepared.identity)
	}
	prepared.winners[key] = replacement
	prepared.fromPatch[replacement] = true
	u.aliases[old] = replacement
	for alias, canonical := range u.aliases {
		if canonical == old {
			u.aliases[alias] = replacement
		}
	}
	if u.useOwners[replacement] == nil {
		u.useOwners[replacement] = make(map[*preparedEmissionPackage]none)
	}
	if u.ownerStates[replacement] == nil {
		u.ownerStates[replacement] = make(map[*preparedEmissionPackage]emissionFunctionState)
	}
	for _, item := range metadata {
		u.useOwners[replacement][item.owner] = none{}
		u.ownerStates[replacement][item.owner] = item.state
		oldOwnerKey := emissionFunctionOwnerKey{function: old, owner: item.owner}
		replacementOwnerKey := emissionFunctionOwnerKey{function: replacement, owner: item.owner}
		u.functionKinds[replacementOwnerKey] = item.kind
		u.finalKeys[replacementOwnerKey] = item.finalKey
		if item.hasIntrinsicOpcode {
			u.intrinsicOps[replacementOwnerKey] = item.intrinsicOpcode
		}
		delete(u.functionKinds, oldOwnerKey)
		delete(u.finalKeys, oldOwnerKey)
		delete(u.intrinsicOps, oldOwnerKey)
	}
	delete(u.useOwners, old)
	delete(u.ownerStates, old)
	delete(u.required, old)
	return nil
}

func (u *EmissionUniverse) aliasPackageMembers(prepared *preparedEmissionPackage, pkg *ssa.Package) error {
	names := make([]string, 0, len(pkg.Members))
	for name := range pkg.Members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		switch member := pkg.Members[name].(type) {
		case *ssa.Function:
			if strings.HasSuffix(member.Name(), "_trampoline") || member.TypeParams() != nil {
				continue
			}
			if err := u.aliasFunction(prepared, member); err != nil {
				return err
			}
		case *ssa.Type:
			if typeName, ok := member.Object().(*types.TypeName); ok && typeName.IsAlias() {
				continue
			}
			for _, typ := range []types.Type{member.Type(), types.NewPointer(member.Type())} {
				mset := u.goProg.MethodSets.MethodSet(typ)
				for i := 0; i < mset.Len(); i++ {
					if err := u.aliasFunction(prepared, u.goProg.MethodValue(mset.At(i))); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (u *EmissionUniverse) aliasFunction(prepared *preparedEmissionPackage, fn *ssa.Function) error {
	if fn == nil {
		return nil
	}
	if _, selected := prepared.selected[fn]; selected {
		return nil
	}
	if fn.Name() == "init" && fn.Signature.Recv() == nil {
		_, skipInit := prepared.skips["init"]
		if prepared.skipall || skipInit {
			key, managed, err := u.managedSymbolKey(prepared, fn, pkgNormal)
			if err != nil {
				return err
			}
			if managed {
				if winner := prepared.winners[key]; winner != nil && winner != fn {
					u.aliases[fn] = winner
					return nil
				}
			}
		}
		u.excluded[fn] = none{}
		return nil
	}
	key, managed, err := u.managedSymbolKey(prepared, fn, pkgNormal)
	if err != nil || !managed {
		return err
	}
	if winner := prepared.winners[key]; winner != nil && winner != fn {
		u.aliases[fn] = winner
		return nil
	}
	if prepared.skipall {
		// Replacement patches may intentionally leave references to declaration-
		// only old runtime helpers in other packages. cl can still request the
		// external symbol even though processPkg emits no old definition.
		if len(fn.Blocks) == 0 {
			return nil
		}
		u.excluded[fn] = none{}
		return nil
	}
	u.excluded[fn] = none{}
	return nil
}

func (u *EmissionUniverse) managedSymbolKey(prepared *preparedEmissionPackage, fn *ssa.Function, state pkgState) (string, bool, error) {
	key, managed, _, _, err := u.managedSymbolInfo(prepared, fn, state)
	return key, managed, err
}

func (u *EmissionUniverse) managedSymbolInfo(prepared *preparedEmissionPackage, fn *ssa.Function, state pkgState) (key string, managed bool, intrinsicName string, ftype int, err error) {
	name, sig, ftype, managed, err := u.classifiedManagedSymbol(prepared, fn, state)
	if err != nil || !managed {
		return "", managed, name, ftype, err
	}
	if isEmissionGeneratedWrapper(fn) {
		name, err = u.promotedWrapperPhysicalName(prepared, fn, state, name, sig)
		if err != nil {
			return "", false, "", ftype, err
		}
	}
	intrinsicName = ""
	if ftype == llgoInstr {
		intrinsicName = name
	}
	return managedSymbolKey(ftype, name, sig), true, intrinsicName, ftype, nil
}

func (u *EmissionUniverse) classifiedManagedSymbol(prepared *preparedEmissionPackage, fn *ssa.Function, state pkgState) (name, sig string, ftype int, managed bool, err error) {
	ctx := &context{
		prog:             u.prog,
		goFn:             fn,
		fset:             u.goProg.Fset,
		goProg:           u.goProg,
		goTyps:           prepared.pkgTypes,
		goPkg:            prepared.ssa,
		patches:          u.patches,
		loaded:           u.loadedPackages(),
		linkOnceFns:      make(map[*ssa.Function]none),
		state:            state,
		emissionUniverse: u,
	}
	_, name, ftype = ctx.funcName(fn)
	if ftype == ignoredFunc {
		return "", "", ftype, false, nil
	}
	if fn.Name() == "init" && fn.Signature.Recv() == nil && state == pkgHasPatch {
		name = initFnNameOfHasPatch(name)
	}
	patchedSignature, ok := ctx.patchType(fn.Signature).(*types.Signature)
	if !ok {
		return "", "", ftype, false, fmt.Errorf("prepare emission universe: patched function %q has non-signature type", fn.Name())
	}
	// Parameter and result names are source/debug metadata, not callable ABI.
	// Patch replacements may legitimately omit or rename them.
	sig = structuralEmissionABITypeKey(patchedSignature)
	if typeArgs := fn.TypeArgs(); len(typeArgs) != 0 {
		// A generic argument is not necessarily observable in the callable
		// signature (for example, func F[T any]() any).  funcName's legacy
		// spelling is also insufficient for substituted local named types, so
		// retain the exact canonical instance arguments in the managed key.
		// The receiver instance is already part of patchedSignature.
		fields := make([]string, 0, len(typeArgs)+2)
		fields = append(fields, "callable-instance-v1", sig)
		for _, argument := range typeArgs {
			fields = append(fields, structuralEmissionTypeKey(ctx.patchType(argument)))
		}
		sig = framedEmissionKey(fields...)
	}
	return name, sig, ftype, true, nil
}

func managedSymbolKey(ftype int, name, sig string) string {
	return strconv.Itoa(ftype) + "\x00" + name + "\x00" + sig
}

func managedKeyFunctionType(key string) int {
	prefix, _, ok := strings.Cut(key, "\x00")
	if !ok {
		return ignoredFunc
	}
	ftype, err := strconv.Atoi(prefix)
	if err != nil {
		return ignoredFunc
	}
	return ftype
}

func (u *EmissionUniverse) promotedWrapperPhysicalName(prepared *preparedEmissionPackage, fn *ssa.Function, state pkgState, legacyName, patchedSignature string) (string, error) {
	ownerIdentity := prepared.identity
	if functionNeedsLinkOnce(fn) {
		ownerIdentity = "linkonce"
	}
	physicalKey := emissionFunctionOwnerKey{function: fn, owner: prepared}
	if frozen := u.physicalNames[physicalKey]; frozen != "" {
		return frozen, nil
	}
	targetIdentity, _, err := u.wrapperCallIdentity(prepared, fn, state)
	if err != nil {
		return "", err
	}
	if targetIdentity == "" {
		targetIdentity = "no-sole-wrapper-call"
	}
	structuralSignature := u.structuralWrapperABIKey(prepared, fn)
	discriminator := framedEmissionKey(
		"cl-promoted-wrapper-physical-v1",
		wrapperKind(fn),
		ownerIdentity,
		targetIdentity,
		patchedSignature,
		structuralSignature,
		deterministicSSABody(fn),
	)
	name := legacyName + "$llgo$promoted$v1$" + emissionDigest(discriminator)
	u.physicalNames[physicalKey] = name
	if functionNeedsLinkOnce(fn) {
		if previous := u.linkOnceNames[fn]; previous != "" && previous != name {
			return "", fmt.Errorf("prepare emission universe: linkonce wrapper %q has owner-dependent physical names %q and %q", fn.Name(), previous, name)
		}
		u.linkOnceNames[fn] = name
	}
	return name, nil
}

func isEmissionGeneratedWrapper(fn *ssa.Function) bool {
	if fn == nil || fn.Pkg != nil {
		return false
	}
	return strings.HasPrefix(fn.Synthetic, "wrapper for ") ||
		strings.HasPrefix(fn.Synthetic, "bound method wrapper for ") ||
		strings.HasPrefix(fn.Synthetic, "thunk for ")
}

func wrapperKind(fn *ssa.Function) string {
	switch {
	case fn == nil:
		return ""
	case strings.HasPrefix(fn.Synthetic, "wrapper for "):
		return "promoted"
	case strings.HasPrefix(fn.Synthetic, "bound method wrapper for "):
		return "bound"
	case strings.HasPrefix(fn.Synthetic, "thunk for "):
		return "thunk"
	default:
		return ""
	}
}

func (u *EmissionUniverse) wrapperCallIdentity(prepared *preparedEmissionPackage, fn *ssa.Function, state pkgState) (identity string, static bool, err error) {
	var common *ssa.CallCommon
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			if common != nil {
				return "", false, nil
			}
			common = call.Common()
		}
	}
	if common == nil {
		return "", false, nil
	}
	if callee := common.StaticCallee(); callee != nil && !common.IsInvoke() {
		identity, err := u.canonicalCalleeLinkageIdentity(prepared, callee, state)
		return identity, true, err
	}
	if common.IsInvoke() && common.Method != nil {
		method := common.Method
		pkgPath := ""
		if method.Pkg() != nil {
			pkgPath = llssa.PathOf(method.Pkg())
		}
		return framedEmissionKey(
			"invoke-method-v1",
			pkgPath,
			method.Name(),
			structuralEmissionTypeKey(u.effectiveType(prepared, fn, method.Type())),
			structuralEmissionTypeKey(u.effectiveType(prepared, fn, common.Value.Type())),
		), false, nil
	}
	return "", false, nil
}

func (u *EmissionUniverse) canonicalCalleeLinkageIdentity(prepared *preparedEmissionPackage, fn *ssa.Function, state pkgState) (string, error) {
	fn = u.canonicalAlias(fn)
	if fn == nil {
		return "", fmt.Errorf("prepare emission universe: promoted-wrapper callee has cyclic canonical aliases")
	}
	if fn.Pkg != nil {
		if exact := u.packages[fn.Pkg]; exact != nil {
			prepared = exact
			state, _ = u.functionProvenance(exact, fn)
		}
	}
	name, sig, ftype, managed, err := u.classifiedManagedSymbol(prepared, fn, state)
	if err != nil {
		return "", err
	}
	if !managed {
		return "", fmt.Errorf("prepare emission universe: promoted wrapper %q calls ignored function %q", fn.Name(), fn.Name())
	}
	return framedEmissionKey("canonical-callee-v1", managedSymbolKey(ftype, name, sig)), nil
}

func emissionDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func (u *EmissionUniverse) materializeFunction(fn *ssa.Function) (bool, error) {
	if fn == nil {
		return false, nil
	}
	owners := make([]*preparedEmissionPackage, 0, len(u.useOwners[fn]))
	for owner := range u.useOwners[fn] {
		if _, done := u.materializedOwners[fn][owner]; !done {
			owners = append(owners, owner)
		}
	}
	if len(owners) == 0 {
		if len(u.useOwners[fn]) != 0 {
			return false, nil
		}
		owner := u.ownerOf(fn)
		if owner == nil {
			return false, fmt.Errorf("prepare emission universe: cannot determine emission package for SSA function %q", fn.String())
		}
		u.recordUseOwner(fn, owner, u.fnStates[fn])
		owners = append(owners, owner)
	}
	sort.SliceStable(owners, func(i, j int) bool { return owners[i].order < owners[j].order })
	progress := false
	for _, owner := range owners {
		if u.materializedOwners[fn] == nil {
			u.materializedOwners[fn] = make(map[*preparedEmissionPackage]none)
		}
		u.materializedOwners[fn][owner] = none{}
		u.materialized[fn] = none{}
		if err := u.materializeFunctionForOwner(fn, owner, u.ownerStates[fn][owner]); err != nil {
			return progress, err
		}
		progress = true
	}
	return progress, nil
}

func (u *EmissionUniverse) materializeFunctionForOwner(fn *ssa.Function, owner *preparedEmissionPackage, emissionState emissionFunctionState) error {
	ctx, err := u.functionABIContext(fn, owner)
	if err != nil {
		return err
	}
	_, _, ftype := ctx.funcName(fn)
	if ftype != goFunc {
		// compileFuncDecl retains the declaration/symbol classification but
		// returns before compiling anonymous children, operands, or ABI roots.
		return nil
	}
	if err := u.registerFunctionLocalGenericTypes(fn, owner); err != nil {
		return err
	}
	for _, child := range fn.AnonFuncs {
		if _, err := u.addResolvedRequired(child, owner, fn, emissionState); err != nil {
			return err
		}
	}
	isCgo := isCgoExternSymbol(fn)
	materializeTarget := func(target *ssa.Function, directCall bool) error {
		if target == nil {
			return nil
		}
		canonicalTarget, err := u.addResolvedRequired(target, owner, fn, emissionState)
		if err != nil {
			return err
		}
		if directCall || !u.isIntrinsic(canonicalTarget, owner) {
			return nil
		}
		key := intrinsicWrapperKey{owner: owner.ssa, intrinsic: canonicalTarget}
		wrapper := u.callWraps[key]
		if wrapper == nil {
			structuralKey, err := u.intrinsicWrapperStructuralKey(key)
			if err != nil {
				return err
			}
			wrapperName := canonicalTarget.Name() + "$wrapper$llgo$intrinsic$v1$" + emissionDigest(structuralKey)
			wrapper = ssawrap.MakeCallWrapperNamed(u.goProg, canonicalTarget, wrapperName)
			u.callWraps[key] = wrapper
			u.callWrapInfo[wrapper] = key
			u.syntheticKeys[wrapper] = structuralKey
		}
		if err := u.recordFunctionKind(wrapper, owner, goFunc); err != nil {
			return err
		}
		u.fnOwners[wrapper] = owner
		u.fnStates[wrapper] = emissionState
		u.addRequired(wrapper, owner)
		return nil
	}
	if isCgo {
		plan, err := u.cgoLoweringPlan(ctx, fn)
		if err != nil {
			return err
		}
		for _, call := range plan.calls {
			for _, root := range call.roots {
				target, ok := root.value.(*ssa.Function)
				if !ok {
					continue
				}
				if err := materializeTarget(target, root.directFunction); err != nil {
					return err
				}
			}
		}
		return u.materializeABITypeDemandsOfFunction(fn, owner, emissionState)
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if err := u.materializeLoweredRuntimeHelpers(ctx, fn, owner, emissionState, instr); err != nil {
				return err
			}
			if call, ok := instr.(ssa.CallInstruction); ok {
				roots, err := u.callValueRoots(ctx, call.Common())
				if err != nil {
					return fmt.Errorf("prepare emission universe: function %q: %w", fn.Name(), err)
				}
				for _, root := range roots {
					target, ok := root.value.(*ssa.Function)
					if !ok {
						continue
					}
					if err := materializeTarget(target, root.directFunction); err != nil {
						return err
					}
				}
				continue
			}
			if makeInterface, ok := instr.(*ssa.MakeInterface); ok && u.makeInterfaceConsumedByFuncAddress(makeInterface, ctx) {
				// funcAddr/funcPCABI0 inspect the MakeInterface SSA node and lower
				// its payload directly; the MakeInterface instruction itself is
				// deliberately elided.
				continue
			}
			var buf [10]*ssa.Value
			operands := instr.Operands(buf[:0])
			for _, operand := range operands {
				target, ok := (*operand).(*ssa.Function)
				if !ok || target == nil {
					continue
				}
				if err := materializeTarget(target, false); err != nil {
					return err
				}
			}
		}
	}
	return u.materializeABITypeDemandsOfFunction(fn, owner, emissionState)
}

func (u *EmissionUniverse) addResolvedRequired(fn *ssa.Function, owner *preparedEmissionPackage, caller *ssa.Function, state emissionFunctionState) (*ssa.Function, error) {
	fn = u.canonicalAlias(fn)
	if fn == nil {
		return nil, fmt.Errorf("prepare emission universe: reached function has cyclic canonical aliases")
	}
	if _, excluded := u.excluded[fn]; excluded {
		return nil, fmt.Errorf(
			"prepare emission universe: effective function %q reaches excluded original %q without an exact patch replacement",
			u.finalIdentity(caller), u.finalIdentity(fn),
		)
	}
	if fn.Pkg == nil {
		if err := u.selectFunction(owner, fn, state.state, state.fromPatch); err != nil {
			return nil, err
		}
		fn = u.canonicalAlias(fn)
		if fn == nil {
			return nil, fmt.Errorf("prepare emission universe: reached synthetic function has cyclic canonical aliases")
		}
		if _, excluded := u.excluded[fn]; excluded {
			return nil, fmt.Errorf(
				"prepare emission universe: effective function %q reaches excluded synthetic %q",
				u.finalIdentity(caller), u.finalIdentity(fn),
			)
		}
		return fn, nil
	}
	if fn.Pkg != nil {
		if exact := u.packages[fn.Pkg]; exact != nil {
			owner = exact
			resolvedState, fromPatch := u.functionProvenance(exact, fn)
			state = emissionFunctionState{state: resolvedState, fromPatch: fromPatch}
		} else if home := u.fnOwners[fn]; home != nil {
			owner = home
			state = u.fnStates[fn]
		}
	}
	if owner == nil {
		return nil, fmt.Errorf("prepare emission universe: reached function %q has no emission owner for frozen frontend metadata", fn.Name())
	}
	ownerKey := emissionFunctionOwnerKey{function: fn, owner: owner}
	functionKind, kindFrozen := u.functionKinds[ownerKey]
	_, finalKeyFrozen := u.finalKeys[ownerKey]
	if kindFrozen != finalKeyFrozen {
		// Ignored declarations deliberately have a frozen unclassified kind and
		// no managed symbol. Every emitted Go/C/Python function must freeze the
		// kind and managed provenance atomically during construction.
		if !kindFrozen || functionKind != ignoredFunc {
			return nil, fmt.Errorf(
				"prepare emission universe: reached function %q has partially frozen frontend metadata for owner %q (kind=%t, managed-symbol=%t)",
				fn.Name(), owner.identity, kindFrozen, finalKeyFrozen,
			)
		}
	}
	if !kindFrozen && !finalKeyFrozen {
		// Package selection records functions from explicit EmissionPackage
		// inputs. Nested closures and dependencies reached through an emitted
		// body may belong to an SSA package omitted from those inputs; select them
		// under their exact emitting owner now, before the universe is frozen.
		if err := u.selectFunction(owner, fn, state.state, state.fromPatch); err != nil {
			return nil, err
		}
		fn = u.canonicalAlias(fn)
		if fn == nil {
			return nil, fmt.Errorf("prepare emission universe: reached function has cyclic canonical aliases")
		}
		if _, excluded := u.excluded[fn]; excluded {
			return nil, fmt.Errorf(
				"prepare emission universe: effective function %q reaches excluded function %q",
				u.finalIdentity(caller), u.finalIdentity(fn),
			)
		}
		return fn, nil
	}
	if _, known := u.fnStates[fn]; !known {
		u.fnStates[fn] = state
	}
	u.addRequiredWithState(fn, owner, state)
	return fn, nil
}

func (u *EmissionUniverse) addRequired(fn *ssa.Function, owner *preparedEmissionPackage) {
	u.addRequiredWithState(fn, owner, u.fnStates[fn])
}

func (u *EmissionUniverse) addRequiredWithState(fn *ssa.Function, owner *preparedEmissionPackage, state emissionFunctionState) {
	if fn == nil {
		return
	}
	if fn.Pkg != nil {
		if exact := u.packages[fn.Pkg]; exact != nil {
			owner = exact
			resolvedState, fromPatch := u.functionProvenance(exact, fn)
			state = emissionFunctionState{state: resolvedState, fromPatch: fromPatch}
		} else if home := u.fnOwners[fn]; home != nil {
			owner = home
			state = u.fnStates[fn]
		}
	}
	u.recordUseOwner(fn, owner, state)
	if _, exists := u.required[fn]; exists {
		return
	}
	u.required[fn] = none{}
	u.functions = append(u.functions, fn)
	if u.fnOwners[fn] == nil {
		u.fnOwners[fn] = owner
	}
}

func (u *EmissionUniverse) recordUseOwner(fn *ssa.Function, owner *preparedEmissionPackage, state emissionFunctionState) {
	if fn == nil || owner == nil {
		return
	}
	owners := u.useOwners[fn]
	if owners == nil {
		owners = make(map[*preparedEmissionPackage]none)
		u.useOwners[fn] = owners
	}
	owners[owner] = none{}
	states := u.ownerStates[fn]
	if states == nil {
		states = make(map[*preparedEmissionPackage]emissionFunctionState)
		u.ownerStates[fn] = states
	}
	if previous, exists := states[owner]; exists {
		merged, err := mergeEmissionOwnerState(fn, owner, previous, state)
		if err != nil {
			if u.ownerStateErr == nil {
				u.ownerStateErr = err
			}
			return
		}
		states[owner] = merged
		return
	}
	states[owner] = state
}

func mergeEmissionOwnerState(fn *ssa.Function, owner *preparedEmissionPackage, previous, incoming emissionFunctionState) (emissionFunctionState, error) {
	switch {
	case previous == incoming:
		return previous, nil
	case previous.fromPatch && !incoming.fromPatch:
		return previous, nil
	case incoming.fromPatch && !previous.fromPatch:
		return incoming, nil
	case previous.state == pkgNormal:
		// pkgNormal is the provenance fallback for an anonymous type. An exact
		// original/alt observation is stronger.
		return incoming, nil
	case incoming.state == pkgNormal:
		return previous, nil
	default:
		name, pkgPath := "<nil>", "<nil>"
		if fn != nil {
			name = fn.Name()
		}
		if owner != nil {
			pkgPath = owner.pkgPath
		}
		return emissionFunctionState{}, fmt.Errorf(
			"prepare emission universe: conflicting emission provenance for %q in package %q: (%d,%t) and (%d,%t)",
			name, pkgPath, previous.state, previous.fromPatch, incoming.state, incoming.fromPatch,
		)
	}
}

func (u *EmissionUniverse) ownerOf(fn *ssa.Function) *preparedEmissionPackage {
	if owner := u.fnOwners[fn]; owner != nil {
		return owner
	}
	if fn != nil && fn.Pkg != nil {
		if owner := u.packages[fn.Pkg]; owner != nil {
			u.fnOwners[fn] = owner
			return owner
		}
	}
	if fn != nil {
		if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
			if owner := u.ownerOfTypes(obj.Pkg()); owner != nil {
				u.fnOwners[fn] = owner
				return owner
			}
		}
		if recv := fn.Signature.Recv(); recv != nil {
			if named := recvNamedOk(recv.Type()); named != nil && named.Obj().Pkg() != nil {
				if owner := u.ownerOfTypes(named.Obj().Pkg()); owner != nil {
					u.fnOwners[fn] = owner
					return owner
				}
			}
		}
	}
	path := functionPackagePath(fn)
	if owner := u.byPath[path]; owner != nil {
		u.fnOwners[fn] = owner
		return owner
	}
	return nil
}

func (u *EmissionUniverse) ownerOfTypes(pkg *types.Package) *preparedEmissionPackage {
	if pkg == nil {
		return nil
	}
	if owner := u.byTypes[pkg]; owner != nil {
		return owner
	}
	return u.byPath[llssa.PathOf(pkg)]
}

func functionPackagePath(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		return llssa.PathOf(fn.Pkg.Pkg)
	}
	if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
		return llssa.PathOf(obj.Pkg())
	}
	if recv := fn.Signature.Recv(); recv != nil {
		if named := recvNamedOk(recv.Type()); named != nil && named.Obj().Pkg() != nil {
			return llssa.PathOf(named.Obj().Pkg())
		}
	}
	return ""
}

func (u *EmissionUniverse) isIntrinsic(fn *ssa.Function, owner *preparedEmissionPackage) bool {
	if owner == nil {
		return false
	}
	ctx := &context{
		prog:        u.prog,
		fset:        u.goProg.Fset,
		goProg:      u.goProg,
		goTyps:      owner.pkgTypes,
		goPkg:       owner.ssa,
		patches:     u.patches,
		loaded:      u.loadedPackages(),
		linkOnceFns: make(map[*ssa.Function]none),
	}
	_, _, ftype := ctx.funcName(fn)
	return ftype == llgoInstr
}

func (u *EmissionUniverse) loadedPackages() map[*types.Package]*pkgInfo {
	loaded := map[*types.Package]*pkgInfo{types.Unsafe: {kind: PkgDeclOnly}}
	if u == nil || u.goProg == nil {
		return loaded
	}
	for _, pkg := range u.goProg.AllPackages() {
		if pkg == nil || pkg.Pkg == nil {
			continue
		}
		loaded[pkg.Pkg] = &pkgInfo{kind: pkgKindByPath(llssa.PathOf(pkg.Pkg))}
	}
	for _, prepared := range u.packages {
		loaded[prepared.oldTypes] = &pkgInfo{kind: pkgKindByPath(prepared.pkgPath)}
		loaded[prepared.pkgTypes] = &pkgInfo{kind: pkgKindByPath(prepared.pkgPath)}
		if prepared.altTypes != nil {
			loaded[prepared.altTypes] = &pkgInfo{kind: pkgKindByPath(prepared.pkgPath)}
		}
	}
	return loaded
}

func (u *EmissionUniverse) typeProvenance(owner *preparedEmissionPackage, typ types.Type) (pkgState, bool, bool) {
	if owner == nil || !owner.hasPatch {
		return pkgNormal, false, owner != nil
	}
	seen := make(map[types.Type]none)
	var alt, original bool
	var visit func(types.Type)
	visit = func(typ types.Type) {
		if typ == nil || alt {
			return
		}
		if _, ok := seen[typ]; ok {
			return
		}
		seen[typ] = none{}
		switch typ := types.Unalias(typ).(type) {
		case *types.Pointer:
			visit(typ.Elem())
		case *types.Named:
			if obj := typ.Obj(); obj != nil {
				switch obj.Pkg() {
				case owner.altTypes:
					alt = true
				case owner.oldTypes:
					original = true
				}
			}
			visit(typ.Underlying())
		case *types.Struct:
			for index := 0; index < typ.NumFields(); index++ {
				visit(typ.Field(index).Type())
			}
		case *types.Array:
			visit(typ.Elem())
		case *types.Slice:
			visit(typ.Elem())
		case *types.Map:
			visit(typ.Key())
			visit(typ.Elem())
		case *types.Chan:
			visit(typ.Elem())
		}
	}
	visit(typ)
	if alt {
		return pkgInPatch, true, true
	}
	if original {
		return pkgHasPatch, false, true
	}
	return pkgNormal, false, false
}

func (u *EmissionUniverse) intrinsicWrapper(owner *ssa.Package, fn *ssa.Function) (*ssa.Function, bool) {
	if u == nil || owner == nil || fn == nil {
		return nil, false
	}
	fn = u.canonicalAlias(fn)
	if fn == nil {
		return nil, false
	}
	wrapper, ok := u.callWraps[intrinsicWrapperKey{owner: owner, intrinsic: fn}]
	return wrapper, ok
}

func (u *EmissionUniverse) effectiveType(owner *preparedEmissionPackage, fn *ssa.Function, typ types.Type) types.Type {
	if owner == nil || typ == nil {
		return typ
	}
	ctx := &context{
		prog:             u.prog,
		goFn:             fn,
		fset:             u.goProg.Fset,
		goProg:           u.goProg,
		goTyps:           owner.pkgTypes,
		goPkg:            owner.ssa,
		patches:          u.patches,
		loaded:           u.loadedPackages(),
		linkOnceFns:      make(map[*ssa.Function]none),
		emissionUniverse: u,
	}
	return ctx.patchType(typ)
}

// registerFunctionLocalGenericTypes records the instantiated lexical owner of
// every exact local named type visible in a lowered SSA body. A local type can
// escape its defining function as a type argument to another generic helper;
// that helper has neither a Parent edge nor source-position containment back
// to the definition, so later patching must consult this frozen registry.
func (u *EmissionUniverse) registerFunctionLocalGenericTypes(fn *ssa.Function, owner *preparedEmissionPackage) error {
	ctx, err := u.functionABIContext(fn, owner)
	if err != nil {
		return err
	}
	seen := make(map[types.Type]none)
	registrations := make(map[*types.Named]*ssa.Function)
	var visit func(types.Type)
	var visitTuple func(*types.Tuple)
	visitTuple = func(tuple *types.Tuple) {
		if tuple == nil {
			return
		}
		for index := 0; index < tuple.Len(); index++ {
			visit(tuple.At(index).Type())
		}
	}
	visit = func(typ types.Type) {
		if typ == nil {
			return
		}
		if _, ok := seen[typ]; ok {
			return
		}
		seen[typ] = none{}
		switch typ := typ.(type) {
		case *types.Alias:
			visit(types.Unalias(typ))
		case *types.Pointer:
			visit(typ.Elem())
		case *types.Array:
			visit(typ.Elem())
		case *types.Slice:
			visit(typ.Elem())
		case *types.Map:
			visit(typ.Key())
			visit(typ.Elem())
		case *types.Chan:
			visit(typ.Elem())
		case *types.Struct:
			for index := 0; index < typ.NumFields(); index++ {
				visit(typ.Field(index).Type())
			}
		case *types.Tuple:
			visitTuple(typ)
		case *types.Signature:
			if recv := typ.Recv(); recv != nil {
				visit(recv.Type())
			}
			visitTuple(typ.Params())
			visitTuple(typ.Results())
		case *types.Interface:
			typ.Complete()
			for index := 0; index < typ.NumExplicitMethods(); index++ {
				visit(typ.ExplicitMethod(index).Type())
			}
			for index := 0; index < typ.NumEmbeddeds(); index++ {
				visit(typ.EmbeddedType(index))
			}
		case *types.Named:
			for index := 0; index < typ.TypeArgs().Len(); index++ {
				visit(typ.TypeArgs().At(index))
			}
			if localCtx := ctx.localGenericTypeContext(typ); localCtx != nil {
				registrations[typ] = localCtx.goFn
				visit(typ.Underlying())
			}
		}
	}

	visit(fn.Signature)
	for _, arg := range fn.TypeArgs() {
		visit(arg)
	}
	for _, param := range fn.Params {
		visit(param.Type())
	}
	for _, free := range fn.FreeVars {
		visit(free.Type())
	}
	for _, local := range fn.Locals {
		visit(local.Type())
	}
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if value, ok := instruction.(ssa.Value); ok {
				visit(value.Type())
			}
			var operands [10]*ssa.Value
			for _, operand := range instruction.Operands(operands[:0]) {
				if operand != nil && *operand != nil {
					visit((*operand).Type())
					if function, ok := (*operand).(*ssa.Function); ok {
						// A generic instance's callable type may erase every
						// type argument. Preserve local-definition provenance
						// from the exact callee operand before that instance is
						// selected and assigned a managed symbol.
						for _, argument := range function.TypeArgs() {
							visit(argument)
						}
					}
				}
			}
		}
	}

	u.localGenericMu.Lock()
	defer u.localGenericMu.Unlock()
	if u.localGenericOwners == nil {
		u.localGenericOwners = make(map[*types.Named]*ssa.Function)
	}
	for source, registration := range registrations {
		if previous, ok := u.localGenericOwners[source]; ok {
			if previous != registration {
				return fmt.Errorf("generic local type %v has conflicting definition functions %q and %q", source, previous, registration)
			}
			continue
		}
		u.localGenericOwners[source] = registration
	}
	return nil
}

func (u *EmissionUniverse) registeredLocalGenericContext(base *context, source *types.Named) *context {
	if u == nil || base == nil || source == nil {
		return nil
	}
	u.localGenericMu.Lock()
	definition, ok := u.localGenericOwners[source]
	u.localGenericMu.Unlock()
	if !ok {
		return nil
	}
	ctx := *base
	ctx.goFn = definition
	return &ctx
}

func (u *EmissionUniverse) cachedLocalGenericNamed(source *types.Named) *types.Named {
	if u == nil || source == nil {
		return nil
	}
	u.localGenericMu.Lock()
	cached := u.localGenericTypes[source]
	u.localGenericMu.Unlock()
	return cached.typ
}

func (u *EmissionUniverse) emissionTypeArgName(ctx *context, typ types.Type) string {
	if u == nil || ctx == nil {
		return types.TypeString(typ, reflectTypeArgPkgPath)
	}
	u.localGenericMu.Lock()
	defer u.localGenericMu.Unlock()
	return u.emissionTypeArgNameLocked(ctx, typ)
}

func (u *EmissionUniverse) emissionTypeArgNameLocked(ctx *context, typ types.Type) string {
	typ = u.patchEmissionTypeGraphLocked(ctx, typ)
	switch typ := typ.(type) {
	case *types.Alias:
		return u.emissionTypeArgNameLocked(ctx, types.Unalias(typ))
	case *types.Basic:
		return typ.String()
	case *types.Named:
		nameCtx := u.localGenericDefinitionContextLocked(ctx, typ)
		if nameCtx == nil {
			nameCtx = ctx
		}
		name := u.localNamedNameLocked(nameCtx, typ, nameCtx.isLocalType(typ.Obj()))
		if pkg := typ.Obj().Pkg(); pkg != nil {
			return reflectTypeArgPkgPath(pkg) + "." + name
		}
		return name
	case *types.Pointer:
		return "*" + u.emissionTypeArgNameLocked(ctx, typ.Elem())
	case *types.Slice:
		return "[]" + u.emissionTypeArgNameLocked(ctx, typ.Elem())
	case *types.Array:
		return fmt.Sprintf("[%v]%s", typ.Len(), u.emissionTypeArgNameLocked(ctx, typ.Elem()))
	case *types.Map:
		return fmt.Sprintf("map[%s]%s", u.emissionTypeArgNameLocked(ctx, typ.Key()), u.emissionTypeArgNameLocked(ctx, typ.Elem()))
	case *types.Chan:
		direction := chanDirName(typ.Dir())
		elem := u.emissionTypeArgNameLocked(ctx, typ.Elem())
		if typ.Dir() == types.SendRecv {
			if channel, ok := typ.Elem().(*types.Chan); ok && channel.Dir() == types.RecvOnly {
				elem = "(" + elem + ")"
			}
		}
		return fmt.Sprintf("%s %s", direction, elem)
	default:
		return types.TypeString(typ, reflectTypeArgPkgPath)
	}
}

func (u *EmissionUniverse) localNamedNameLocked(ctx *context, typ *types.Named, suffix bool) string {
	obj := typ.Obj()
	name := obj.Name()
	if isPatchedLocalGenericName(name) {
		if suffix {
			if ordinal := ctx.localTypeOrdinal(obj); ordinal != 0 {
				name += "·" + strconv.Itoa(ordinal)
			}
		}
		return name
	}
	var outer []string
	if ctx.goFn != nil && len(ctx.goFn.TypeArgs()) != 0 && ctx.isGenericLocalType(obj) {
		args := ctx.goFn.TypeArgs()
		outer = make([]string, len(args))
		for index, arg := range args {
			outer[index] = u.emissionTypeArgNameLocked(ctx, arg)
		}
	}
	own := make([]string, typ.TypeArgs().Len())
	for index := range own {
		own[index] = u.emissionTypeArgNameLocked(ctx, typ.TypeArgs().At(index))
	}
	switch {
	case len(outer) != 0 && len(own) != 0:
		name += "[" + strings.Join(outer, ",") + ";" + strings.Join(own, ",") + "]"
	case len(outer) != 0:
		name += "[" + strings.Join(outer, ",") + "]"
	case len(own) != 0:
		name += "[" + strings.Join(own, ",") + "]"
	}
	if suffix {
		if ordinal := ctx.localTypeOrdinal(obj); ordinal != 0 {
			name += "·" + strconv.Itoa(ordinal)
		}
	}
	return name
}

func (u *EmissionUniverse) localGenericDefinitionContextLocked(base *context, source *types.Named) *context {
	if base == nil || source == nil {
		return nil
	}
	if local := base.localGenericTypeContext(source); local != nil {
		return local
	}
	definition := u.localGenericOwners[source]
	if definition == nil {
		return nil
	}
	ctx := *base
	ctx.goFn = definition
	return &ctx
}

func (u *EmissionUniverse) canonicalLocalGenericNamed(ctx *context, source *types.Named) *types.Named {
	if u == nil || ctx == nil || ctx.goFn == nil || source == nil {
		return nil
	}
	u.localGenericMu.Lock()
	defer u.localGenericMu.Unlock()
	if u.localGenericTypes == nil {
		u.localGenericTypes = make(map[*types.Named]emissionLocalGenericType)
	}
	name := u.localNamedNameLocked(ctx, source, false)
	return u.canonicalLocalGenericNamedLocked(ctx, source, name)
}

func (u *EmissionUniverse) canonicalLocalGenericNamedLocked(ctx *context, source *types.Named, name string) *types.Named {
	if cached, ok := u.localGenericTypes[source]; ok {
		if cached.name != name {
			panic(fmt.Sprintf("generic local type %v acquired conflicting canonical names %q and %q", source, cached.name, name))
		}
		return cached.typ
	}
	obj := source.Obj()
	// Register an incomplete shell in the locked construction graph before
	// rebuilding the underlying shape. Every generic-local named edge must use
	// its own canonical shell: ssa.Builder.abiType computes descriptor names
	// before applying its patch callback, so leaving any source local in the
	// graph can alias Generic[int] and Generic[string] through the old name.
	canonical := types.NewNamed(types.NewTypeName(obj.Pos(), obj.Pkg(), name, nil), nil, nil)
	u.localGenericTypes[source] = emissionLocalGenericType{name: name, typ: canonical}
	canonical.SetUnderlying(u.patchEmissionTypeGraphLocked(ctx, source.Underlying()))
	return canonical
}

func (u *EmissionUniverse) patchEmissionTypeGraph(ctx *context, root types.Type) (types.Type, bool) {
	if u == nil || ctx == nil || root == nil {
		return root, false
	}
	u.localGenericMu.Lock()
	defer u.localGenericMu.Unlock()
	patched := u.patchEmissionTypeGraphLocked(ctx, root)
	return patched, patched != root
}

func (u *EmissionUniverse) patchEmissionTypeGraphLocked(ctx *context, root types.Type) types.Type {
	return replaceEmissionLocalGenericNamed(root, func(named *types.Named) *types.Named {
		if named == nil || isPatchedLocalGenericName(named.Obj().Name()) {
			return nil
		}
		nestedCtx := u.localGenericDefinitionContextLocked(ctx, named)
		if nestedCtx == nil {
			if named.TypeArgs().Len() == 0 {
				return nil
			}
			args := make([]types.Type, named.TypeArgs().Len())
			changed := false
			for index := range args {
				arg := named.TypeArgs().At(index)
				args[index] = u.patchEmissionTypeGraphLocked(ctx, arg)
				changed = changed || args[index] != arg
			}
			if !changed {
				return nil
			}
			if u.genericNamedTypes == nil {
				u.genericNamedTypes = make(map[*types.Named]*types.Named)
			}
			if cached := u.genericNamedTypes[named]; cached != nil {
				return cached
			}
			// The original instance was already type-checked. Canonical local
			// shells may still be incomplete while a recursive graph is being
			// assembled, so constraint revalidation here would observe a
			// transient method set and reject an otherwise valid instance.
			instantiated, err := types.Instantiate(nil, named.Origin(), args, false)
			if err != nil {
				panic(fmt.Sprintf("cannot canonicalize instantiated type %v: %v", named, err))
			}
			canonical, ok := instantiated.(*types.Named)
			if !ok {
				panic(fmt.Sprintf("canonical instantiated type %v has type %T", instantiated, instantiated))
			}
			u.genericNamedTypes[named] = canonical
			return canonical
		}
		return u.canonicalLocalGenericNamedLocked(nestedCtx, named, u.localNamedNameLocked(nestedCtx, named, false))
	})
}

// replaceEmissionLocalGenericNamed rebuilds anonymous container types only
// where canonicalize replaces a named edge. Package-level named types remain
// opaque, while all generic-local named dependencies join one canonical graph.
func replaceEmissionLocalGenericNamed(root types.Type, canonicalize func(*types.Named) *types.Named) types.Type {
	memo := make(map[types.Type]types.Type)
	var replace func(types.Type) types.Type
	var replaceTuple func(*types.Tuple) *types.Tuple
	var replaceVar func(*types.Var) *types.Var
	var replaceSignature func(*types.Signature, bool) *types.Signature

	replaceVar = func(variable *types.Var) *types.Var {
		if variable == nil {
			return nil
		}
		typ := replace(variable.Type())
		if typ == variable.Type() {
			return variable
		}
		return types.NewVar(variable.Pos(), variable.Pkg(), variable.Name(), typ)
	}
	replaceTuple = func(tuple *types.Tuple) *types.Tuple {
		if tuple == nil {
			return nil
		}
		variables := make([]*types.Var, tuple.Len())
		changed := false
		for index := 0; index < tuple.Len(); index++ {
			variables[index] = replaceVar(tuple.At(index))
			changed = changed || variables[index] != tuple.At(index)
		}
		if !changed {
			return tuple
		}
		return types.NewTuple(variables...)
	}
	replaceSignature = func(signature *types.Signature, includeReceiver bool) *types.Signature {
		receiver := signature.Recv()
		if includeReceiver {
			receiver = replaceVar(receiver)
		}
		params, results := replaceTuple(signature.Params()), replaceTuple(signature.Results())
		if receiver == signature.Recv() && params == signature.Params() && results == signature.Results() {
			return signature
		}
		// Generic function types and generic methods cannot appear in a
		// concrete local type's underlying ABI graph. Preserve an invalid
		// frontend signature rather than rebinding its type parameters.
		if signature.TypeParams().Len() != 0 || signature.RecvTypeParams().Len() != 0 {
			return signature
		}
		return types.NewSignatureType(receiver, nil, nil, params, results, signature.Variadic())
	}
	replace = func(typ types.Type) types.Type {
		if typ == nil {
			return nil
		}
		if cached := memo[typ]; cached != nil {
			return cached
		}

		var rebuilt types.Type = typ
		switch typ := typ.(type) {
		case *types.Alias:
			actual := types.Unalias(typ)
			if replacement := replace(actual); replacement != actual {
				rebuilt = replacement
			}
		case *types.Pointer:
			if elem := replace(typ.Elem()); elem != typ.Elem() {
				rebuilt = types.NewPointer(elem)
			}
		case *types.Array:
			if elem := replace(typ.Elem()); elem != typ.Elem() {
				rebuilt = types.NewArray(elem, typ.Len())
			}
		case *types.Slice:
			if elem := replace(typ.Elem()); elem != typ.Elem() {
				rebuilt = types.NewSlice(elem)
			}
		case *types.Map:
			key, elem := replace(typ.Key()), replace(typ.Elem())
			if key != typ.Key() || elem != typ.Elem() {
				rebuilt = types.NewMap(key, elem)
			}
		case *types.Chan:
			if elem := replace(typ.Elem()); elem != typ.Elem() {
				rebuilt = types.NewChan(typ.Dir(), elem)
			}
		case *types.Struct:
			fields := make([]*types.Var, typ.NumFields())
			tags := make([]string, typ.NumFields())
			changed := false
			for index := 0; index < typ.NumFields(); index++ {
				field := typ.Field(index)
				fieldType := replace(field.Type())
				if fieldType == field.Type() {
					fields[index] = field
				} else {
					fields[index] = types.NewField(field.Pos(), field.Pkg(), field.Name(), fieldType, field.Anonymous())
					changed = true
				}
				tags[index] = typ.Tag(index)
			}
			if changed {
				rebuilt = types.NewStruct(fields, tags)
			}
		case *types.Tuple:
			rebuilt = replaceTuple(typ)
		case *types.Signature:
			rebuilt = replaceSignature(typ, true)
		case *types.Interface:
			typ.Complete()
			methods := make([]*types.Func, typ.NumExplicitMethods())
			embeddeds := make([]types.Type, typ.NumEmbeddeds())
			changed := false
			for index := range methods {
				method := typ.ExplicitMethod(index)
				methodType := replaceSignature(method.Type().(*types.Signature), false)
				if methodType == method.Type() {
					methods[index] = method
				} else {
					methods[index] = types.NewFunc(method.Pos(), method.Pkg(), method.Name(), methodType)
					changed = true
				}
			}
			for index := range embeddeds {
				embeddeds[index] = replace(typ.EmbeddedType(index))
				changed = changed || embeddeds[index] != typ.EmbeddedType(index)
			}
			if changed {
				iface := types.NewInterfaceType(methods, embeddeds)
				if typ.IsImplicit() {
					iface.MarkImplicit()
				}
				rebuilt = iface.Complete()
			}
		case *types.Named:
			if canonical := canonicalize(typ); canonical != nil {
				rebuilt = canonical
			}
		}
		memo[typ] = rebuilt
		return rebuilt
	}
	return replace(root)
}

func (u *EmissionUniverse) checkPackage(pkg *ssa.Package, files []*ast.File, patches Patches) (*preparedEmissionPackage, error) {
	if u == nil {
		return nil, fmt.Errorf("coroutine entry resolution requires a prepared emission universe")
	}
	prepared := u.packages[pkg]
	if prepared == nil {
		return nil, fmt.Errorf("package %q is absent from the prepared emission universe", llssa.PathOf(pkg.Pkg))
	}
	if len(files) != len(prepared.files) {
		return nil, fmt.Errorf("package %q syntax changed after emission-universe preparation", prepared.pkgPath)
	}
	for i := range files {
		if files[i] != prepared.files[i] {
			return nil, fmt.Errorf("package %q syntax changed after emission-universe preparation", prepared.pkgPath)
		}
	}
	patch, hasPatch := patches[prepared.pkgPath]
	if hasPatch != prepared.hasPatch || hasPatch && (patch.Alt != prepared.patch.Alt || patch.Types != prepared.patch.Types) {
		return nil, fmt.Errorf("package %q patch changed after emission-universe preparation", prepared.pkgPath)
	}
	scan := &context{prog: u.prog, skips: make(map[string]none)}
	scan.initFiles(prepared.pkgPath, files, prepared.pkgTypes.Name() == "C")
	if scan.skipall != prepared.skipall || !sameNoneMap(scan.skips, prepared.skips) {
		return nil, fmt.Errorf("package %q skip directives changed after emission-universe preparation", prepared.pkgPath)
	}
	return prepared, nil
}

func cloneNoneMap(src map[string]none) map[string]none {
	if len(src) == 0 {
		return make(map[string]none)
	}
	dst := make(map[string]none, len(src))
	for key := range src {
		dst[key] = none{}
	}
	return dst
}

func sameNoneMap(a, b map[string]none) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, ok := b[key]; !ok {
			return false
		}
	}
	return true
}

func stableUniqueFunctions(functions []*ssa.Function) []*ssa.Function {
	seen := make(map[*ssa.Function]none, len(functions))
	out := functions[:0]
	for _, fn := range functions {
		if fn == nil {
			continue
		}
		if _, ok := seen[fn]; ok {
			continue
		}
		seen[fn] = none{}
		out = append(out, fn)
	}
	return out
}

func filterRequiredFunctions(functions []*ssa.Function, required map[*ssa.Function]none) []*ssa.Function {
	out := functions[:0]
	for _, fn := range functions {
		if _, ok := required[fn]; ok {
			out = append(out, fn)
		}
	}
	return out
}

func (u *EmissionUniverse) intrinsicWrapperStructuralKey(info intrinsicWrapperKey) (string, error) {
	owner := u.packages[info.owner]
	if owner == nil {
		return "", fmt.Errorf("intrinsic wrapper owner is absent from the emission universe")
	}
	callee := u.canonicalAlias(info.intrinsic)
	if callee == nil {
		return "", fmt.Errorf("wrapped intrinsic %q has cyclic canonical aliases", info.intrinsic.Name())
	}
	return framedEmissionKey(
		"llgo-intrinsic-call-wrapper-v1",
		owner.identity,
		u.finalIdentity(callee),
	), nil
}

func (u *EmissionUniverse) freezeFunctionIdentities() error {
	for wrapper, info := range u.callWrapInfo {
		key, err := u.intrinsicWrapperStructuralKey(info)
		if err != nil {
			return err
		}
		u.syntheticKeys[wrapper] = key
	}
	u.freezeManagedPhysicalNameCollisions()
	for _, fn := range u.functions {
		owners := u.sortedUseOwners(fn)
		if len(owners) == 0 {
			return fmt.Errorf("prepare emission universe: cannot freeze link identity for ownerless function %q", fn.Name())
		}
		final := u.finalIdentity(fn)
		if functionNeedsLinkOnce(fn) {
			var physical string
			for _, owner := range owners {
				key := u.finalKeys[emissionFunctionOwnerKey{function: fn, owner: owner}]
				if key == "" {
					continue
				}
				if physical == "" {
					physical = key
				} else if physical != key {
					return fmt.Errorf("prepare emission universe: linkonce function %q has owner-dependent physical symbols", fn.Name())
				}
			}
			u.linkIdentities[fn] = framedEmissionKey("cl-emission-linkonce-v1", final)
			continue
		}
		if len(owners) == 1 {
			u.linkIdentities[fn] = framedEmissionKey("cl-emission-link-v1", owners[0].identity, final)
			continue
		}
		// Non-linkonce Pkg-nil thunks and structural wrappers are emitted in
		// every concrete use-site module. Aggregate the sorted owner/symbol set;
		// choosing the first owner would make the identity input-order dependent.
		ownerSymbols := make([]string, 0, len(owners)*2)
		for _, owner := range owners {
			key := u.finalKeys[emissionFunctionOwnerKey{function: fn, owner: owner}]
			if key == "" {
				return fmt.Errorf("prepare emission universe: non-linkonce function %q has no frozen physical symbol for owner %q", fn.Name(), owner.identity)
			}
			ownerSymbols = append(ownerSymbols, owner.identity, key)
		}
		u.linkIdentities[fn] = framedEmissionKey(append([]string{"cl-emission-multi-owner-link-v1"}, ownerSymbols...)...)
	}
	return nil
}

type coroForeignPhysicalABI struct {
	symbol    string
	signature string
}

// freezeCoroForeignNoBlockCertificates binds source directives to the same
// exact physical identities already frozen for codegen. It deliberately scans
// every required C declaration before accepting a certificate: if another
// required declaration names the same physical symbol with a different ABI
// signature, the proof fails closed instead of blessing one guessed spelling.
func (u *EmissionUniverse) freezeCoroForeignNoBlockCertificates() error {
	abiByFunction := make(map[*ssa.Function]coroForeignPhysicalABI)
	signaturesBySymbol := make(map[string]map[string]none)
	for _, fn := range u.functions {
		owners := u.sortedUseOwners(fn)
		var abi coroForeignPhysicalABI
		haveABI := false
		for _, owner := range owners {
			key := u.finalKeys[emissionFunctionOwnerKey{function: fn, owner: owner}]
			ftype, symbol, signature, ok := splitManagedSymbolKey(key)
			if !ok || ftype != cFunc {
				continue
			}
			candidate := coroForeignPhysicalABI{symbol: symbol, signature: signature}
			if haveABI && candidate != abi {
				return fmt.Errorf("prepare emission universe: C declaration %q has owner-dependent physical ABI while freezing coroutine noblock metadata", fn.Name())
			}
			abi, haveABI = candidate, true
		}
		if !haveABI {
			continue
		}
		abiByFunction[fn] = abi
		signatures := signaturesBySymbol[abi.symbol]
		if signatures == nil {
			signatures = make(map[string]none)
			signaturesBySymbol[abi.symbol] = signatures
		}
		signatures[abi.signature] = none{}
	}

	for _, fn := range u.functions {
		annotated, err := coroForeignNoBlockDirective(fn)
		if err != nil {
			return fmt.Errorf("prepare emission universe: coroutine noblock directive on %q: %w", fn.Name(), err)
		}
		if !annotated {
			continue
		}
		abi, ok := abiByFunction[fn]
		if !ok {
			return fmt.Errorf("prepare emission universe: //llgo:coro noblock on %q requires an exact frozen C declaration", fn.Name())
		}
		if signatures := signaturesBySymbol[abi.symbol]; len(signatures) != 1 {
			return fmt.Errorf("prepare emission universe: //llgo:coro noblock physical symbol %q has conflicting frozen ABI signatures", abi.symbol)
		}
		linkIdentity, ok := u.linkIdentities[fn]
		if !ok || linkIdentity == "" {
			return fmt.Errorf("prepare emission universe: //llgo:coro noblock on %q has no frozen link identity", fn.Name())
		}
		u.foreignNoBlock[fn] = CoroForeignNoBlockCertificate{
			ID: framedEmissionKey(
				"llgo-coro-foreign-noblock-v0",
				linkIdentity,
				abi.symbol,
				abi.signature,
			),
			PhysicalSymbol: abi.symbol,
			ABISignature:   abi.signature,
		}
	}
	return nil
}

func coroForeignNoBlockDirective(fn *ssa.Function) (bool, error) {
	if fn == nil {
		return false, nil
	}
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Doc == nil {
		return false, nil
	}
	found := false
	for _, comment := range decl.Doc.List {
		if comment == nil {
			continue
		}
		line := strings.TrimSpace(comment.Text)
		switch line {
		case "//llgo:coro noblock", "// llgo:coro noblock":
			if found {
				return false, fmt.Errorf("duplicate //llgo:coro noblock directive")
			}
			found = true
		default:
			if strings.HasPrefix(line, "//llgo:coro") || strings.HasPrefix(line, "// llgo:coro") {
				return false, fmt.Errorf("unsupported directive %q", line)
			}
		}
	}
	return found, nil
}

func (u *EmissionUniverse) freezeManagedPhysicalNameCollisions() {
	// Linkonce definitions from different use-site modules meet in one linker
	// namespace. Grouping by the emission owner would therefore miss the most
	// important collision: two distinct instances each emitted by only one
	// owner. A repeated exact function is still one member of the group.
	groups := make(map[string]map[*ssa.Function]none)
	for _, fn := range u.functions {
		if !functionNeedsLinkOnce(fn) {
			// Package declarations and explicit go:linkname targets have an
			// externally meaningful spelling. Only internal generic/linkonce
			// definitions are safe to disambiguate with a private suffix.
			continue
		}
		for _, owner := range u.sortedUseOwners(fn) {
			ownerKey := emissionFunctionOwnerKey{function: fn, owner: owner}
			finalKey := u.finalKeys[ownerKey]
			if finalKey == "" {
				continue
			}
			ftype, legacy, _, ok := splitManagedSymbolKey(finalKey)
			if !ok || ftype != goFunc {
				continue
			}
			name := u.physicalNames[ownerKey]
			if name == "" {
				name = legacy
			}
			if groups[name] == nil {
				groups[name] = make(map[*ssa.Function]none)
			}
			groups[name][fn] = none{}
		}
	}
	disambiguate := make(map[*ssa.Function]none)
	for _, functions := range groups {
		if len(functions) < 2 {
			continue
		}
		for fn := range functions {
			disambiguate[fn] = none{}
		}
	}
	for fn := range disambiguate {
		for _, owner := range u.sortedUseOwners(fn) {
			ownerKey := emissionFunctionOwnerKey{function: fn, owner: owner}
			if u.physicalNames[ownerKey] != "" {
				continue
			}
			finalKey := u.finalKeys[ownerKey]
			_, legacy, _, ok := splitManagedSymbolKey(finalKey)
			if !ok {
				continue
			}
			// finalIdentity is owner-independent for linkonce functions. It gives
			// every emission of the same exact instance the same spelling while
			// distinguishing canonical generic arguments that do not occur in the
			// callable signature.
			discriminator := framedEmissionKey("cl-managed-physical-v2", u.finalIdentity(fn))
			u.physicalNames[ownerKey] = legacy + "$llgo$managed$v1$" + emissionDigest(discriminator)
		}
	}
}

func splitManagedSymbolKey(key string) (ftype int, name, signature string, ok bool) {
	prefix, rest, ok := strings.Cut(key, "\x00")
	if !ok {
		return 0, "", "", false
	}
	name, signature, ok = strings.Cut(rest, "\x00")
	if !ok {
		return 0, "", "", false
	}
	ftype, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, "", "", false
	}
	return ftype, name, signature, true
}

func (u *EmissionUniverse) sortedUseOwners(fn *ssa.Function) []*preparedEmissionPackage {
	owners := make([]*preparedEmissionPackage, 0, len(u.useOwners[fn]))
	for owner := range u.useOwners[fn] {
		owners = append(owners, owner)
	}
	if len(owners) == 0 {
		if owner := u.fnOwners[fn]; owner != nil {
			owners = append(owners, owner)
		}
	}
	sort.SliceStable(owners, func(i, j int) bool {
		if owners[i].identity != owners[j].identity {
			return owners[i].identity < owners[j].identity
		}
		return owners[i].order < owners[j].order
	})
	return owners
}

func (u *EmissionUniverse) finalIdentity(fn *ssa.Function) string {
	if fn == nil {
		return "<nil>"
	}
	fn = u.canonicalAlias(fn)
	if fn == nil {
		return "<cyclic-alias>"
	}
	type ownerFinalKey struct {
		owner string
		key   string
	}
	owners := u.sortedUseOwners(fn)
	managed := make([]ownerFinalKey, 0, len(owners))
	for _, owner := range owners {
		if key := u.finalKeys[emissionFunctionOwnerKey{function: fn, owner: owner}]; key != "" {
			managed = append(managed, ownerFinalKey{owner: owner.identity, key: key})
		}
	}
	if len(managed) != 0 {
		sort.SliceStable(managed, func(i, j int) bool {
			if managed[i].owner != managed[j].owner {
				return managed[i].owner < managed[j].owner
			}
			return managed[i].key < managed[j].key
		})
		if functionNeedsLinkOnce(fn) {
			unique := make(map[string]none, len(managed))
			for _, item := range managed {
				unique[item.key] = none{}
			}
			keys := make([]string, 0, len(unique)+1)
			keys = append(keys, "managed-linkonce")
			for key := range unique {
				keys = append(keys, key)
			}
			sort.Strings(keys[1:])
			return framedEmissionKey(keys...)
		}
		if len(managed) == 1 {
			return framedEmissionKey("managed", managed[0].key)
		}
		fields := make([]string, 1, len(managed)*2+1)
		fields[0] = "managed-multi-owner"
		for _, item := range managed {
			fields = append(fields, item.owner, item.key)
		}
		return framedEmissionKey(fields...)
	}
	if info, ok := u.callWrapInfo[fn]; ok {
		if key := u.syntheticKeys[fn]; key != "" {
			return key
		}
		if key, err := u.intrinsicWrapperStructuralKey(info); err == nil {
			return key
		}
		owner := u.packages[info.owner]
		ownerPath := ""
		if owner != nil {
			ownerPath = owner.identity
		}
		return framedEmissionKey("llgo-intrinsic-call-wrapper-v1", ownerPath, emissionFunctionSortKey(info.intrinsic))
	}
	owner := u.ownerOf(fn)
	if owner != nil {
		ctx := &context{
			prog:        u.prog,
			fset:        u.goProg.Fset,
			goProg:      u.goProg,
			goTyps:      owner.pkgTypes,
			goPkg:       owner.ssa,
			patches:     u.patches,
			loaded:      u.loadedPackages(),
			linkOnceFns: make(map[*ssa.Function]none),
		}
		_, name, ftype := ctx.funcName(fn)
		sig := ""
		if fn.Signature != nil {
			sig = types.TypeString(fn.Signature, func(pkg *types.Package) string { return llssa.PathOf(pkg) })
		}
		return framedEmissionKey("resolved", strconv.Itoa(ftype), name, sig)
	}
	return framedEmissionKey("ssa", emissionFunctionSortKey(fn))
}

func (u *EmissionUniverse) functionSortKey(fn *ssa.Function) string {
	owners := u.sortedUseOwners(fn)
	ownerIDs := make([]string, 0, len(owners))
	for _, owner := range owners {
		ownerIDs = append(ownerIDs, owner.identity)
	}
	return framedEmissionKey(u.finalIdentity(fn), strings.Join(ownerIDs, "\x00"), emissionFunctionSortKey(fn))
}

func framedEmissionKey(fields ...string) string {
	var out strings.Builder
	for _, field := range fields {
		out.WriteString(strconv.Itoa(len(field)))
		out.WriteByte(':')
		out.WriteString(field)
		out.WriteByte(';')
	}
	return out.String()
}

func emissionFunctionSortKey(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	sig := ""
	if fn.Signature != nil {
		sig = types.TypeString(fn.Signature, func(pkg *types.Package) string { return llssa.PathOf(pkg) })
	}
	filename := ""
	line, column := 0, 0
	if fn.Prog != nil && fn.Prog.Fset != nil && fn.Pos().IsValid() {
		// Raw token.Pos includes the FileSet allocation base and therefore
		// changes when otherwise unrelated files are parsed first. Ignore line
		// directives, strip checkout-dependent directories, and retain the
		// package-local basename plus lexical coordinates as the stable
		// diagnostic/sort tie-breaker.
		position := fn.Prog.Fset.PositionFor(fn.Pos(), false)
		filename = strings.ReplaceAll(position.Filename, "\\", "/")
		if filename != "" {
			filename = path.Base(filename)
		}
		line, column = position.Line, position.Column
	}
	return framedEmissionKey(
		functionPackagePath(fn),
		fn.Name(),
		filename,
		strconv.Itoa(line),
		strconv.Itoa(column),
		fn.Synthetic,
		sig,
	)
}

func emissionFunctionDiagnostic(fn *ssa.Function) string {
	if fn == nil {
		return "<nil>"
	}
	callee := ""
	var body strings.Builder
	if len(fn.Blocks) != 0 {
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				fmt.Fprintf(&body, "%T:%s|", instr, instr.String())
				if call, ok := instr.(ssa.CallInstruction); ok && call.Common().StaticCallee() != nil {
					callee = emissionFunctionSortKey(call.Common().StaticCallee())
					break
				}
			}
			if callee != "" {
				break
			}
		}
	}
	return fmt.Sprintf("{%s; synthetic=%q; callee=%q; body=%q}", emissionFunctionSortKey(fn), fn.Synthetic, callee, body.String())
}

func (u *EmissionUniverse) functionProvenanceDiagnostic(owner *preparedEmissionPackage, fn *ssa.Function) string {
	pathOf := func(pkg *types.Package) string {
		if pkg == nil {
			return "<nil>"
		}
		label := llssa.PathOf(pkg)
		switch pkg {
		case owner.oldTypes:
			label += "(old)"
		case owner.altTypes:
			label += "(alt)"
		case owner.pkgTypes:
			label += "(effective)"
		}
		return label
	}
	fnPkg := "<nil>"
	if fn != nil && fn.Pkg != nil {
		fnPkg = pathOf(fn.Pkg.Pkg)
	}
	recv := "<nil>"
	if fn != nil && fn.Signature != nil && fn.Signature.Recv() != nil {
		recvType := fn.Signature.Recv().Type()
		recv = types.TypeString(recvType, func(pkg *types.Package) string { return pathOf(pkg) })
	}
	objectPkg := "<nil>"
	if fn != nil && fn.Object() != nil {
		objectPkg = pathOf(fn.Object().Pkg())
	}
	return fmt.Sprintf("fnPkg=%s recv=%s objectPkg=%s", fnPkg, recv, objectPkg)
}
