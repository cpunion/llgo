package ssa

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestCoroHelperViewPreservesManagedPrefixWithoutFrames(t *testing.T) {
	for _, target := range []struct {
		name string
		arch *Target
	}{{name: "native"}, {name: "wasm32", arch: &Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			for _, prefix := range []int{0, 2, 3} {
				f := newCoroDynamicDispatchTestFixture(t, target.arch)
				pointer := types.Typ[types.UnsafePointer]
				output := types.NewPointer(types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "R0", types.Typ[types.Int], false)}, nil))
				container := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "Value", types.Typ[types.Uint32], false)}, nil)
				flags := types.NewNamed(types.NewTypeName(token.NoPos, types.NewPackage("view", "view"), "Flags", nil), types.Typ[types.Uint32], nil)
				parameters := make([]types.Type, prefix)
				for index := range parameters {
					parameters[index] = pointer
				}
				var results []types.Type
				if prefix == 2 {
					results = []types.Type{pointer}
				}
				sourceSignature := coroPlainDispatchTestSignature(append(append([]types.Type(nil), parameters...), output, container), results)
				targetSignature := coroPlainDispatchTestSignature(append(append([]types.Type(nil), parameters...), output, flags, flags), results)
				callee := f.pkg.NewFunc("target", targetSignature, InGo)
				b := callee.MakeBody(1)
				if prefix == 2 {
					b.Return(callee.Param(1))
				} else {
					b.Return()
				}
				b.EndBuild()
				b.Dispose()
				arguments := []CoroHelperViewArgument{
					{Parameter: 0},
					{Parameter: 1, Fields: []int{0}},
					{Parameter: -1, Constant: f.prog.IntVal(7, f.prog.Uint32())},
				}
				view := f.pkg.NewCoroHelperView("view", sourceSignature, callee.Expr, prefix, arguments)
				if again := f.pkg.NewCoroHelperView("view", sourceSignature, callee.Expr, prefix, arguments); again.impl.C != view.impl.C {
					t.Fatal("identical typed view did not reuse its definition")
				}
				calls := 0
				for _, block := range view.impl.BasicBlocks() {
					for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
						switch instruction.InstructionOpcode() {
						case llvm.Alloca, llvm.Load, llvm.Store:
							t.Fatalf("physical view allocated or borrowed storage: %s", instruction)
						case llvm.Call:
							calls++
							if instruction.CalledValue().C != callee.impl.C {
								t.Fatalf("view introduced a helper call: %s", instruction)
							}
							for index := 0; index < prefix; index++ {
								if instruction.Operand(index).C != view.PhysicalParam(index).impl.C {
									t.Fatal("task/output/completion prefix changed")
								}
							}
							if instruction.Operand(prefix).C != view.PhysicalParam(prefix).impl.C {
								t.Fatal("resource result cell was copied or replaced")
							}
						}
					}
				}
				if calls != 1 {
					t.Fatalf("view calls=%d, want the one actual helper", calls)
				}
				body := coroPlainDispatchIRFunction(f.pkg.String(), "view")
				for _, forbidden := range []string{"llvm.coro", "coro_elide_safe", "await", "__llgo_coro"} {
					if strings.Contains(body, forbidden) {
						t.Fatalf("physical view added a frame or owner transaction: %s", body)
					}
				}
				changed := append([]CoroHelperViewArgument(nil), arguments...)
				changed[2].Constant = f.prog.IntVal(8, f.prog.Uint32())
				coroPlainDispatchMustPanicContains(t, "forwarding recipe", func() {
					f.pkg.NewCoroHelperView("view", sourceSignature, callee.Expr, prefix, changed)
				})
				if err := llvm.VerifyModule(f.pkg.Module(), llvm.ReturnStatusAction); err != nil {
					t.Fatalf("invalid physical helper view: %v\n%s", err, f.pkg.String())
				}
				// No local caller or descriptor takes this view's address.
				// Archive metadata nevertheless permits a future consumer to
				// reference it; compiler retention must not pin the final link.
				f.pkg.MaterializePreserveSyms()
				if !f.pkg.Module().NamedGlobal("llvm.used").IsNil() {
					t.Fatal("physical archive view acquired final-link retention")
				}
				options := llvm.NewPassBuilderOptions()
				options.SetVerifyEach(true)
				err := f.pkg.Module().RunPasses("globaldce", f.prog.TargetMachine(), options)
				options.Dispose()
				if err != nil {
					t.Fatal(err)
				}
				if kept := f.pkg.Module().NamedFunction("view"); kept.IsNil() || kept.BasicBlocksCount() == 0 {
					t.Fatal("compiler DCE removed a metadata-discoverable helper view")
				}
			}
		})
	}
}

