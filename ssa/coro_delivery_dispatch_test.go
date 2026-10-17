package ssa

import (
	"encoding/binary"
	"go/token"
	"go/types"
	"regexp"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestCoroDeliveryHelperTypesNormalizeExactPhysicalAggregates(t *testing.T) {
	for _, target := range []struct {
		name string
		arch *Target
	}{{name: "native"}, {name: "wasm32", arch: &Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			f := newCoroDynamicDispatchTestFixture(t, target.arch)
			callback := coroPlainDispatchTestSignature(nil, nil)
			aggregate := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "callback", callback, false)}, nil)
			argument := types.NewNamed(types.NewTypeName(token.NoPos, nil, "Argument", nil), aggregate, nil)
			output := types.NewPointer(aggregate)
			result := f.prog.Type(aggregate, InGo)
			producer := coroPlainDispatchTestSignature([]types.Type{argument}, []types.Type{aggregate})
			initializer := coroPlainDispatchTestSignature([]types.Type{output}, nil)
			drop := coroPlainDispatchTestSignature([]types.Type{output, types.Typ[types.UnsafePointer], argument}, nil)
			physicalProducer := f.prog.PhysicalFuncDecl(producer, InGo)
			physicalDrop := f.prog.PhysicalFuncDecl(drop, InGo)
			if types.Identical(physicalProducer.Params().At(0).Type(), argument) || types.Identical(result.RawType(), aggregate) {
				t.Fatal("fixture did not exercise distinct source and physical aggregate types")
			}
			for _, source := range []*types.Signature{producer, physicalProducer} {
				for _, helper := range []*types.Signature{drop, physicalDrop} {
					f.prog.validateCoroDeliveryHelperTypes(source, result, initializer, helper)
				}
			}
			other := types.NewNamed(types.NewTypeName(token.NoPos, nil, "OtherArgument", nil), aggregate, nil)
			wrongDrop := coroPlainDispatchTestSignature([]types.Type{output, types.Typ[types.UnsafePointer], other}, nil)
			coroPlainDispatchMustPanicContains(t, "exact producer arguments", func() {
				f.prog.validateCoroDeliveryHelperTypes(physicalProducer, result, initializer, wrongDrop)
			})
			wrongInitializer := coroPlainDispatchTestSignature([]types.Type{types.Typ[types.UnsafePointer]}, nil)
			coroPlainDispatchMustPanicContains(t, "exact output", func() {
				f.prog.validateCoroDeliveryHelperTypes(physicalProducer, result, wrongInitializer, drop)
			})
		})
	}
}

func coroDeliveryDispatchTestOptions(t *testing.T, f *coroDynamicDispatchTestFixture) CoroDispatchDescriptorOptions {
	t.Helper()
	output := types.NewPointer(f.result.RawType())
	initializer := coroPlainDispatchTestSignature([]types.Type{output}, nil)
	drop := coroPlainDispatchTestSignature([]types.Type{output, types.Typ[types.UnsafePointer], types.Typ[types.Uint32]}, nil)
	makeHelper := func(name string, signature *types.Signature, coroutine bool, hash byte) CoroDispatchDeliveryHelper {
		flags := CoroDispatchFlagNoCapture | CoroDispatchFlagHasPlain | CoroDispatchFlagPlainNoUnwind
		physical := f.prog.CoroDispatchPlainEntrySignature(signature)
		if coroutine {
			flags = CoroDispatchFlagNoCapture | CoroDispatchFlagHasCoro
			physical = f.prog.CoroDispatchCoroEntrySignature(signature)
		}
		entry := f.pkg.NewFunc(name+"_entry", physical, InC)
		b := entry.MakeBody(1)
		if coroutine {
			b.Return(f.prog.Nil(f.prog.VoidPtr()))
		} else {
			b.Return()
		}
		b.EndBuild()
		b.Dispose()
		var abiHash [16]byte
		for index := range abiHash {
			abiHash[index] = hash + byte(index)
		}
		opts := CoroDispatchDescriptorOptions{
			Version: CoroDispatchVersionV2, Flags: flags, ABIHash: abiHash,
			Signature: signature, Result: f.prog.Struct(), CodeEntry: entry.Expr,
		}
		if coroutine {
			opts.CoroEntry = entry.Expr
		} else {
			opts.PlainEntry = entry.Expr
		}
		return CoroDispatchDeliveryHelper{
			Descriptor: f.pkg.NewCoroDispatchDescriptor(name, opts), Signature: signature, ABIHash: opts.ABIHash,
		}
	}
	opts := f.descriptorOptions(CoroDispatchFlagHasCoro | CoroDispatchFlagHasDelivery)
	opts.Delivery = &CoroDispatchDeliveryOptions{
		Version:     CoroDispatchDeliveryVersionV2,
		Initializer: makeHelper("delivery_initializer", initializer, false, 1),
		Drop:        makeHelper("delivery_drop", drop, true, 2),
	}
	return opts
}

