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
	"go/token"
	"go/types"
	"sort"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroRootFactoryPrefix           = "__llgo_coro_root_factory_v1."
	coroRootFactoryDescriptorPrefix = "__llgo_coro_root_factory_descriptor_v1."
	coroRootPackageAnchorPrefix     = "__llgo_coro_root_package_v1."
	coroRootPackageAnchorVersionV1  = uint32(1)
)

type coroRootFactoryRegistration struct {
	functionID coro.FunctionID
	abiHash    [16]byte
	descriptor llssa.Expr
}

func coroRootFactorySignature() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "out", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "startup", types.Typ[types.UnsafePointer]),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func explicitCoroRoot(plan *coro.SSAPlan, fn *ssa.Function) (coro.SSARootPlan, bool) {
	if plan == nil || fn == nil {
		return coro.SSARootPlan{}, false
	}
	for _, root := range plan.Roots() {
		if root.Function == fn {
			return root, true
		}
	}
	return coro.SSARootPlan{}, false
}

func validateCoroRootEntries(plan *coro.SSAPlan) error {
	if plan == nil {
		return fmt.Errorf("coroutine root validation requires a compilation CoroPlan")
	}
	for _, root := range plan.Roots() {
		if root.Function == nil {
			return fmt.Errorf("coroutine root factory %q has no SSA function", root.ID)
		}
		function, ok := plan.FunctionPlan(root.Function)
		if !ok || function.ID != root.ID {
			return fmt.Errorf("coroutine root factory %q has no canonical function plan", root.ID)
		}
		if function.External != coro.Defined || !function.Demand.Contains(root.Demand) {
			return fmt.Errorf(
				"coroutine root %q requires a defined body whose demand contains the explicit root (external=%s emission=%s representation=%s demand=%s root-demand=%s)",
				root.ID, function.External, function.Emission, function.FuncRep, function.Demand, root.Demand,
			)
		}
		switch function.Emission {
		case coro.EmitPlain:
			// AsyncDemand describes an entry context, not a requirement to clone
			// or coroutine-lower a body that cannot suspend. A direct plain root
			// is invoked inside a scheduler-owned bootstrap coroutine and needs no
			// per-function root factory or package-anchor descriptor.
			if function.FuncRep != coro.DirectPlain {
				return fmt.Errorf(
					"plain coroutine root %q requires direct-plain representation, got %s",
					root.ID, function.FuncRep,
				)
			}
		case coro.EmitCoroutine:
			if root.Demand != coro.AsyncDemand || function.Demand != coro.AsyncDemand {
				return fmt.Errorf(
					"coroutine root factory %q requires explicit and total async-only demand, got root=%s total=%s",
					root.ID, root.Demand, function.Demand,
				)
			}
			if function.FuncRep != coro.DirectCoro {
				return fmt.Errorf(
					"coroutine root factory %q requires direct-coro representation, got %s",
					root.ID, function.FuncRep,
				)
			}
		default:
			return fmt.Errorf(
				"coroutine root %q requires a plain or coroutine body, got emission %s",
				root.ID, function.Emission,
			)
		}
	}
	return nil
}

// emitCoroRootFactory emits a typed, non-coroutine factory only for an
// explicitly declared Async root. The startup/result objects are owned by the
// runtime and outlive this native wrapper invocation; the factory merely loads
// scalar arguments and calls the root's unique coroutine ramp.
func (p *context) emitCoroRootFactory(pkg llssa.Package, entry plannedFunctionSymbol, abi coroPhysicalABI, sourceSig *types.Signature, ramp llssa.Function) {
	if p.compilation == nil || p.compilation.CoroPlan == nil {
		panic("coroutine root factory requires a compilation CoroPlan")
	}
	root, ok := explicitCoroRoot(p.compilation.CoroPlan, entry.function)
	if !ok {
		return
	}
	if root.Demand != coro.AsyncDemand || entry.plan.ID != root.ID {
		panic(fmt.Sprintf("coroutine root factory: unsupported root %q demand %s", root.ID, root.Demand))
	}

	fields := make([]*types.Var, sourceSig.Params().Len())
	for i := range fields {
		fields[i] = types.NewField(token.NoPos, nil, fmt.Sprintf("a%d", i), sourceSig.Params().At(i).Type(), false)
	}
	startupGoType := types.NewStruct(fields, nil)
	startupType := p.prog.Type(startupGoType, llssa.InGo)
	resultType := p.prog.Type(abi.resultSlotType, llssa.InGo)
	hash := hex.EncodeToString(abi.hash[:])
	factoryName := coroRootFactoryPrefix + hash
	factory := pkg.FuncOf(factoryName)
	if factory == nil {
		factory = pkg.NewFunc(factoryName, coroRootFactorySignature(), llssa.InC)
	}
	if !factory.HasBody() {
		b := factory.MakeBody(1)
		physicalArgs := make([]llssa.Expr, 0, len(fields)+2)
		physicalArgs = append(physicalArgs, factory.PhysicalParam(0), factory.PhysicalParam(1))
		if len(fields) != 0 {
			startup := b.Convert(p.prog.Pointer(startupType), factory.PhysicalParam(2))
			for i := range fields {
				physicalArgs = append(physicalArgs, b.Load(b.FieldAddr(startup, i)))
			}
		}
		handle := b.Call(ramp.Expr, physicalArgs...)
		b.Return(handle)
		b.EndBuild()
		b.Dispose()
	}
	descriptor := pkg.NewCoroRootFactoryDescriptor(coroRootFactoryDescriptorPrefix+hash, llssa.CoroRootFactoryDescriptorOptions{
		Version: coroPhysicalABIVersionV1,
		ABIHash: abi.hash,
		Factory: factory.Expr,
		Startup: startupType,
		Result:  resultType,
	})
	p.coroRootFactories = append(p.coroRootFactories, coroRootFactoryRegistration{
		functionID: root.ID,
		abiHash:    abi.hash,
		descriptor: descriptor,
	})
}

