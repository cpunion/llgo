package coro

import (
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// Use actual Go declarations: constructing both signatures with the same
// helper would hide the package identity of unexported struct fields.
func TestDeliveryRecipeAcceptsSourceDeclaredResultAlias(t *testing.T) {
	pkg := resultSlotTestSourcePackage(t)
	record, producer, initializer, drop := deliveryTestInputs()
	producer.Signature = pkg.Scope().Lookup("acquire").Type().(*types.Signature)
	initializer.Signature = pkg.Scope().Lookup("initialize").Type().(*types.Signature)
	drop.Signature = pkg.Scope().Lookup("drop").Type().(*types.Signature)
	recipe, err := FreezeDeliveryRecipe(record, producer, initializer, drop)
	if err != nil {
		t.Fatal(err)
	}
	if !types.Identical(initializer.Signature.Params().At(0).Type(), types.NewPointer(recipe.ResultType())) {
		t.Fatal("source helper does not use the canonical typed result pointer")
	}
	// A defined type has a distinct Go identity even with the same fields.
	defined := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "definedResult", nil), recipe.ResultType(), nil)
	initializer.Signature = deliveryTestSignature([]types.Type{types.NewPointer(defined)}, nil)
	if _, err := FreezeDeliveryRecipe(record, producer, initializer, drop); err == nil {
		t.Fatal("accepted an unrelated defined result type without an exact ABI")
	}
}

func deliveryTestSignature(parameters, results []types.Type) *types.Signature {
	tuple := func(elements []types.Type) *types.Tuple {
		variables := make([]*types.Var, len(elements))
		for index, typ := range elements {
			variables[index] = types.NewParam(token.NoPos, nil, "", typ)
		}
		return types.NewTuple(variables...)
	}
	return types.NewSignatureType(nil, nil, nil, tuple(parameters), tuple(results), false)
}

func deliveryTestInputs() (DeliveryRecipeRecord, DeliveryCallable, DeliveryCallable, DeliveryCallable) {
	record := DeliveryRecipeRecord{
		Version:  DeliveryRecipeVersionV1,
		Producer: "llgo.function.v0:acquire", ProducerABI: strings.Repeat("1", 64),
		Initializer: "llgo.function.v0:acquire.init", InitializerABI: strings.Repeat("2", 64),
		Drop: "llgo.function.v0:acquire.drop", DropABI: strings.Repeat("3", 64),
		ResultSize: 24, ResultAlign: 8,
	}
	signature := deliveryTestSignature([]types.Type{types.Typ[types.String]},
		[]types.Type{types.Typ[types.Int], types.Universe.Lookup("error").Type()})
	init, drop := deliveryHelperSignatures(signature, ResultSlotType(signature.Results()))
	return record,
		DeliveryCallable{ID: record.Producer, ABIHash: record.ProducerABI, Signature: signature, ResultSize: 24, ResultAlign: 8},
		DeliveryCallable{ID: record.Initializer, ABIHash: record.InitializerABI, Signature: init},
		DeliveryCallable{ID: record.Drop, ABIHash: record.DropABI, Signature: drop}
}

func TestDeliveryRecipeBindsTypedProducerAndHelpers(t *testing.T) {
	record, producer, initializer, drop := deliveryTestInputs()
	recipe, err := FreezeDeliveryRecipe(record, producer, initializer, drop)
	if err != nil {
		t.Fatal(err)
	}
	if recipe.IsZero() || recipe.Record() != record || recipe.ResultType().NumFields() != 2 {
		t.Fatalf("frozen recipe = %+v", recipe)
	}
	copy := recipe.Record()
	copy.Drop = "llgo.function.v0:unrelated"
	if recipe.Record() != record {
		t.Fatal("record accessor mutated frozen authority")
	}
	for index := 0; index < recipe.ResultType().NumFields(); index++ {
		if !types.Identical(recipe.ResultType().Field(index).Type(), producer.Signature.Results().At(index).Type()) {
			t.Fatal("result slot did not retain exact result type")
		}
	}
}