func TestCoroDeliveryDispatchOptionalTailNativeAndWasm(t *testing.T) {
	for _, target := range []struct {
		name string
		arch *Target
	}{{name: "native"}, {name: "wasm32", arch: &Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			f := newCoroDynamicDispatchTestFixture(t, target.arch)
			opts := coroDeliveryDispatchTestOptions(t, f)
			descriptor := f.pkg.NewCoroDispatchDescriptor("resource_descriptor", opts)
			global := coroPlainDispatchGlobal(descriptor.impl)
			initializer := global.Initializer()
			if initializer.OperandsCount() != 2 || initializer.Operand(0).OperandsCount() != 9 ||
				initializer.Operand(1).OperandsCount() != 8 {
				t.Fatalf("resource descriptor does not have an intact base and optional tail: %s", global)
			}
			base, extended := f.prog.coroDispatchDescriptorType(), f.prog.coroDeliveryDescriptorType()
			if offset := f.prog.TargetData().ElementOffset(extended.ll, 1); uint64(offset) != f.prog.SizeOf(base) {
				t.Fatalf("tail offset=%d, want padded base size %d", offset, f.prog.SizeOf(base))
			}
			if size := f.prog.SizeOf(extended) - f.prog.SizeOf(base); size != uint64(40+2*f.prog.PointerSize()) {
				t.Fatalf("optional tail size=%d, want header, two pointers and two exact helper hashes", size)
			}
			tail := initializer.Operand(1)
			for index, helper := range []CoroDispatchDeliveryHelper{opts.Delivery.Initializer, opts.Delivery.Drop} {
				for word := 0; word < 2; word++ {
					if got, want := tail.Operand(4+index*2+word).ZExtValue(), binary.BigEndian.Uint64(helper.ABIHash[word*8:word*8+8]); got != want {
						t.Fatalf("helper %d hash word %d=%x, want %x", index, word, got, want)
					}
				}
			}
			if ordinary := f.coroOnly.impl.Initializer(); ordinary.OperandsCount() != 9 {
				t.Fatal("ordinary descriptor acquired a delivery tail")
			}
			if again := f.pkg.NewCoroDispatchDescriptor("resource_descriptor", opts); again.impl.C != descriptor.impl.C {
				t.Fatal("identical delivery publication did not reuse its global")
			}
			maker := f.pkg.NewFunc("make_resource_value", coroPlainDispatchTestSignature(nil, []types.Type{f.signature}), InGo)
			b := maker.MakeBody(1)
			b.Return(b.MakeCoroDispatchValue(f.signature, descriptor, Nil))
			b.EndBuild()
			b.Dispose()

			probe := f.pkg.NewFunc("guarded_delivery_probe", coroPlainDispatchTestSignature(
				[]types.Type{f.signature}, nil), InGo)
			b = probe.MakeBody(1)
			selection := b.PrepareCoroDispatchCall(probe.Param(0), CoroDispatchCallOptions{
				Version: CoroDispatchVersionV2, ABIHash: f.hash, Result: f.result, DeliveryGuard: true,
			})
			present, done := probe.MakeBlock(), probe.MakeBlock()
			b.If(selection.HasDelivery(), present, done)
			b.SetBlock(present)
			init, _ := b.PrepareCoroDispatchDelivery(selection, opts.Delivery.Initializer.Signature, opts.Delivery.Drop.Signature,
				CoroDispatchDeliveryABI{Initializer: opts.Delivery.Initializer.ABIHash, Drop: opts.Delivery.Drop.ABIHash})
			out := b.Alloc(f.result, false)
			b.CallCoroDispatchPlain(init, []Expr{out}, CoroDispatchCallOptions{
				Version: CoroDispatchVersionV2, ABIHash: opts.Delivery.Initializer.ABIHash, Result: f.prog.Struct(),
			})
			b.Jump(done)
			b.SetBlock(done)
			b.Return()
			b.EndBuild()
			b.Dispose()
			// No tail projection may be reachable with the positive successor
			// removed: a base-only descriptor is valid on the other lane.
			reachable := make(map[llvm.BasicBlock]bool)
			pending := []llvm.BasicBlock{probe.impl.FirstBasicBlock()}
			for len(pending) != 0 {
				block := pending[len(pending)-1]
				pending = pending[:len(pending)-1]
				if reachable[block] || block == present.first {
					continue
				}
				reachable[block] = true
				branch := block.LastInstruction()
				for index := 0; index < branch.SuccessorsCount(); index++ {
					pending = append(pending, branch.Successor(index))
				}
			}
			tailLoads := 0
			for _, block := range probe.impl.BasicBlocks() {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					if instruction.InstructionOpcode() != llvm.GetElementPtr || instruction.GEPSourceElementType().C != extended.ll.C {
						continue
					}
					tailLoads++
					if reachable[block] {
						t.Fatal("optional tail lacks a dominating positive capability branch")
					}
				}
			}
			if tailLoads != 1 {
				t.Fatalf("optional tail projections=%d, want exactly one", tailLoads)
			}
			if err := llvm.VerifyModule(f.pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify delivery descriptor: %v\n%s", err, f.pkg.String())
			}
			body := coroPlainDispatchIRFunction(f.pkg.String(), probe.Name())
			if !strings.Contains(body, "getelementptr") || !strings.Contains(body, "i32 1") ||
				!strings.Contains(body, "call void") {
				t.Fatalf("missing optional-tail projection or ordinary typed helper dispatch:\n%s", body)
			}
			// The consumer's four expected words must participate in generated
			// checks. Reading a helper's own hash and comparing it with itself
			// would not certify the helper type advertised by the producer.
			var checked [4]bool
			for _, block := range probe.impl.BasicBlocks() {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					if instruction.InstructionOpcode() != llvm.ICmp || instruction.IntPredicate() != llvm.IntNE {
						continue
					}
					field, expected := instruction.Operand(0), instruction.Operand(1)
					if field.IsAInstruction().IsNil() || field.InstructionOpcode() != llvm.ExtractValue ||
						field.Operand(0).Type().C != f.prog.Field(extended, 1).ll.C || expected.IsAConstantInt().IsNil() {
						continue
					}
					indices := field.Indices()
					if len(indices) != 1 || indices[0] < 4 || indices[0] > 7 {
						continue
					}
					index := int(indices[0]) - 4
					hash := [][16]byte{opts.Delivery.Initializer.ABIHash, opts.Delivery.Drop.ABIHash}[index/2]
					word := index % 2
					if want := binary.BigEndian.Uint64(hash[word*8 : word*8+8]); expected.ZExtValue() != want {
						t.Fatalf("tail field %d checked against %x, want %x", indices[0], expected.ZExtValue(), want)
					}
					checked[index] = true
				}
			}
			if checked != [4]bool{true, true, true, true} {
				t.Fatalf("typed tail omitted independent hash comparisons: %v\n%s", checked, body)
			}
			for _, name := range []string{"dynamic_coro_call", "dynamic_outcome_call", "dynamic_plain_call"} {
				body := coroPlainDispatchIRFunction(f.pkg.String(), name)
				if !regexp.MustCompile(`and i32 [^,]+, -64`).MatchString(body) ||
					!strings.Contains(body, "coro.dispatch.flags.unknown") || strings.Contains(body, "coro.dispatch.delivery") {
					t.Fatalf("unguarded call %s silently accepts a delivery descriptor:\n%s", name, body)
				}
			}
		})
	}
}