// emitCoroRootPackageAnchor emits the package's one linker-visible root
// registry after all source and deferred init compilation has finished. Root
// factories may be discovered in frontend emission order; the registry ABI is
// always canonical FunctionID order.
func (p *context) emitCoroRootPackageAnchor(pkg llssa.Package) {
	if len(p.coroRootFactories) == 0 {
		return
	}
	roots := append([]coroRootFactoryRegistration(nil), p.coroRootFactories...)
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].functionID < roots[j].functionID
	})
	descriptors := make([]llssa.Expr, len(roots))
	for i, root := range roots {
		if i != 0 && roots[i-1].functionID == root.functionID {
			panic(fmt.Sprintf("coroutine root package anchor: duplicate canonical root %q", root.functionID))
		}
		descriptors[i] = root.descriptor
	}
	hash := p.coroRootPackageAnchorHash(pkg, roots)
	pkg.NewCoroRootPackageAnchor(
		coroRootPackageAnchorPrefix+hex.EncodeToString(hash[:]),
		llssa.CoroRootPackageAnchorOptions{
			Version:     coroRootPackageAnchorVersionV1,
			ABIHash:     hash,
			Descriptors: descriptors,
		},
	)
}

// coroRootPackageAnchorHash is the single source for both the anchor symbol
// suffix and its embedded ABI hash. Normal builds use the canonical whole-plan
// digest supplied by the driver. Direct cl tests intentionally may omit that
// digest, so a domain-separated fallback covers the ordered roots and complete
// effective target layout without introducing pointer or emission-order state.
func (p *context) coroRootPackageAnchorHash(pkg llssa.Package, roots []coroRootFactoryRegistration) [16]byte {
	coroABI := coro.PhysicalABIV1
	schedulerABI := coro.SchedulerChildAwaitABIV0
	panicABI := coro.PanicLegacyABIV0
	funcRepABI := coro.FuncRepABIV0
	planDigest := ""
	if p.compilation != nil {
		planDigest = p.compilation.CoroPlanDigest
		if p.compilation.CoroABI != "" {
			coroABI = p.compilation.CoroABI
		}
		if p.compilation.SchedulerABI != "" {
			schedulerABI = p.compilation.SchedulerABI
		}
		if p.compilation.PanicABI != "" {
			panicABI = p.compilation.PanicABI
		}
		if p.compilation.FuncRepABI != "" {
			funcRepABI = p.compilation.FuncRepABI
		}
	}
	target := p.prog.TargetSpec()
	rootIdentities := make([]string, len(roots))
	for i, root := range roots {
		rootIdentities[i] = string(root.functionID) + "\x00" + hex.EncodeToString(root.abiHash[:])
	}
	if planDigest == "" {
		fallback := strings.Join(rootIdentities, "\x00")
		sum := sha256.Sum256([]byte(fmt.Sprintf(
			"llgo-coro-root-package-plan-fallback-v1\x00roots=%s\x00triple=%s\x00cpu=%s\x00features=%s\x00target-abi=%s\x00data-layout=%s\x00ptr=%d",
			fallback,
			target.Triple,
			target.CPU,
			target.Features,
			target.TargetABI,
			p.prog.DataLayout(),
			p.prog.PointerSize(),
		)))
		planDigest = hex.EncodeToString(sum[:])
	}
	key := fmt.Sprintf(
		"llgo-coro-root-package-v1\x00package=%s\x00plan=%s\x00coro=%s\x00scheduler=%s\x00panic=%s\x00func-rep=%s\x00triple=%s\x00cpu=%s\x00features=%s\x00target-abi=%s\x00data-layout=%s\x00ptr=%d\x00roots=%s",
		pkg.Path(),
		planDigest,
		coroABI,
		schedulerABI,
		panicABI,
		funcRepABI,
		target.Triple,
		target.CPU,
		target.Features,
		target.TargetABI,
		p.prog.DataLayout(),
		p.prog.PointerSize(),
		strings.Join(rootIdentities, "\x00"),
	)
	sum := sha256.Sum256([]byte(key))
	var hash [16]byte
	copy(hash[:], sum[:len(hash)])
	return hash
}