func TestDeliveryRecipePhysicalViewDoesNotReplaceLogicalHelperABI(t *testing.T) {
	record, producer, initializer, drop := deliveryTestInputs()
	record.DropView = DeliveryHelperViewRecord{Symbol: "__llgo_coro_delivery_view.wrapper.drop", ABIHash: strings.Repeat("4", 64)}
	drop.ABIHash = record.DropView.ABIHash
	if _, err := FreezeDeliveryRecipe(record, producer, initializer, drop); err != nil {
		t.Fatal(err)
	}
	drop.ABIHash = record.DropABI
	if _, err := FreezeDeliveryRecipe(record, producer, initializer, drop); err == nil {
		t.Fatal("logical helper hash was accepted as the adapted public callable ABI")
	}
	for _, view := range []DeliveryHelperViewRecord{{Symbol: "missing-hash"}, {ABIHash: strings.Repeat("4", 64)}, {Symbol: "bad symbol", ABIHash: strings.Repeat("4", 64)}} {
		record.DropView = view
		if record.Validate() == nil {
			t.Fatalf("incomplete physical helper view accepted: %+v", view)
		}
	}
}

func TestDeliveryRecipeRejectsIncompleteRecord(t *testing.T) {
	for name, change := range map[string]func(*DeliveryRecipeRecord){
		"absent":        func(r *DeliveryRecipeRecord) { *r = DeliveryRecipeRecord{} },
		"version":       func(r *DeliveryRecipeRecord) { r.Version++ },
		"producer":      func(r *DeliveryRecipeRecord) { r.Producer = "" },
		"initializer":   func(r *DeliveryRecipeRecord) { r.Initializer = "" },
		"drop":          func(r *DeliveryRecipeRecord) { r.Drop = "" },
		"producer-abi":  func(r *DeliveryRecipeRecord) { r.ProducerABI = "" },
		"init-abi":      func(r *DeliveryRecipeRecord) { r.InitializerABI = "not-an-abi" },
		"drop-abi":      func(r *DeliveryRecipeRecord) { r.DropABI = strings.Repeat("z", 64) },
		"producer-drop": func(r *DeliveryRecipeRecord) { r.Drop = r.Producer },
		"init-drop":     func(r *DeliveryRecipeRecord) { r.Initializer = r.Drop },
		"empty-size":    func(r *DeliveryRecipeRecord) { r.ResultSize = 0 },
		"empty-align":   func(r *DeliveryRecipeRecord) { r.ResultAlign = 0 },
		"odd-align":     func(r *DeliveryRecipeRecord) { r.ResultAlign = 3 },
		"partial-size":  func(r *DeliveryRecipeRecord) { r.ResultSize = 23 },
	} {
		t.Run(name, func(t *testing.T) {
			record, _, _, _ := deliveryTestInputs()
			change(&record)
			if err := record.Validate(); err == nil {
				t.Fatalf("accepted %+v", record)
			}
		})
	}
}