func TestCoroDeliveryDispatchRejectsChangedColdContract(t *testing.T) {
	for _, target := range []struct {
		name string
		arch *Target
	}{{name: "native"}, {name: "wasm32", arch: &Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			for field, name := range []string{"version", "reserved", "initializer", "drop", "initializer-lo", "initializer-hi", "drop-lo", "drop-hi"} {
				t.Run(name, func(t *testing.T) {
					f := newCoroDynamicDispatchTestFixture(t, target.arch)
					opts := coroDeliveryDispatchTestOptions(t, f)
					descriptor := f.pkg.NewCoroDispatchDescriptor("cold_contract", opts)
					global := coroPlainDispatchGlobal(descriptor.impl)
					base, tail := global.Initializer().Operand(0), global.Initializer().Operand(1)
					fields := make([]llvm.Value, tail.OperandsCount())
					for index := range fields {
						fields[index] = tail.Operand(index)
					}
					if field == 2 || field == 3 {
						fields[field] = llvm.ConstNull(fields[field].Type())
					} else {
						fields[field] = llvm.ConstInt(fields[field].Type(), fields[field].ZExtValue()^1, false)
					}
					changed := f.prog.ctx.ConstStruct(fields, false)
					if f.pkg.matchesCoroDispatchDelivery(changed, *opts.Delivery) {
						t.Fatal("changed cold helper contract was accepted")
					}
					global.SetInitializer(f.prog.ctx.ConstStruct([]llvm.Value{base, changed}, false))
					coroPlainDispatchMustPanicContains(t, "conflicts", func() {
						f.pkg.NewCoroDispatchDescriptor("cold_contract", opts)
					})
				})
			}
		})
	}
}

