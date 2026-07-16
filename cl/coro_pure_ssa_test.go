//go:build !llgo

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
	"bytes"
	"go/ast"
	"regexp"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroPureSSAFixture = `package foo

type Pair struct {
	A uint32
	B [2]uint32
}

type Word uintptr
type NamedPointer *uint32

var Global Pair
var Backing [2]uint32

func Child(value uint32) uint32 { return value + 1 }
func PairValue() Pair { return Pair{A: 3} }
func ArrayValue() [2]uint32 { return [2]uint32{5, 7} }
func ScalarPair() (uint32, uint32) { return 11, 13 }

func Aggregate() uint32 {
	left, right := ScalarPair()
	return PairValue().A + ArrayValue()[1] + left + right
}

func Root(pointer *uint32) (Pair, any, []uint32, uintptr) {
	var local Pair
	var values [2]uint32
	local.A = 7
	values[1] = 9
	local.B = values
	named := NamedPointer(pointer)
	boxed := any(named)
	view := Backing[:]
	for step := uint32(0); step < 2; step++ {
		local.A += step
	}
	Global = local
	next := Child(local.A)
	word := Word(next)
	global := Global
	return local, boxed, view, uintptr(word) + uintptr(global.A)
}
`

func TestCoroPureSSAPhysicalABIV1NativeAndWasm(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, ssaPkg, files, universe, plan := prepareCoroPureSSATestPlan(t, test.target)
			defer prog.Dispose()
			assertCoroPureSSAInstructionCoverage(t, ssaPkg)
			root := ssaPkg.Func("Root")
			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.FuncRep != coro.DirectCoro ||
				rootPlan.Demand != coro.AsyncDemand || !rootPlan.Exec.Contains(coro.NeedsPreempt) ||
				!rootPlan.Effect.Contains(coro.AwaitStructured) {
				t.Fatalf("Root plan = %+v, present=%t; want preemptible child-await coroutine", rootPlan, ok)
			}

			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroPreemptCompilation(compilation)
			pkg, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify pure SSA coroutine before CoroSplit: %v\n%s", err, module.String())
			}
			rootIR := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			aggregateIR := requireCoroPhysicalFunction(t, module, "foo.Aggregate").String()
			for _, required := range []string{
				"alloca %foo.Pair",
				"foo.Child$coro",
				"call void @" + coroAwaitPrepareHookV1,
				"call i1 @" + coroPreemptPollHookV1,
			} {
				if !strings.Contains(rootIR, required) {
					t.Fatalf("Root pure SSA coroutine lacks %q:\n%s", required, rootIR)
				}
			}
			if !regexp.MustCompile(`getelementptr inbounds(?: (?:nuw|nusw))* %foo\.Pair`).MatchString(rootIR) {
				t.Fatalf("Root pure SSA coroutine lacks typed Pair field addressing:\n%s", rootIR)
			}
			for _, forbidden := range []string{
				"CheckIndexRange", "AssertNilDeref", "AllocU", "AllocZ", "NewSlice2", "NewSlice3Bounds", "NewItab",
			} {
				if strings.Contains(rootIR, forbidden) {
					t.Fatalf("Root pure SSA lowering introduced hidden helper %q:\n%s", forbidden, rootIR)
				}
				if got := strings.Count(rootIR, "call void @"+coroYieldPrepareHookV1); got < 2 {
					t.Fatalf("Root preemption handoffs = %d, want multiple block safepoints after aggregate/interface/slice construction:\n%s", got, rootIR)
				}
			}
			if !strings.Contains(aggregateIR, "foo.PairValue$coro") ||
				!strings.Contains(aggregateIR, "foo.ArrayValue$coro") || !strings.Contains(aggregateIR, "extractvalue") {
				t.Fatalf("Aggregate lost its fixed-array/field/multi-result lowering:\n%s", aggregateIR)
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() {
				t.Fatalf("CoroSplit did not create Root resume entry:\n%s", module.String())
			}
			resumeIR := resume.String()
			for _, resultStore := range []*regexp.Regexp{
				regexp.MustCompile(`store %foo\.Pair `),
				regexp.MustCompile(`store %"[^"]*\.eface" `),
				regexp.MustCompile(`store %"[^"]*\.Slice" `),
			} {
				if !resultStore.MatchString(resumeIR) {
					t.Fatalf("value live across await/preempt did not reach its typed result store (%s):\n%s", resultStore, resumeIR)
				}
				if aggregateResume := module.NamedFunction("foo.Aggregate$coro.resume"); aggregateResume.IsNil() {
					t.Fatalf("CoroSplit did not create Aggregate resume entry:\n%s", module.String())
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit post-CoroSplit object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte("foo.Root$coro")) ||
				!bytes.Contains(object.Bytes(), []byte("foo.Aggregate$coro")) {
				t.Fatal("post-CoroSplit object lost a pure SSA coroutine symbol")
			}
		})
	}
}

func TestCoroPureSSAPreflightRemainsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "capturing closure",
			source: `package foo
func Root(value uint32) func() uint32 { return func() uint32 { return value } }
`,
			want: "nested function literals require closure body lowering",
		},
		{
			name: "type assertion",
			source: `package foo
func Root(value any) uint32 { result, _ := value.(uint32); return result }
`,
			want: "instruction is outside the CFG physical ABI allowlist",
		},
		{
			name: "dynamic call",
			source: `package foo
func Root(callback func() uint32) uint32 { return callback() }
`,
			want: "requires a compilation CallPlan",
		},
		{
			name: "possibly panicking slice index",
			source: `package foo
func Root(values []uint32, index int) uint32 { return values[index] }
`,
			want: "index base is not a fixed-array pointer",
		},
		{
			name: "nested field array needs nil helper",
			source: `package foo
type Value struct { Slots [2]uint32 }
func Root() uint32 { var value Value; value.Slots[1] = 9; return value.Slots[1] }
`,
			want: "operation lowers through managed runtime helper(s) AssertNilDeref",
		},
		{
			name: "allocating interface box",
			source: `package foo
func Root(value uint64) any { return any(value) }
`,
			want: "managed backing allocation",
		},
		{
			name: "heap allocation",
			source: `package foo
func Root() *uint32 { value := uint32(1); return &value }
`,
			want: "heap allocation requires managed allocation",
		},
		{
			name: "pointer global store without barrier",
			source: `package foo
var Global *uint32
func Root(value *uint32) { Global = value }
`,
			want: "global typed store of a pointer-containing value requires explicit write-barrier lowering",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, test.source)
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			root := ssaPkg.Func("Root")
			plan := coro.FunctionPlan{
				ID:       coro.FunctionID("foo.Root"),
				External: coro.Defined,
				Demand:   coro.AsyncDemand,
				Emission: coro.EmitCoroutine,
				Primary:  coro.PrimaryCoroutine,
				FuncRep:  coro.DirectCoro,
				Effect:   coro.YieldOnly,
			}
			err = validateCoroPhysicalABIWithUniverse(root, plan, nil, universe, true, true)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight error = %v, want %q", err, test.want)
			}
		})
	}
}

func prepareCoroPureSSATestPlan(t *testing.T, target *llssa.Target) (
	llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coro.SSAPlan,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroPureSSAFixture)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	functionIDs.ArchiveReady = true
	root, aggregate := ssaPkg.Func("Root"), ssaPkg.Func("Aggregate")
	child := ssaPkg.Func("Child")
	pairValue, arrayValue, scalarPair := ssaPkg.Func("PairValue"), ssaPkg.Func("ArrayValue"), ssaPkg.Func("ScalarPair")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: root, Demand: coro.AsyncDemand},
		{Function: aggregate, Demand: coro.AsyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: 1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == child || fn == pairValue || fn == arrayValue || fn == scalarPair {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, ssaPkg, files, universe, plan
}

func assertCoroPureSSAInstructionCoverage(t *testing.T, pkg *ssa.Package) {
	t.Helper()
	seen := struct {
		alloc, fieldAddr, indexAddr, index, slice, extract bool
		field, makeInterface, store, load                  bool
		changeType, convert                                bool
	}{}
	for _, name := range []string{"Root", "Aggregate"} {
		fn := pkg.Func(name)
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				switch instruction := instruction.(type) {
				case *ssa.Alloc:
					seen.alloc = true
				case *ssa.FieldAddr:
					seen.fieldAddr = true
				case *ssa.IndexAddr:
					seen.indexAddr = true
				case *ssa.Index:
					seen.index = true
				case *ssa.Slice:
					seen.slice = true
				case *ssa.Extract:
					seen.extract = true
				case *ssa.Field:
					seen.field = true
				case *ssa.MakeInterface:
					seen.makeInterface = true
				case *ssa.Store:
					seen.store = true
				case *ssa.UnOp:
					seen.load = seen.load || instruction.Op.String() == "*"
				case *ssa.ChangeType:
					seen.changeType = true
				case *ssa.Convert:
					seen.convert = true
				}
			}
		}
	}
	if !seen.alloc || !seen.fieldAddr || !seen.indexAddr || !seen.index || !seen.slice || !seen.extract ||
		!seen.field || !seen.makeInterface || !seen.store || !seen.load || !seen.changeType || !seen.convert {
		t.Fatalf("pure SSA fixture did not materialize every audited instruction class: %+v", seen)
	}
}