func TestCoroHelperViewEqualTypedPermutationHasDistinctBinding(t *testing.T) {
	for _, target := range []*Target{nil, {GOOS: "wasip1", GOARCH: "wasm"}} {
		f := newCoroDynamicDispatchTestFixture(t, target)
		output := types.NewPointer(types.Typ[types.Int])
		signature := coroPlainDispatchTestSignature([]types.Type{output, types.Typ[types.Uint32], types.Typ[types.Uint32]}, nil)
		callee := f.pkg.NewFunc("actual_helper", signature, InGo)
		identity := []CoroHelperViewArgument{{Parameter: 0}, {Parameter: 1}, {Parameter: 2}}
		permutation := []CoroHelperViewArgument{{Parameter: 0}, {Parameter: 2}, {Parameter: 1}}
		f.pkg.NewCoroHelperView("identity", signature, callee.Expr, 0, identity)
		coroPlainDispatchMustPanicContains(t, "forwarding recipe", func() {
			f.pkg.NewCoroHelperView("identity", signature, callee.Expr, 0, permutation)
		})
		view := f.pkg.NewCoroHelperView("permutation", signature, callee.Expr, 0, permutation)
		calls := 0
		for _, block := range view.impl.BasicBlocks() {
			for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
				if instruction.InstructionOpcode() != llvm.Call {
					continue
				}
				calls++
				for index, source := range []int{0, 2, 1} {
					if instruction.Operand(index).C != view.PhysicalParam(source).impl.C {
						t.Fatal("same-typed arguments were matched by type instead of the frozen mapping")
					}
				}
			}
		}
		if calls != 1 {
			t.Fatal("permutation did not preserve the one actual target")
		}
		if err := llvm.VerifyModule(f.pkg.Module(), llvm.ReturnStatusAction); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCoroHelperViewRejectsChangedOutputAndAllocatingConversions(t *testing.T) {
	for _, name := range []string{"output", "field", "numeric", "prefix", "target-alias"} {
		t.Run(name, func(t *testing.T) {
			f := newCoroDynamicDispatchTestFixture(t)
			output := types.NewPointer(types.Typ[types.Int])
			source := coroPlainDispatchTestSignature([]types.Type{output, types.Typ[types.Uint32]}, nil)
			target := f.pkg.NewFunc("target", source, InGo)
			arguments := []CoroHelperViewArgument{{Parameter: 0}, {Parameter: 1}}
			prefix, symbol := 0, "invalid_view"
			switch name {
			case "output":
				arguments[0] = CoroHelperViewArgument{Parameter: -1, Constant: f.prog.Nil(f.prog.Pointer(f.prog.Int()))}
			case "field":
				arguments[1].Fields = []int{0}
			case "numeric":
				source = coroPlainDispatchTestSignature([]types.Type{output, types.Typ[types.Uint64]}, nil)
			case "prefix":
				prefix = 1
			case "target-alias":
				symbol = "target"
			}
			coroPlainDispatchMustPanicContains(t, "helper view", func() {
				f.pkg.NewCoroHelperView(symbol, source, target.Expr, prefix, arguments)
			})
		})
	}
}

func TestCoroHelperViewProjectsOnlyTypedBorrowedEnvironmentFields(t *testing.T) {
	for _, target := range []*Target{nil, {GOOS: "wasip1", GOARCH: "wasm"}} {
		for _, prefix := range []int{0, 2, 3} {
			f := newCoroDynamicDispatchTestFixture(t, target)
			word, pointer := types.Typ[types.Uint32], types.Typ[types.UnsafePointer]
			output := types.NewPointer(word)
			captured := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "Token", word, false)}, nil)
			environment := types.NewStruct([]*types.Var{
				types.NewField(token.NoPos, nil, "Unused", types.NewArray(types.Typ[types.Byte], 4096), false),
				types.NewField(token.NoPos, nil, "Captured", captured, false),
			}, nil)
			envType := f.prog.Type(types.NewPointer(environment), InGo)
			parameters := make([]types.Type, prefix)
			for index := range parameters {
				parameters[index] = pointer
			}
			var results []types.Type
			if prefix == 2 {
				results = []types.Type{pointer}
			}
			signature := coroPlainDispatchTestSignature(append(append([]types.Type(nil), parameters...), output, pointer), results)
			callee := f.pkg.NewFunc("actual", coroPlainDispatchTestSignature(append(append([]types.Type(nil), parameters...), output, word), results), InGo)
			arguments := []CoroHelperViewArgument{{Parameter: 0}, {Parameter: 1, Environment: envType, Fields: []int{1, 0}}}
			view := f.pkg.NewCoroHelperView("captured_view", signature, callee.Expr, prefix, arguments)
			loads, calls := 0, 0
			for _, block := range view.impl.BasicBlocks() {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					switch instruction.InstructionOpcode() {
					case llvm.Alloca, llvm.Store:
						t.Fatal("environment projection introduced owned storage:", instruction)
					case llvm.Load:
						loads++
						if instruction.Type().C != f.prog.Uint32().ll.C {
							t.Fatal("environment projection loaded more than its selected field:", instruction)
						}
					case llvm.Call:
						calls++
						if instruction.CalledValue().C != callee.impl.C {
							t.Fatal("environment projection introduced a runtime helper:", instruction)
						}
					}
				}
			}
			if loads != 1 || calls != 1 {
				t.Fatalf("environment view loads=%d calls=%d, want one selected load and the real helper", loads, calls)
			}
			// Interface value receivers can use the boxed value itself as the
			// typed context, without inventing a representation wrapper around it.
			rootView := f.pkg.NewCoroHelperView("boxed_receiver_view", signature, callee.Expr, prefix, []CoroHelperViewArgument{
				{Parameter: 0}, {Parameter: 1, Environment: f.prog.Pointer(f.prog.Uint32())},
			})
			rootLoads := 0
			for _, block := range rootView.impl.BasicBlocks() {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					if instruction.InstructionOpcode() == llvm.Load {
						rootLoads++
					}
				}
			}
			if rootLoads != 1 {
				t.Fatalf("boxed receiver loads=%d, want one exact typed load", rootLoads)
			}
			cellEnvironment := f.prog.Type(types.NewPointer(types.NewStruct([]*types.Var{
				types.NewField(token.NoPos, nil, "MutableCell", types.NewPointer(captured), false),
			}, nil)), InGo)
			for _, invalid := range []CoroHelperViewArgument{
				{Parameter: 0, Environment: envType, Fields: []int{1, 0}},
				{Parameter: 1, Environment: f.prog.Uint32(), Fields: []int{1, 0}},
				{Parameter: 1, Environment: envType},
				{Parameter: 1, Environment: envType, Fields: []int{1, 1}},
				{Parameter: 1, Environment: cellEnvironment, Fields: []int{0, 0}},
			} {
				coroPlainDispatchMustPanicContains(t, "helper view", func() {
					f.pkg.NewCoroHelperView("invalid_environment", signature, callee.Expr, prefix, []CoroHelperViewArgument{{Parameter: 0}, invalid})
				})
			}
			changedLayout := types.NewStruct([]*types.Var{
				types.NewField(token.NoPos, nil, "Unused", types.NewArray(types.Typ[types.Byte], 8192), false),
				types.NewField(token.NoPos, nil, "Captured", captured, false),
			}, nil)
			changed := append([]CoroHelperViewArgument(nil), arguments...)
			changed[1].Environment = f.prog.Type(types.NewPointer(changedLayout), InGo)
			coroPlainDispatchMustPanicContains(t, "forwarding recipe", func() {
				f.pkg.NewCoroHelperView("captured_view", signature, callee.Expr, prefix, changed)
			})
			mixedTarget := f.pkg.NewFunc("mixed_layout_target", coroPlainDispatchTestSignature(
				append(append([]types.Type(nil), parameters...), output, word, word), results), InGo)
			mixed := append(append([]CoroHelperViewArgument(nil), arguments...), changed[1])
			coroPlainDispatchMustPanicContains(t, "one frozen layout", func() {
				f.pkg.NewCoroHelperView("mixed_layout_view", signature, mixedTarget.Expr, prefix, mixed)
			})
			if err := llvm.VerifyModule(f.pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCoroHelperViewProjectsOwnedResultState(t *testing.T) {
	for _, target := range []*Target{nil, {GOOS: "wasip1", GOARCH: "wasm"}} {
		for _, prefix := range []int{0, 2, 3} {
			f := newCoroDynamicDispatchTestFixture(t, target)
			word, pointer := types.Typ[types.Uint32], types.Typ[types.UnsafePointer]
			tuple := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "R0", word, false)}, nil)
			output := types.NewPointer(tuple)
			record := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "Arg0", word, false)}, nil)
			stateType := f.prog.Type(types.NewPointer(record), InGo)
			parameters := make([]types.Type, prefix)
			for i := range parameters {
				parameters[i] = pointer
			}
			var results []types.Type
			if prefix == 2 {
				results = []types.Type{pointer}
			}
			signature := coroPlainDispatchTestSignature(append(append([]types.Type(nil), parameters...), output, pointer), results)
			callee := f.pkg.NewFunc("state_target", coroPlainDispatchTestSignature(append(append([]types.Type(nil), parameters...), output, word), results), InGo)
			argument := CoroHelperViewArgument{Parameter: 0, ResultState: stateType, Fields: []int{0}}
			view := f.pkg.NewCoroHelperView("state_view", signature, callee.Expr, prefix, []CoroHelperViewArgument{{Parameter: 0}, argument})
			loads, calls := 0, 0
			for _, block := range view.impl.BasicBlocks() {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					switch instruction.InstructionOpcode() {
					case llvm.Alloca, llvm.Store:
						t.Fatal("state view introduced owned storage:", instruction)
					case llvm.Load:
						loads++
					case llvm.Call:
						calls++
						if instruction.CalledValue().C != callee.impl.C {
							t.Fatal("state view introduced a runtime binder")
						}
					}
				}
			}
			if loads != 2 || calls != 1 {
				t.Fatalf("state view loads=%d calls=%d", loads, calls)
			}
			recordTarget := f.pkg.NewFunc("record_target", coroPlainDispatchTestSignature(append(append([]types.Type(nil), parameters...), types.NewPointer(record)), results), InGo)
			recordView := f.pkg.NewCoroHelperView("record_view", signature, recordTarget.Expr, prefix,
				[]CoroHelperViewArgument{{Parameter: 0, ResultState: stateType, StatePointer: true}})
			loads, calls = 0, 0
			for _, block := range recordView.impl.BasicBlocks() {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					switch instruction.InstructionOpcode() {
					case llvm.Alloca, llvm.Store:
						t.Fatal("record view introduced owned storage:", instruction)
					case llvm.Load:
						loads++
					case llvm.Call:
						calls++
						if instruction.CalledValue().C != recordTarget.impl.C {
							t.Fatal("record view introduced a runtime binder")
						}
					}
				}
			}
			if loads != 1 || calls != 1 {
				t.Fatalf("record view loads=%d calls=%d", loads, calls)
			}
			for _, invalid := range []CoroHelperViewArgument{
				{Parameter: 1, ResultState: stateType, Fields: []int{0}},
				{Parameter: 0, ResultState: f.prog.Pointer(f.prog.Uint32())},
				{Parameter: 0, ResultState: stateType, Environment: stateType, Fields: []int{0}},
				{Parameter: 0, ResultState: stateType, Fields: []int{1}},
				{Parameter: 0, StatePointer: true},
				{Parameter: 0, ResultState: stateType, StatePointer: true, Fields: []int{0}},
			} {
				coroPlainDispatchMustPanicContains(t, "helper view", func() {
					f.pkg.NewCoroHelperView("invalid_state_view", signature, callee.Expr, prefix, []CoroHelperViewArgument{{Parameter: 0}, invalid})
				})
			}
			if err := llvm.VerifyModule(f.pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Exercise the raw ABI and shared output cell, not scheduler handles: the
// prefix-2 target here is an ordinary test function with the entry signature.
func TestCoroHelperViewNativeArgumentExecution(t *testing.T) {
	Initialize(InitAll)
	llvm.LinkInMCJIT()
	for _, test := range []struct {
		prefix   int
		captured bool
	}{{0, false}, {0, true}, {2, false}, {2, true}, {3, false}, {3, true}} {
		prefix, captured := test.prefix, test.captured
		func() {
			prog := NewProgram(nil)
			defer prog.Dispose()
			pkg := prog.NewPackage("helperview", "helper/view/execution")
			word, pointer := types.Typ[types.Uint32], types.Typ[types.UnsafePointer]
			output := types.NewPointer(word)
			container := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "Value", word, false)}, nil)
			parameters := make([]types.Type, prefix)
			for index := range parameters {
				parameters[index] = pointer
			}
			var results []types.Type
			if prefix == 2 {
				results = []types.Type{pointer}
			}
			target := pkg.NewFunc("actual", coroPlainDispatchTestSignature(append(append([]types.Type(nil), parameters...), output, word, word), results), InGo)
			b := target.MakeBody(1)
			value := b.BinOp(token.ADD,
				b.BinOp(token.MUL, target.Param(prefix+1), prog.IntVal(10, prog.Uint32())), target.Param(prefix+2))
			b.StoreKnownNonNil(target.Param(prefix), value)
			if prefix == 2 {
				b.Return(target.Param(0))
			} else {
				b.Return()
			}
			b.EndBuild()
			b.Dispose()
			input := types.Type(container)
			arguments := []CoroHelperViewArgument{
				{Parameter: 0}, {Parameter: 1, Fields: []int{0}}, {Parameter: -1, Constant: prog.IntVal(7, prog.Uint32())},
			}
			if captured {
				input = pointer
				arguments[1].Environment = prog.Type(types.NewPointer(container), InGo)
			}
			signature := coroPlainDispatchTestSignature(append(append([]types.Type(nil), parameters...), output, input), results)
			view := pkg.NewCoroHelperView("view", signature, target.Expr, prefix, arguments)
			probe := pkg.NewFunc("probe", coroPlainDispatchTestSignature(nil, []types.Type{word}), InC)
			b = probe.MakeBody(1)
			out := b.AllocaT(prog.Uint32())
			b.StoreKnownNonNil(out, prog.IntVal(0, prog.Uint32()))
			var args []Expr
			for index := 0; index < prefix; index++ {
				args = append(args, b.Convert(prog.VoidPtr(), out))
			}
			inputValue := b.Aggregate(prog.Type(container, InGo), prog.IntVal(5, prog.Uint32()))
			if captured {
				storage := b.AllocaT(inputValue.Type)
				b.StoreKnownNonNil(storage, inputValue)
				inputValue = b.Convert(prog.VoidPtr(), storage)
			}
			args = append(args, out, inputValue)
			b.Call(view.Expr, args...)
			b.Return(b.LoadKnownNonNil(out))
			b.EndBuild()
			b.Dispose()
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				pkg.Module().Dispose()
				t.Fatal(err)
			}
			options := llvm.NewMCJITCompilerOptions()
			engine, err := llvm.NewMCJITCompiler(pkg.Module(), options)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Dispose() // The engine owns the module.
			result := engine.RunFunction(probe.impl, nil)
			defer result.Dispose()
			if got := result.Int(false); got != 57 {
				t.Fatalf("prefix %d: mapped helper output=%d, want 57", prefix, got)
			}
		}()
	}
}