func TestCoroDeliveryDispatchRejectsIncompleteTypedPublication(t *testing.T) {
	for name, change := range map[string]func(*CoroDispatchDescriptorOptions){
		"missing-tail":           func(o *CoroDispatchDescriptorOptions) { o.Delivery = nil },
		"missing-capability":     func(o *CoroDispatchDescriptorOptions) { o.Flags &^= CoroDispatchFlagHasDelivery },
		"version":                func(o *CoroDispatchDescriptorOptions) { o.Delivery.Version++ },
		"old-version":            func(o *CoroDispatchDescriptorOptions) { o.Delivery.Version = 1 },
		"missing-initializer":    func(o *CoroDispatchDescriptorOptions) { o.Delivery.Initializer.Descriptor = Nil },
		"initializer-hash":       func(o *CoroDispatchDescriptorOptions) { o.Delivery.Initializer.ABIHash[0]++ },
		"drop-hash":              func(o *CoroDispatchDescriptorOptions) { o.Delivery.Drop.ABIHash[0]++ },
		"wrong-initializer-type": func(o *CoroDispatchDescriptorOptions) { o.Delivery.Initializer.Signature = o.Delivery.Drop.Signature },
		"untyped-output": func(o *CoroDispatchDescriptorOptions) {
			o.Delivery.Initializer.Signature = coroPlainDispatchTestSignature([]types.Type{types.Typ[types.UnsafePointer]}, nil)
		},
		"wrong-drop-argument": func(o *CoroDispatchDescriptorOptions) {
			o.Delivery.Drop.Signature = coroPlainDispatchTestSignature([]types.Type{
				o.Delivery.Initializer.Signature.Params().At(0).Type(), types.Typ[types.UnsafePointer], types.Typ[types.String]}, nil)
		},
		"wrong-entry-type": func(o *CoroDispatchDescriptorOptions) {
			o.Delivery.Initializer.Descriptor = o.Delivery.Drop.Descriptor
			o.Delivery.Initializer.ABIHash = o.Delivery.Drop.ABIHash
		},
		"plain-resource":   func(o *CoroDispatchDescriptorOptions) { o.Flags |= CoroDispatchFlagHasPlain },
		"runtime-resource": func(o *CoroDispatchDescriptorOptions) { o.Flags |= CoroDispatchFlagRuntimeTyped },
		"unbound-resource": func(o *CoroDispatchDescriptorOptions) { o.Flags |= CoroDispatchFlagDeliveryUnbound },
	} {
		t.Run(name, func(t *testing.T) {
			f := newCoroDynamicDispatchTestFixture(t)
			opts := coroDeliveryDispatchTestOptions(t, f)
			change(&opts)
			coroPlainDispatchMustPanicContains(t, "delivery", func() {
				f.pkg.NewCoroDispatchDescriptor("invalid_resource", opts)
			})
		})
	}
}

func TestCoroDeliveryDispatchRejectsUnboundHelperMetadata(t *testing.T) {
	for name, mutate := range map[string]func([]llvm.Value){
		"captured-helper": func(fields []llvm.Value) {
			flags := fields[1].ZExtValue() &^ uint64(CoroDispatchFlagNoCapture)
			fields[1] = llvm.ConstInt(fields[1].Type(), flags, false)
		},
		"nonvoid-helper": func(fields []llvm.Value) { fields[6] = llvm.ConstInt(fields[6].Type(), 1, false) },
		"unbound-helper": func(fields []llvm.Value) {
			fields[1] = llvm.ConstInt(fields[1].Type(), fields[1].ZExtValue()|uint64(CoroDispatchFlagDeliveryUnbound), false)
		},
		"missing-code": func(fields []llvm.Value) { fields[8] = llvm.ConstNull(fields[8].Type()) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newCoroDynamicDispatchTestFixture(t)
			opts := coroDeliveryDispatchTestOptions(t, f)
			global := coroPlainDispatchGlobal(opts.Delivery.Initializer.Descriptor.impl)
			base := global.Initializer()
			fields := make([]llvm.Value, base.OperandsCount())
			for index := range fields {
				fields[index] = base.Operand(index)
			}
			mutate(fields)
			global.SetInitializer(f.prog.ctx.ConstStruct(fields, false))
			coroPlainDispatchMustPanicContains(t, "delivery helper", func() {
				f.pkg.NewCoroDispatchDescriptor("invalid_helper_metadata", opts)
			})
		})
	}
}