func TestDeliveryRecipeRejectsWrongTypedCapability(t *testing.T) {
	for name, change := range map[string]func(*DeliveryRecipeRecord, *DeliveryCallable, *DeliveryCallable, *DeliveryCallable){
		"producer-id":    func(_ *DeliveryRecipeRecord, p, _, _ *DeliveryCallable) { p.ID = "llgo.function.v0:other" },
		"producer-abi":   func(_ *DeliveryRecipeRecord, p, _, _ *DeliveryCallable) { p.ABIHash = strings.Repeat("4", 64) },
		"initializer-id": func(_ *DeliveryRecipeRecord, _, i, _ *DeliveryCallable) { i.ID = "llgo.function.v0:other" },
		"drop-id":        func(_ *DeliveryRecipeRecord, _, _, d *DeliveryCallable) { d.ID = "llgo.function.v0:other" },
		"init-abi":       func(_ *DeliveryRecipeRecord, _, i, _ *DeliveryCallable) { i.ABIHash = strings.Repeat("4", 64) },
		"drop-abi":       func(_ *DeliveryRecipeRecord, _, _, d *DeliveryCallable) { d.ABIHash = strings.Repeat("4", 64) },
		"layout-size":    func(r *DeliveryRecipeRecord, _, _, _ *DeliveryCallable) { r.ResultSize += 8 },
		"layout-align":   func(r *DeliveryRecipeRecord, _, _, _ *DeliveryCallable) { r.ResultAlign = 4 },
		"nil-source":     func(_ *DeliveryRecipeRecord, p, _, _ *DeliveryCallable) { p.Signature = nil },
		"no-results": func(_ *DeliveryRecipeRecord, p, _, _ *DeliveryCallable) {
			p.Signature = deliveryTestSignature(nil, nil)
		},
		"no-out-pointer": func(_ *DeliveryRecipeRecord, _, i, _ *DeliveryCallable) {
			i.Signature = deliveryTestSignature(nil, nil)
		},
		"untyped-out": func(_ *DeliveryRecipeRecord, _, i, _ *DeliveryCallable) {
			i.Signature = deliveryTestSignature([]types.Type{types.Typ[types.UnsafePointer]}, nil)
		},
		"drop-missing-inputs": func(_ *DeliveryRecipeRecord, _, i, d *DeliveryCallable) { d.Signature = i.Signature },
		"drop-result": func(_ *DeliveryRecipeRecord, _, _, d *DeliveryCallable) {
			d.Signature = types.NewSignatureType(nil, nil, nil, d.Signature.Params(), types.NewTuple(types.NewParam(token.NoPos, nil, "", types.Typ[types.Int])), false)
		},
		"helper-result-layout": func(_ *DeliveryRecipeRecord, _, _, d *DeliveryCallable) { d.ResultSize = 8 },
		"hidden-type-parameter": func(_ *DeliveryRecipeRecord, p, _, _ *DeliveryCallable) {
			parameter := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), types.Universe.Lookup("any").Type())
			p.Signature = deliveryTestSignature([]types.Type{types.NewPointer(parameter)}, []types.Type{types.Typ[types.Int]})
		},
	} {
		t.Run(name, func(t *testing.T) {
			record, producer, initializer, drop := deliveryTestInputs()
			change(&record, &producer, &initializer, &drop)
			if _, err := FreezeDeliveryRecipe(record, producer, initializer, drop); err == nil {
				t.Fatal("accepted mismatched typed delivery capability")
			}
		})
	}
}

func TestDeliveryCallSharesStaticAndDescriptorFacts(t *testing.T) {
	record, producer, initializer, drop := deliveryTestInputs()
	recipe, err := FreezeDeliveryRecipe(record, producer, initializer, drop)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(id FunctionID) (DeliveryRecipe, bool) { return recipe, id == record.Producer }
	for _, test := range []struct {
		name string
		call SSACallPlan
		want DeliveryCallKind
	}{
		{"static", SSACallPlan{Kind: CallDirect, Rep: DirectCoro, Transport: ManagedTransport, Targets: []FunctionID{record.Producer}}, StaticDelivery},
		{"closed-descriptor", SSACallPlan{Kind: CallDirect, Rep: Dispatch, Transport: ManagedTransport, Targets: []FunctionID{record.Producer}}, DescriptorDelivery},
		{"mixed-descriptor", SSACallPlan{Kind: CallDirect, Rep: Dispatch, Transport: ManagedTransport, Targets: []FunctionID{record.Producer, "llgo.function.v0:ordinary"}}, DescriptorDelivery},
		{"open-descriptor", SSACallPlan{Kind: CallDirect, Rep: Dispatch, Transport: ManagedTransport, Open: true, Unresolved: UnknownManagedDispatch}, DescriptorDelivery},
		{"open-interface", SSACallPlan{Kind: CallDirect, Rep: Dispatch, Transport: ManagedTransport, Open: true, Unresolved: UnknownManagedInterfaceDispatch}, DescriptorDelivery},
		{"ordinary-static", SSACallPlan{Kind: CallDirect, Rep: DirectCoro, Transport: ManagedTransport, Targets: []FunctionID{"llgo.function.v0:ordinary"}}, NoDelivery},
		{"ordinary-closed", SSACallPlan{Kind: CallDirect, Rep: Dispatch, Transport: ManagedTransport, Targets: []FunctionID{"llgo.function.v0:ordinary"}}, NoDelivery},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := PlanDeliveryCall(test.call, producer.Signature, lookup)
			if err != nil || plan.Kind != test.want {
				t.Fatalf("plan=%+v error=%v", plan, err)
			}
			if test.want == StaticDelivery && plan.Static.Record() != record {
				t.Fatal("static consumer did not retain exact frozen recipe")
			}
			if test.want == NoDelivery && plan != (DeliveryCallPlan{}) {
				t.Fatal("ordinary call acquired delivery state")
			}
		})
	}
	noResult := deliveryTestSignature(nil, nil)
	plan, err := PlanDeliveryCall(SSACallPlan{Kind: CallDirect, Rep: Dispatch, Transport: ManagedTransport, Open: true, Unresolved: UnknownManagedDispatch}, noResult, lookup)
	if err != nil || plan != (DeliveryCallPlan{}) {
		t.Fatal("zero-result open call acquired a resource-result guard")
	}
}

