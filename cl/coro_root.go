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
	"encoding/hex"
	"fmt"
	"go/token"
	"go/types"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroRootFactoryPrefix           = "__llgo_coro_root_factory_v1."
	coroRootFactoryDescriptorPrefix = "__llgo_coro_root_factory_descriptor_v1."
)

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

func validateCoroRootFactories(plan *coro.SSAPlan) error {
	if plan == nil {
		return fmt.Errorf("coroutine root factory requires a compilation CoroPlan")
	}
	for _, root := range plan.Roots() {
		if root.Function == nil {
			return fmt.Errorf("coroutine root factory %q has no SSA function", root.ID)
		}
		if root.Demand != coro.AsyncDemand {
			return fmt.Errorf("coroutine root factory %q requires explicit async-only demand, got %s", root.ID, root.Demand)
		}
		function, ok := plan.FunctionPlan(root.Function)
		if !ok || function.ID != root.ID {
			return fmt.Errorf("coroutine root factory %q has no canonical function plan", root.ID)
		}
		if function.External != coro.Defined || function.Primary != coro.PrimaryCoroutine || function.FuncRep != coro.DirectCoro || function.Demand != coro.AsyncDemand {
			return fmt.Errorf(
				"coroutine root factory %q requires an async-only defined direct coroutine (external=%s primary=%s representation=%s demand=%s)",
				root.ID, function.External, function.Primary, function.FuncRep, function.Demand,
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
	pkg.NewCoroRootFactoryDescriptor(coroRootFactoryDescriptorPrefix+hash, llssa.CoroRootFactoryDescriptorOptions{
		Version: coroPhysicalABIVersionV1,
		ABIHash: abi.hash,
		Factory: factory.Expr,
		Startup: startupType,
		Result:  resultType,
	})
}
