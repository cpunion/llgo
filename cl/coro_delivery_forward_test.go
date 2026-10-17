//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroDeliveryForwardingPreservesOneCustodySlotAndTypedViews(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{
		{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			for _, asyncHelper := range []bool{false, true} {
				source := newEmissionTestProgram()
				source.ssa.CreatePackage(types.Unsafe, nil, nil, true)
				runtimePkg := source.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocU(size uintptr) unsafe.Pointer { return nil }
func AssertNilDeref(value bool) {}
func PanicWrapNilPointer(value bool, receiver, method string) {}
type iface struct { tab, data unsafe.Pointer }
func IfacePtrData(value iface) unsafe.Pointer { return value.data }
func memequal32(a, b unsafe.Pointer) bool { return true }
func memequal64(a, b unsafe.Pointer) bool { return true }
func memequalptr(a, b unsafe.Pointer) bool { return true }
func interequal(a, b unsafe.Pointer) bool { return true }
func nilinterequal(a, b unsafe.Pointer) bool { return true }
func strequal(a, b unsafe.Pointer) bool { return true }
func memequal8(a, b unsafe.Pointer) bool { return true }
func structequal(a, b unsafe.Pointer) bool { return true }
func memequal0(a, b unsafe.Pointer) bool { return true }
`)
				foo := source.addPackage(t, "foo", `package foo
import "unsafe"
type Result = struct { R0 int; R1 error }
func initialize(out *Result) { out.R0 = -1 }
func drop(out *Result, _ unsafe.Pointer, a, b int) { out.R0 = a*10+b }
//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*Result), func(*Result, unsafe.Pointer, int, int)) *Result
func Acquire(a, b int) (int,error) { out := borrow(initialize,drop); out.R0=a+b; return out.R0,out.R1 }
func Permuted(a, b int) (int,error) { return Acquire(b,a) }
func Identity(a, b int) (int,error) { return Acquire(a,b) }
func Literal(a int) (int,error) { return Permuted(a,7) }
func Chain(a int) (int,error) { return Literal(a) }
//go:noinline
func NoInline(a int) (int,error) { return Literal(a) }
type FieldInputs struct { Prefix int; Inner struct { Token int; Callback func() } }
type FieldAlias = FieldInputs
func Projected(value FieldAlias, b int) (int,error) { return Acquire(value.Inner.Token,b) }
func ProjectedChain(value FieldAlias, b int) (int,error) { return Projected(value,b) }
var PublishedProjected = Projected
var PublishedProjectedChain = ProjectedChain
func Changed(a int) (int,error) { n,e:=Acquire(a,0); return n+1,e }
func dropPath(out *Result, _ unsafe.Pointer, path string) { out.R0 = len(path) }
//go:linkname borrowPath llgo.coroBorrowResult
func borrowPath(func(*Result), func(*Result, unsafe.Pointer, string)) *Result
func AcquirePath(path string) (int,error) { out := borrowPath(initialize,dropPath); out.R0=len(path); return out.R0,out.R1 }
func FixedPath() (int,error) { return AcquirePath("fixed") }
var Published = Chain
var PublishedPermutation = Permuted
var PublishedIdentity = Identity
var PublishedPath = FixedPath
type Receiver struct { Token int }
func (Receiver) Open(a,b int) (int,error) { return Acquire(a,b) }
var PublishedMethod = Receiver.Open
type Token int
func (token Token) Bound(b int) (int,error) { return Acquire(int(token),b) }
type Opener interface { Open(int,int) (int,error) }
type PlainReceiver struct{}
func (PlainReceiver) Open(a,b int) (int,error) { return a+b,nil }
func InterfaceOwner(open Opener) { open.Open(11,12) }
type PointerReceiver struct { Value int }
func dropPointer(out *Result, _ unsafe.Pointer, receiver *PointerReceiver, a int) { out.R0 = a }
//go:linkname borrowPointer llgo.coroBorrowResult
func borrowPointer(func(*Result), func(*Result,unsafe.Pointer,*PointerReceiver,int)) *Result
func (receiver *PointerReceiver) PointerOpen(a int) (int,error) {
 out := borrowPointer(initialize,dropPointer)
 out.R0 = a
 return out.R0,out.R1
}
type PointerOpener interface { PointerOpen(int) (int,error) }
var PublishedCaptured = Token(17).Bound
func Root(pointer *PointerReceiver) {
 Permuted(1,2); Literal(3); Chain(4); NoInline(5); Published(6); Changed(7); PublishedPermutation(8,9); PublishedIdentity(10,11); FixedPath(); PublishedPath()
 var fields FieldAlias
 fields.Inner.Token=17
 Projected(fields,3); ProjectedChain(fields,4); PublishedProjected(fields,5); PublishedProjectedChain(fields,6)
 receiver := Receiver{Token:37}
 receiver.Open(1,2)
 bound := receiver.Open
 receiver.Token=99
 bound(3,4)
 PublishedMethod(receiver,5,6)
 token := Token(17)
 captured := token.Bound
 token = 99
 captured(7)
 PublishedCaptured(8)
 var open Opener = receiver
 open.Open(9,10)
 InterfaceOwner(open)
 InterfaceOwner(PlainReceiver{})
 var pointerOpen PointerOpener = pointer
 pointerOpen.PointerOpen(13)
}
`)
				source.ssa.Build()
				prog := newLLSSAProgForTarget(t, target.arch)
				defer prog.Dispose()
				u, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
					{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
					{SSA: foo.ssa, Files: []*ast.File{foo.file}},
				}, EmissionUniverseOptions{CompleteRuntimeABI: true})
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"Permuted", "Literal", "Chain", "NoInline", "FixedPath", "Projected", "ProjectedChain"} {
					recipe, present, err := u.coroProgramIR.sourceDelivery(foo.ssa.Func(name))
					if err != nil || !present || recipe.forward == nil || recipe.borrow != nil || recipe.anchor() != recipe.forward.call {
						t.Fatalf("%s did not retain its exact real forwarding call: %+v: %v", name, recipe, err)
					}
				}
				chain := u.coroProgramIR.deliveryProducers[foo.ssa.Func("Chain")]
				if chain.forwardBase != foo.ssa.Func("Acquire") || len(chain.forwardArgs) != 2 ||
					chain.forwardArgs[0].constant == nil || chain.forwardArgs[0].constant.Value.ExactString() != "7" ||
					chain.forwardArgs[1].sourceParameter != 0 {
					t.Fatalf("multi-layer drop projection was not composed in ProgramIR: %+v", chain)
				}
				for _, name := range []string{"Projected", "ProjectedChain"} {
					recipe := u.coroProgramIR.deliveryProducers[foo.ssa.Func(name)]
					if recipe.forwardBase != foo.ssa.Func("Acquire") || len(recipe.forwardArgs) != 2 ||
						recipe.forwardArgs[0].sourceParameter != 0 || len(recipe.forwardArgs[0].fieldPath) != 2 ||
						recipe.forwardArgs[0].fieldPath[0] != 1 || recipe.forwardArgs[0].fieldPath[1] != 0 || recipe.forwardArgs[1].sourceParameter != 1 {
						t.Fatalf("%s lost its composed source field path: %+v", name, recipe)
					}
				}
				methods := 0
				for _, function := range u.Functions() {
					if function.Name() != "Open" && function.Name() != "Open$bound" && function.Name() != "Open$thunk" {
						continue
					}
					if function.Signature.Recv() != nil && strings.Contains(function.Signature.Recv().Type().String(), "PlainReceiver") {
						if _, present, err := u.coroProgramIR.sourceDelivery(function); err != nil || present {
							t.Fatalf("ordinary interface candidate acquired false resource custody: %s: %v", function, err)
						}
						continue
					}
					recipe, present, err := u.coroProgramIR.sourceDelivery(function)
					if err != nil || !present || recipe.forwardBase != foo.ssa.Func("Acquire") || len(recipe.forwardArgs) != 2 {
						t.Fatalf("%s lost its normalized receiver custody: %+v: %v", function, recipe, err)
					}
					methods++
				}
				if methods < 4 {
					t.Fatalf("only %d declared/bound/expression method recipes", methods)
				}
				captures := 0
				for _, function := range u.Functions() {
					if function.Name() != "Bound$bound" {
						continue
					}
					recipe, present, err := u.coroProgramIR.sourceDelivery(function)
					if err != nil || !present || len(recipe.forwardArgs) != 2 || recipe.forwardArgs[0].capture == nil ||
						recipe.forwardArgs[0].environment == nil || recipe.forwardArgs[0].environmentField != 0 || recipe.forwardArgs[1].sourceParameter != 0 {
						t.Fatalf("%s lost its frozen immutable receiver: %+v: %v", function, recipe, err)
					}
					captures++
				}
				if captures != 1 {
					t.Fatalf("bound captured receiver recipes=%d, want one", captures)
				}
				receiverSnapshotRejections := 0
				for _, function := range u.Functions() {
					if function.Name() != "Bound" || function.Signature.Recv() == nil ||
						function.Signature.Recv().Type().String() != "*foo.Token" {
						continue
					}
					receiverSnapshotRejections++
					if u.coroProgramIR.validateInterfaceDeliveryReceiver(function) == nil {
						t.Fatal("unevaluated pointer-to-value receiver acquired a false interface capability")
					}
				}
				if receiverSnapshotRejections != 1 {
					t.Fatalf("receiver snapshot negative gates=%d, want one real pointer wrapper", receiverSnapshotRejections)
				}
				if _, present, _ := u.coroProgramIR.sourceDelivery(foo.ssa.Func("Changed")); present {
					t.Fatal("changed return acquired resource custody")
				}
				inventory, err := coro.NewSSAEmissionUniverse(foo.ssa.Prog, u.Functions())
				if err != nil {
					t.Fatal(err)
				}
				ids := u.FunctionIDConfig()
				ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, true
				plan, err := coro.AnalyzeSSA(foo.ssa.Prog, coro.Roots{
					{Function: foo.ssa.Func("Root"), Demand: coro.AsyncDemand}, {Function: foo.ssa.Func("init"), Demand: coro.AsyncDemand},
				}, coro.SSAConfig{
					EmissionUniverse: inventory, FunctionIDs: ids, MaxPlainInstructions: -1, OutcomeMode: coro.OutcomeExplicitStatus,
					DynamicResolution: coro.DynamicCHAClosed,
					ResolveFunction: func(function *ssa.Function) (*ssa.Function, bool, error) {
						canonical, present := u.Resolve(function)
						return canonical, present, nil
					},
					ClassifyLocalBody: u.CoroLocalBodyFacts, ClassifyLoweredCalls: u.CoroLoweredCalls,
					ClassifyDemandReferences:       u.CoroDemandReferences,
					ClassifySyncDemandReferences:   u.CoroSyncDemandReferences,
					ClassifyManagedValueReferences: u.CoroPlanningMetadata().ManagedValueReferences,
					ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
						if fn == foo.ssa.Func("Acquire") || fn == foo.ssa.Func("AcquirePath") || fn.Name() == "PointerOpen" || asyncHelper && fn == foo.ssa.Func("drop") {
							return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
						}
						return coro.SSAFunctionPolicy{}, nil
					},
					ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
						fact, present, err := u.CoroCallSitePlan(call)
						return present && fact.ElidesCall(), err
					},
					ClassifyStaticFunctionReferenceCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, arg int) (bool, error) {
						return u.CoroStaticFunctionReferenceCallArgument(call, arg)
					},
					ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
						if call.Common().IsInvoke() {
							return coro.UnknownManagedInterfaceDispatch, nil
						}
						return coro.UnknownManagedDispatch, nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				for _, owner := range []*ssa.Function{foo.ssa.Func("Root"), foo.ssa.Func("InterfaceOwner")} {
					for _, block := range owner.Blocks {
						for _, instruction := range block.Instrs {
							call, ok := instruction.(*ssa.Call)
							if !ok || !call.Common().IsInvoke() {
								continue
							}
							fact, found := plan.CallPlan(call)
							if !found || !fact.ResultDelivery || u.coroProgramIR.callPlans[call].dynamicDelivery == nil {
								t.Fatalf("%s interface lost conditional receiver-free custody: %+v", owner, fact)
							}
							if box, ok := call.Common().Value.(*ssa.MakeInterface); ok {
								if elided, err := coroProveExactInterfaceMakeElision(plan, box); err != nil || elided {
									t.Fatalf("resource receiver context was elided: %t: %v", elided, err)
								}
							}
						}
					}
				}
				for _, owner := range []*ssa.Function{foo.ssa.Func("init"), foo.ssa.Func("Root")} {
					for _, block := range owner.Blocks {
						for _, instruction := range block.Instrs {
							closure, ok := instruction.(*ssa.MakeClosure)
							if !ok {
								continue
							}
							raw := closure.Fn.(*ssa.Function)
							canonical, _ := u.Resolve(raw)
							id, _ := plan.FunctionID(canonical)
							value, _ := plan.ValuePlan(closure)
							present := false
							for _, leaf := range value.Funcs {
								for _, target := range leaf.Targets {
									present = present || target == id
								}
							}
							if !present {
								t.Fatalf("closure %s canonical target %s (%s) absent from %+v", raw, canonical, id, value)
							}
						}
					}
				}
				for _, name := range []string{"Permuted", "Literal", "Chain", "NoInline", "FixedPath", "Projected", "ProjectedChain"} {
					guard, err := prepareCoroDeliveryFunctionPlan(foo.ssa.Func(name), plan, u.coroProgramIR, u)
					if err != nil || guard == nil || guard.fallback == nil || len(guard.calls) != 1 || !guard.calls[0].forward {
						t.Fatalf("%s guard lost single incoming/fallback owner: %+v: %v", name, guard, err)
					}
				}
				// Captured globals use the real non-moving collector contract;
				// unprofiled/moving stores remain rejected by their own gates.
				compilation := &Compilation{CoroPlan: plan, EmissionUniverse: u, CoroFrameRetentionABI: CoroFrameRetentionParkABIV2}
				enableCoroChildAwaitCompilation(compilation)
				compilation.PanicABI = coro.PanicExplicitStatusABIV0
				pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, foo.ssa, []*ast.File{foo.file}, goembed.VarMap{}, PackageOptions{Compilation: compilation})
				if err != nil {
					t.Fatal(err)
				}
				module := pkg.Module()
				defer module.Dispose()
				metadata := coro.LibraryEffectMetadata{FunctionIDSchema: coro.FunctionIDSchema, CoroABI: compilation.CoroABI,
					SchedulerABI: compilation.SchedulerABI, PanicABI: compilation.PanicABI, FuncRepABI: compilation.FuncRepABI,
					TargetTriple: prog.TargetSpec().Triple, PointerBits: prog.PointerSize() * 8, Endianness: "little", DataLayout: prog.DataLayout()}
				base, _, err := u.CoroLibraryEffects().SourceDeliveryRecord(foo.ssa.Func("Acquire"), metadata)
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"Permuted", "Identity", "Literal", "Chain", "NoInline", "Projected", "ProjectedChain"} {
					record, present, err := u.CoroLibraryEffects().SourceDeliveryRecord(foo.ssa.Func(name), metadata)
					if err != nil || !present || record.Drop != base.Drop || record.DropABI != base.DropABI || record.DropView.IsZero() || record.DropView.Symbol != coroDeliveryViewSymbol(u.finalIdentity(foo.ssa.Func(name))) {
						t.Fatalf("%s changed its real helper identity or lost the physical view: %+v: %v", name, record, err)
					}
				}
				helperPlan, _ := plan.FunctionPlan(foo.ssa.Func("drop"))
				prefix := 0
				helperName := "foo.drop"
				switch helperPlan.ManagedEntry {
				case coro.ManagedEntryCoroutine:
					prefix = 2
					helperName += coroPrimarySuffix
				case coro.ManagedEntryOutcomePlain:
					prefix = 3
					helperName += coroOutcomePlainPrimarySuffix
				}
				interfaceViews := 0
				for _, function := range u.Functions() {
					if function.Name() != "Open" || function.Signature.Recv() == nil ||
						function.Signature.Recv().Type().String() != "*foo.Receiver" {
						continue
					}
					view := module.NamedFunction("__llgo_coro_delivery_interface_view." + emissionDigest(u.finalIdentity(function)) + ".drop")
					if view.IsNil() || view.BasicBlocksCount() != 1 {
						t.Fatal("interface receiver lost its frame-free typed helper view")
					}
					calls := 0
					for instruction := view.FirstBasicBlock().FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
						switch instruction.InstructionOpcode() {
						case llvm.Load, llvm.Alloca, llvm.Store:
							t.Fatal("unused interface receiver introduced a context read or storage")
						case llvm.Call:
							calls++
							if instruction.CalledValue().Name() != helperName ||
								instruction.Operand(prefix).C != view.Param(prefix).C || !instruction.Operand(prefix+1).IsNull() ||
								instruction.Operand(prefix+2).C != view.Param(prefix+2).C || instruction.Operand(prefix+3).C != view.Param(prefix+3).C {
								t.Fatalf("interface helper lost its receiver-free mapping: %s", instruction.String())
							}
						}
					}
					if calls != 1 {
						t.Fatalf("interface view calls=%d, want one real helper call", calls)
					}
					interfaceViews++
				}
				if interfaceViews != 1 {
					t.Fatalf("interface typed receiver views=%d, want one", interfaceViews)
				}
				pointerViews := 0
				for _, function := range u.Functions() {
					if function.Name() != "PointerOpen" {
						continue
					}
					view := module.NamedFunction("__llgo_coro_delivery_interface_view." + emissionDigest(u.finalIdentity(function)) + ".drop")
					if view.IsNil() || view.BasicBlocksCount() != 1 {
						t.Fatal("pointer receiver lost its exact identity helper view")
					}
					calls := 0
					for instruction := view.FirstBasicBlock().FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
						switch instruction.InstructionOpcode() {
						case llvm.Load, llvm.Alloca, llvm.Store:
							t.Fatal("pointer identity view reread the pointee or allocated storage")
						case llvm.Call:
							calls++
							callee := instruction.CalledValue()
							base := callee.ParamsCount() - 4
							if instruction.Operand(base).C != view.Param(base).C || !instruction.Operand(base+1).IsNull() ||
								instruction.Operand(base+2).C != view.Param(base+1).C || instruction.Operand(base+3).C != view.Param(base+2).C {
								t.Fatalf("pointer identity view changed its evaluated receiver: %s", instruction.String())
							}
						}
					}
					if calls != 1 {
						t.Fatalf("pointer identity helper calls=%d, want one", calls)
					}
					pointerViews++
				}
				if pointerViews != 1 {
					t.Fatalf("pointer identity views=%d, want one", pointerViews)
				}
				for _, function := range u.Functions() {
					if function.Name() != "Bound$bound" {
						continue
					}
					view := module.NamedFunction(coroDeliveryViewSymbol(u.finalIdentity(function)))
					if view.IsNil() || view.BasicBlocksCount() != 1 {
						t.Fatal("bound receiver lost its frame-free typed drop view")
					}
					loads, calls := 0, 0
					for instruction := view.FirstBasicBlock().FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
						switch instruction.InstructionOpcode() {
						case llvm.Load:
							loads++
						case llvm.Alloca, llvm.Store:
							t.Fatal("bound receiver drop view added storage instead of reading its existing context")
						case llvm.Call:
							calls++
							if instruction.CalledValue().Name() != helperName || !instruction.Operand(prefix+1).IsNull() ||
								instruction.Operand(prefix+2).InstructionOpcode() != llvm.Load || instruction.Operand(prefix+3).C != view.Param(prefix+2).C {
								t.Fatalf("bound receiver lost exact captured-value/parameter mapping: %s", instruction.String())
							}
						}
					}
					if loads != 1 || calls != 1 {
						t.Fatalf("bound drop view loads=%d calls=%d, want one typed capture read and one real helper call", loads, calls)
					}
				}
				for _, name := range []string{"Permuted", "Identity", "Chain", "Projected", "ProjectedChain"} {
					view := module.NamedFunction(coroDeliveryViewSymbol(u.finalIdentity(foo.ssa.Func(name))))
					if view.IsNil() || view.BasicBlocksCount() != 1 {
						t.Fatalf("%s has no pure typed helper view", name)
					}
					calls := 0
					for instruction := view.FirstBasicBlock().FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
						if instruction.InstructionOpcode() == llvm.Load || instruction.InstructionOpcode() == llvm.Alloca || instruction.InstructionOpcode() == llvm.Store {
							t.Fatalf("%s pure value view added a memory operation: %s", name, instruction.String())
						}
						if instruction.InstructionOpcode() != llvm.Call {
							continue
						}
						calls++
						if instruction.CalledValue().Name() != helperName {
							t.Fatalf("%s helper view did not call the real helper directly: %s", name, instruction.String())
						}
						if !instruction.Operand(prefix + 1).IsNull() {
							t.Fatalf("%s passed an unrelated forwarding env to its context-free base helper", name)
						}
						if name == "Chain" {
							if !instruction.Operand(prefix+2).IsConstant() || instruction.Operand(prefix+2).ZExtValue() != 7 || instruction.Operand(prefix+3).C != view.Param(prefix+2).C {
								t.Fatalf("composed Chain lost literal/parameter order: %s", instruction.String())
							}
							continue
						}
						if name == "Projected" || name == "ProjectedChain" {
							value := instruction.Operand(prefix + 2)
							for _, field := range []uint32{0, 1} {
								if value.InstructionOpcode() != llvm.ExtractValue || len(value.Indices()) != 1 || value.Indices()[0] != field {
									t.Fatalf("%s lost its exact aggregate extraction: %s", name, instruction.String())
								}
								value = value.Operand(0)
							}
							if value.C != view.Param(prefix+2).C || instruction.Operand(prefix+3).C != view.Param(prefix+3).C {
								t.Fatalf("%s changed its source parameter mapping: %s", name, instruction.String())
							}
							continue
						}
						first, second := prefix+2, prefix+3
						if name == "Permuted" {
							first, second = second, first
						}
						if instruction.Operand(prefix+2).C != view.Param(first).C || instruction.Operand(prefix+3).C != view.Param(second).C {
							t.Fatalf("%s same-typed arguments lost their exact mapping: %s", name, instruction.String())
						}
					}
					if calls != 1 {
						t.Fatalf("%s helper calls=%d, want one", name, calls)
					}
				}
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("forwarded resource IR: %v\n%s", err, module.String())
				}
				for _, name := range []string{"Permuted", "Literal", "Chain", "NoInline", "Projected", "ProjectedChain"} {
					body := requireCoroPhysicalFunction(t, module, "foo."+name)
					results := 0
					for _, block := range body.BasicBlocks() {
						for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
							if instruction.InstructionOpcode() != llvm.Alloca {
								continue
							}
							typ := instruction.AllocatedType()
							if typ.TypeKind() != llvm.StructTypeKind {
								continue
							}
							fields := typ.StructElementTypes()
							if len(fields) == 2 && fields[0].TypeKind() == llvm.IntegerTypeKind && fields[0].IntTypeWidth() == prog.PointerSize()*8 && fields[1].TypeKind() == llvm.StructTypeKind && len(fields[1].StructElementTypes()) == 2 {
								results++
							}
						}
					}
					if results != 1 {
						t.Fatalf("%s resource result slots=%d, want only its nil-output fallback:\n%s", name, results, body.String())
					}
				}
				runCoroABITestPipeline(t, prog, module)
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// This source-fact gate precedes physical ABI and actual cancellation gates.
func TestCoroDeliveryMethodsKeepCustodyAcrossCompilerAdapters(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	source := newEmissionTestProgram()
	source.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := source.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocU(size uintptr) unsafe.Pointer { return nil }
func AssertNilDeref(value bool) {}
`)
	foo := source.addPackage(t, "foo", `package foo
import "unsafe"
type Result = struct { R0 int; R1 error }
func initialize(out *Result) { out.R0 = -1 }
func drop(out *Result, _ unsafe.Pointer, a, b int) { out.R0 = a*10+b }
//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*Result), func(*Result, unsafe.Pointer, int, int)) *Result
func Acquire(a, b int) (int,error) { out := borrow(initialize,drop); out.R0=a+b; return out.R0,out.R1 }
type Receiver struct{}
func (Receiver) Open(a, b int) (int,error) { return Acquire(a,b) }
type Opener interface { Open(int,int) (int,error) }
var MethodExpression = Receiver.Open
func Root(receiver Receiver) {
 receiver.Open(1,2)
 bound := receiver.Open
 bound(3,4)
 MethodExpression(receiver,5,6)
 var opener Opener = receiver
 opener.Open(7,8)
}
`)
	source.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: foo.ssa, Files: []*ast.File{foo.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	receiver := foo.types.Scope().Lookup("Receiver").Type().(*types.Named)
	method := source.ssa.FuncValue(receiver.Method(0))
	if recipe, present, err := u.coroProgramIR.sourceDelivery(method); err != nil || !present || recipe.forward == nil {
		t.Errorf("declared method lost its receiver-normalized result custody: present=%t recipe=%+v error=%v", present, recipe, err)
	}
	adapters := 0
	for _, function := range u.Functions() {
		if function == foo.ssa.Func("Root") {
			continue
		}
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*ssa.Call)
				if !ok || call.Common().StaticCallee() != method {
					continue
				}
				adapters++
				if recipe, present, err := u.coroProgramIR.sourceDelivery(function); err != nil || !present || recipe.forward == nil {
					t.Errorf("compiler adapter %s lost transparent custody: present=%t error=%v", function, present, err)
				}
			}
		}
	}
	if adapters < 2 {
		t.Errorf("expected bound and method-expression adapters, found %d", adapters)
	}
	for _, block := range foo.ssa.Func("Root").Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().IsInvoke() && u.coroProgramIR.callPlans[call].dynamicDelivery == nil {
				t.Error("interface invocation lost its conditional typed delivery consumer")
			}
		}
	}
}