func TestDeliveryCallRejectsRawAndUnmatchedBoundary(t *testing.T) {
	record, producer, initializer, drop := deliveryTestInputs()
	recipe, err := FreezeDeliveryRecipe(record, producer, initializer, drop)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(FunctionID) (DeliveryRecipe, bool) { return recipe, true }
	for name, change := range map[string]func(*SSACallPlan){
		"raw":             func(c *SSACallPlan) { c.RawPlain = true },
		"nonmanaged":      func(c *SSACallPlan) { c.Transport = RawCCodePointer },
		"spawn":           func(c *SSACallPlan) { c.Kind = CallSpawn },
		"defer":           func(c *SSACallPlan) { c.Kind = CallDefer },
		"wrong-target":    func(c *SSACallPlan) { c.Targets = []FunctionID{"llgo.function.v0:other"} },
		"multiple-static": func(c *SSACallPlan) { c.Targets = append(c.Targets, record.Producer) },
		"open-static":     func(c *SSACallPlan) { c.Open = true; c.Unresolved = UnknownManagedDispatch },
		"open-untyped":    func(c *SSACallPlan) { c.Open = true; c.Rep = Dispatch },
	} {
		t.Run(name, func(t *testing.T) {
			call := SSACallPlan{Kind: CallDirect, Rep: DirectCoro, Transport: ManagedTransport, Targets: []FunctionID{record.Producer}}
			change(&call)
			if _, err := PlanDeliveryCall(call, producer.Signature, lookup); err == nil {
				t.Fatal("accepted unsupported delivery boundary")
			}
		})
	}
	call := SSACallPlan{Kind: CallDirect, Rep: DirectCoro, Transport: ManagedTransport, Targets: []FunctionID{record.Producer}}
	if _, err := PlanDeliveryCall(call, deliveryTestSignature(nil, []types.Type{types.Typ[types.Int]}), lookup); err == nil {
		t.Fatal("call signature mismatch accepted")
	}
}

func TestDeliveryCallDoesNotRevalidateOrdinarySourceSignature(t *testing.T) {
	lookup := func(FunctionID) (DeliveryRecipe, bool) { return DeliveryRecipe{}, false }
	variadic := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "", types.NewSlice(types.Typ[types.Int]))), nil, true)
	for _, signature := range []*types.Signature{nil, variadic} {
		for _, rep := range []FuncRep{DirectPlain, DirectCoro, Dispatch} {
			plan, err := PlanDeliveryCall(SSACallPlan{Kind: CallDirect, Rep: rep, Targets: []FunctionID{"llgo.function.v0:ordinary"}}, signature, lookup)
			if err != nil || plan != (DeliveryCallPlan{}) {
				t.Fatalf("ordinary call was reclassified: plan=%+v err=%v", plan, err)
			}
		}
	}
	plan, err := PlanDeliveryCall(SSACallPlan{Kind: CallDirect, Rep: Dispatch, Transport: RawCCodePointer, Open: true}, nil, lookup)
	if err != nil || plan != (DeliveryCallPlan{}) {
		t.Fatal("unknown raw foreign call acquired an inferred resource contract")
	}
}
