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
	"go/token"
	"go/types"
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

func TestCoroStringRangeNormalizesUntypedConstantSource(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func ConstantRange() int {
	total := 0
	for _, value := range "abc" {
		total += int(value)
	}
	return total
}
`)
	function := ssaPkg.Func("ConstantRange")
	var found *ssa.Range
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if rng, ok := instruction.(*ssa.Range); ok {
				found = rng
			}
		}
	}
	if found == nil {
		t.Fatal("constant string range fixture has no Range instruction")
	}
	basic, ok := types.Unalias(found.X.Type()).Underlying().(*types.Basic)
	if !ok || basic.Kind() != types.UntypedString {
		t.Fatalf("constant Range source type = %v; want untyped string SSA input", found.X.Type())
	}
	physical, accepted := coroPhysicalRangeStringType(found.X.Type())
	if !accepted || !types.Identical(physical, types.Typ[types.String]) {
		t.Fatalf("constant Range physical type = %v, %t; want concrete string", physical, accepted)
	}
	if _, accepted := coroPhysicalRangeStringType(types.Typ[types.UntypedInt]); accepted {
		t.Fatal("untyped integer was accepted as a string Range source")
	}
}

func TestCoroPureAggregateEqualityPhysicalABIV1NativeAndWasm(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	const source = `package foo
import "unsafe"
type Ticket struct { Epoch, Generation uint32 }
type Lease struct { ID [2]uintptr; Ticket Ticket }
type RunDecision struct {
	G *byte
	Ticket Ticket
	Cases [2]uint32
	Outcome uint8
	Task uint8
	Lease Lease
	Flag bool
	Scale float32
	Number complex64
	Channel chan byte
	Raw unsafe.Pointer
	_ string
}
func Child(value uint32) uint32 { return value + 1 }
func Leaf(left, right RunDecision) bool {
	_ = Child(left.Cases[0])
	return left != right
}
`
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, source)
			var prog llssa.Program
			if test.target == nil {
				prog = newLLSSAProg(t)
			} else {
				prog = newLLSSAProgForTarget(t, test.target)
			}
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			functionIDs := universe.FunctionIDConfig()
			functionIDs.CoroABI = coro.PhysicalABIV1
			functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
			functionIDs.ArchiveReady = true
			leaf, child := ssaPkg.Func("Leaf"), ssaPkg.Func("Child")
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: leaf, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse:     ssaUniverse,
				FunctionIDs:          functionIDs,
				MaxPlainInstructions: 1,
				OutcomeMode:          coro.OutcomeExplicitStatus,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == child {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
					if fn == leaf {
						return coro.SSAFunctionPolicy{Exec: coro.MayUnwind}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			leafPlan, ok := plan.FunctionPlan(leaf)
			if !ok || leafPlan.Emission != coro.EmitCoroutine || leafPlan.Primary != coro.PrimaryCoroutine ||
				!leafPlan.Effect.Contains(coro.AwaitStructured) {
				t.Fatalf("Leaf plan = %+v, present=%t; want PhysicalABIV1 structured child-await coroutine", leafPlan, ok)
			}
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroPreemptCompilation(compilation)
			pkg, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify aggregate equality before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.Leaf").String()
			for _, required := range []string{"extractvalue", "icmp", "fcmp"} {
				if !strings.Contains(body, required) {
					t.Fatalf("RunDecision-like equality lacks recursive pure lowering %q:\n%s", required, body)
				}
			}
			for _, forbidden := range []string{"StringEqual", "EfaceEqual", "IfaceType"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("RunDecision-like equality unexpectedly calls helper %q:\n%s", forbidden, body)
				}
			}
			runCoroABITestPipeline(t, prog, module)
			if resume := module.NamedFunction("foo.Leaf$coro.resume"); resume.IsNil() {
				t.Fatalf("CoroSplit did not materialize aggregate equality resume entry:\n%s", module.String())
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit aggregate equality object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 {
				t.Fatal("aggregate equality emitted an empty object")
			}
		})
	}
}

func TestCoroPureAggregateEqualityRejectsHelperBackedLeaves(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "string field",
			source: `package foo
type Value struct { Count uint32; Text string }
func Root(left, right Value) bool { return left == right }
`,
		},
		{
			name: "nested string array",
			source: `package foo
type Value struct { Text [2]string }
func Root(left, right Value) bool { return left != right }
`,
		},
		{
			name: "interface field",
			source: `package foo
type Value struct { Payload any }
func Root(left, right Value) bool { return left == right }
`,
		},
		{
			name: "nested interface array",
			source: `package foo
type Value struct { Payload [2]any }
func Root(left, right Value) bool { return left != right }
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, _, _, root, audit, _ := prepareCoroFrameRootAudit(t, test.source, "Root", EmissionUniverseOptions{})
			defer prog.Dispose()
			found := false
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					operation, ok := instruction.(*ssa.BinOp)
					if !ok || (operation.Op != token.EQL && operation.Op != token.NEQ) {
						continue
					}
					found = true
					handled, reason := audit.validate(operation)
					if !handled || !strings.Contains(reason, "aggregate equality contains a helper-backed or unsupported element") {
						t.Fatalf("helper-backed aggregate equality validation = handled %t, reason %q", handled, reason)
					}
				}
			}
			if !found {
				t.Fatal("helper-backed fixture has no aggregate equality")
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
			want: "heap allocation requires managed allocation",
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
			name: "allocating interface box",
			source: `package foo
func Root(value uint64) any { return any(value) }
`,
			want: "structured runtime helper validation requires a frozen emission universe",
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
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			root := ssaPkg.Func("Root")
			plan := coro.FunctionPlan{
				ID:            coro.FunctionID("foo.Root"),
				External:      coro.Defined,
				Demand:        coro.AsyncDemand,
				ManagedDemand: coro.AsyncDemand,
				Emission:      coro.EmitCoroutine,
				Primary:       coro.PrimaryCoroutine,
				FuncRep:       coro.DirectCoro,
				Effect:        coro.YieldOnly,
			}
			err = validateCoroPhysicalABIWithUniverse(root, plan, nil, universe, true, true)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCoroPureSSAChangeInterfaceUsesExactHelperInventory(t *testing.T) {
	prog, _, universe, root, audit, _ := prepareCoroFrameRootAudit(t, `package foo
type Source interface { First(); Second() }
type Target interface { First() }
func Root(value Source) Target { return Target(value) }
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	var change *ssa.ChangeInterface
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.ChangeInterface); ok {
				change = candidate
			}
		}
	}
	if change == nil {
		t.Fatal("fixture has no ChangeInterface")
	}
	if helpers := universe.loweredRuntimeHelpers(audit.ctx, change); strings.Join(helpers, ",") != "IfaceType,NewItab" {
		t.Fatalf("non-empty interface conversion helpers = %v; want IfaceType, NewItab", helpers)
	}
	if handled, reason := audit.validate(change); !handled || !strings.Contains(reason, "structured runtime helper validation requires a frozen emission universe") {
		t.Fatalf("non-empty interface conversion validation = handled %t, reason %q", handled, reason)
	}
}

func TestCoroPureSSATypeAssertUsesExactHelperInventory(t *testing.T) {
	for _, test := range []struct {
		name        string
		source      string
		wantHelpers string
		wantReason  string
	}{
		{
			name: "empty interface comma ok concrete",
			source: `package foo
func Root(value any) (string, bool) {
	result, ok := value.(string)
	return result, ok
}
`,
		},
		{
			name: "empty interface single concrete",
			source: `package foo
func Root(value any) string { return value.(string) }
`,
			wantHelpers: "PanicTypeAssert",
			wantReason:  "structured runtime helper validation requires a frozen emission universe",
		},
		{
			name: "nonempty interface comma ok concrete",
			source: `package foo
type Value string
func (Value) M() {}
type Source interface { M() }
func Root(value Source) (Value, bool) {
	result, ok := value.(Value)
	return result, ok
}
`,
			wantHelpers: "IfaceType",
			wantReason:  "structured runtime helper validation requires a frozen emission universe",
		},
		{
			name: "nonempty interface comma ok interface",
			source: `package foo
type Source interface { M() }
type Target interface { M(); N() }
func Root(value Source) (Target, bool) {
	result, ok := value.(Target)
	return result, ok
}
`,
			wantHelpers: "IfaceType,Implements,NewItab",
			wantReason:  "structured runtime helper validation requires a frozen emission universe",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, _, universe, root, audit, _ := prepareCoroFrameRootAudit(t, test.source, "Root", EmissionUniverseOptions{})
			defer prog.Dispose()
			var assertion *ssa.TypeAssert
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					if candidate, ok := instruction.(*ssa.TypeAssert); ok {
						assertion = candidate
					}
				}
			}
			if assertion == nil {
				t.Fatal("fixture has no TypeAssert")
			}
			if got := strings.Join(universe.loweredRuntimeHelpers(audit.ctx, assertion), ","); got != test.wantHelpers {
				t.Fatalf("type assertion helpers = %q; want %q", got, test.wantHelpers)
			}
			handled, reason := audit.validate(assertion)
			if !handled || reason != test.wantReason {
				t.Fatalf("type assertion validation = handled %t, reason %q; want reason %q", handled, reason, test.wantReason)
			}
		})
	}
}

func TestCoroPureSSAStringConversionsUseExactHelperInventory(t *testing.T) {
	const source = `package foo
func FromBytes(value []byte) string { return string(value) }
func FromRunes(value []rune) string { return string(value) }
func FromInt(value int) string { return string(value) }
func FromUint(value uint) string { return string(value) }
func ToBytes(value string) []byte { return []byte(value) }
func ToRunes(value string) []rune { return []rune(value) }
`
	for function, helper := range map[string]string{
		"FromBytes": "StringFromBytes",
		"FromRunes": "StringFromRunes",
		"FromInt":   "StringFromInt64",
		"FromUint":  "StringFromUint64",
		"ToBytes":   "StringToBytes",
		"ToRunes":   "StringToRunes",
	} {
		t.Run(function, func(t *testing.T) {
			prog, _, universe, root, audit, _ := prepareCoroFrameRootAudit(t, source, function, EmissionUniverseOptions{})
			defer prog.Dispose()
			var conversion *ssa.Convert
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					if candidate, ok := instruction.(*ssa.Convert); ok {
						conversion = candidate
					}
				}
			}
			if conversion == nil {
				t.Fatal("fixture has no Convert")
			}
			if got := strings.Join(universe.loweredRuntimeHelpers(audit.ctx, conversion), ","); got != helper {
				t.Fatalf("conversion helpers = %q; want %q", got, helper)
			}
			if handled, reason := audit.validate(conversion); !handled || reason != "runtime helper capability validation requires a frozen emission universe" {
				t.Fatalf("string conversion validation = handled %t, reason %q", handled, reason)
			}
		})
	}
}

func TestCoroPureSSAStringComparisonsUseExactHelperInventory(t *testing.T) {
	for _, test := range []struct {
		name   string
		op     string
		helper string
	}{
		{name: "equal", op: "==", helper: "StringEqual"},
		{name: "not-equal", op: "!=", helper: "StringEqual"},
		{name: "less", op: "<", helper: "StringLess"},
		{name: "less-equal", op: "<=", helper: "StringLess"},
		{name: "greater", op: ">", helper: "StringLess"},
		{name: "greater-equal", op: ">=", helper: "StringLess"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package foo\nfunc Root(left, right string) bool { return left " + test.op + " right }\n"
			prog, _, universe, root, audit, _ := prepareCoroFrameRootAudit(t, source, "Root", EmissionUniverseOptions{})
			defer prog.Dispose()
			var comparison *ssa.BinOp
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					if candidate, ok := instruction.(*ssa.BinOp); ok {
						comparison = candidate
					}
				}
			}
			if comparison == nil {
				t.Fatal("fixture has no BinOp")
			}
			if got := strings.Join(universe.loweredRuntimeHelpers(audit.ctx, comparison), ","); got != test.helper {
				t.Fatalf("comparison helpers = %q; want %q", got, test.helper)
			}
			if handled, reason := audit.validate(comparison); !handled || reason != "runtime helper capability validation requires a frozen emission universe" {
				t.Fatalf("string comparison validation = handled %t, reason %q", handled, reason)
			}
		})
	}
}

func TestCoroPureSSASignedShiftUsesExplicitStatusOutcome(t *testing.T) {
	prog, _, universe, root, audit, _ := prepareCoroFrameRootAudit(t, `package foo
func Root(value uint64, count int) uint64 { return value >> count }
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	var shift *ssa.BinOp
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.BinOp); ok && candidate.Op == token.SHR {
				shift = candidate
			}
		}
	}
	if shift == nil {
		t.Fatal("fixture has no signed-count shift")
	}
	if helpers := universe.loweredRuntimeHelpers(audit.ctx, shift); strings.Join(helpers, ",") != "AssertNegativeShift" {
		t.Fatalf("signed shift helpers = %v; want AssertNegativeShift", helpers)
	}
	if handled, reason := audit.validate(shift); !handled || reason != "potentially panicking runtime helper requires the explicit-status panic ABI" {
		t.Fatalf("signed shift without ExplicitStatus = handled %t, reason %q", handled, reason)
	}
	audit.allowImplicitNilFault = true
	if handled, reason := audit.validate(shift); !handled || reason != "runtime helper capability validation requires a frozen emission universe" {
		t.Fatalf("signed shift with ExplicitStatus = handled %t, reason %q", handled, reason)
	}
}

func TestCoroPureSSAGlobalPointerStoreRequiresExactNonMovingProfile(t *testing.T) {
	prog, _, _, root, audit, _ := prepareCoroFrameRootAudit(t, `package foo
var Global *uint32
func Root(value *uint32) { Global = value }
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()
	var store *ssa.Store
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.Store); ok {
				store = candidate
			}
		}
	}
	if store == nil {
		t.Fatal("fixture has no global pointer Store")
	}
	const want = "global typed store of a pointer-containing value requires explicit write-barrier lowering"
	if reason := audit.validateStore(store); reason != want {
		t.Fatalf("unprofiled global pointer store reason = %q; want %q", reason, want)
	}
	audit.frameRetentionABI = CoroFrameRetentionParkABIV2
	if reason := audit.validateStore(store); reason != "" {
		t.Fatalf("non-moving profile global pointer store rejected: %s", reason)
	}

	old := emitShadowStackInstrumentation
	emitShadowStackInstrumentation = true
	defer func() { emitShadowStackInstrumentation = old }()
	if reason := audit.validateStore(store); reason != want {
		t.Fatalf("precise/shadow profile global pointer store reason = %q; want %q", reason, want)
	}
}

func TestCoroPureSSANilComparisonsFollowGoSemantics(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
import "unsafe"
func Interface(value error) bool { return value != nil }
func Slice(value []byte) bool { return value == nil }
func Unsafe(value unsafe.Pointer) bool { return value != nil }
`)
	for _, name := range []string{"Interface", "Slice", "Unsafe"} {
		fn := ssaPkg.Func(name)
		audit := &coroPhysicalPureSSAAudit{fn: fn, reachableBlocks: coroPhysicalConstantReachableBlocks(fn)}
		found := false
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				operation, ok := instruction.(*ssa.BinOp)
				if !ok {
					continue
				}
				found = true
				if reason := audit.validateBinOp(operation); reason != "" {
					t.Fatalf("%s nil comparison rejected: %s", name, reason)
				}
			}
		}
		if !found {
			t.Fatalf("%s fixture has no binary nil comparison", name)
		}
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
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
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
