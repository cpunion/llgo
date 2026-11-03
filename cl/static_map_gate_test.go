//go:build !llgo

package cl

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestStaticMapGateSelectsOnlyFrozenPureIntervals(t *testing.T) {
	test := newEmissionTestProgram()
	pkg := test.addPackage(t, "example.com/emission/gate", `package gate
var M = map[string]int{"a": 1}
func Read() int { return M["a"] }
`)
	test.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	prog.TypeSizes(types.SizesFor("gc", "arm64"))
	u, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := u.StaticMapInitializers()
	if err != nil || len(facts) != 1 || facts[0].GoUnread || facts[0].RawInputsClosed {
		t.Fatalf("facts=%+v err=%v", facts, err)
	}
	compiler := &context{emissionUniverse: u}
	init := pkg.ssa.Func("init")
	for _, block := range init.Blocks {
		if len(compiler.staticMapGateRegions(block, 0, len(block.Instrs))) != 0 {
			t.Fatal("default options enabled canonical gates")
		}
	}
	compiler.options.CanonicalStaticMapInits = true
	count := 0
	for _, block := range init.Blocks {
		regions := compiler.staticMapGateRegions(block, 0, len(block.Instrs))
		count += len(regions)
		for start, region := range regions {
			if _, ok := start.(*ssa.MakeMap); !ok || region.end == nil || region.producer.GateSymbol != facts[0].GateSymbol {
				t.Fatalf("invalid retained constructor region: %+v", region)
			}
		}
	}
	if count != 1 {
		t.Fatalf("retained canonical gates=%d; want 1 even when a consumer reads the map", count)
	}
	compiler.pyMod = "foreign-module"
	for _, block := range init.Blocks {
		if len(compiler.staticMapGateRegions(block, 0, len(block.Instrs))) != 0 {
			t.Fatal("Python lowering acquired a canonical native gate")
		}
	}
}

func buildStaticMapGateIR(t *testing.T) (llssa.Program, llssa.Package, StaticMapInitProducer) {
	t.Helper()
	prog := llssa.NewProgram(nil)
	pkg := prog.NewPackage("mapgate", "example.com/mapgate")
	params := types.NewTuple(types.NewVar(token.NoPos, nil, "choose", types.Typ[types.Bool]))
	results := types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Typ[types.Int32]))
	fn := pkg.NewFunc("probe", types.NewSignatureType(nil, nil, nil, params, results, false), llssa.InGo)
	b := fn.MakeBody(4)
	defer b.Dispose()
	then, otherwise, merge := fn.Block(1), fn.Block(2), fn.Block(3)
	b.If(fn.Param(0), then, otherwise)
	b.SetBlock(then)
	compiler := &context{prog: prog, pkg: pkg, fn: fn}
	producer := StaticMapInitProducer{
		Version: 1, PackageIdentity: "mapgate", PhysicalSymbol: "example.com/mapgate.M",
		ConstructorID: strings.Repeat("a", 64),
		GateSymbol:    "__llgo_static_map_init_live_v1_" + strings.Repeat("a", 64), Entries: 1,
	}
	end := &ssa.Store{}
	region := &staticMapGateRegion{producer: producer, end: end}
	compiler.beginStaticMapGate(b, region)
	// A side-effecting body makes the conditional region observable in IR.
	result := pkg.NewVar("body_result", types.NewPointer(types.Typ[types.Int32]), llssa.InGo)
	result.InitNil()
	b.Store(result.Expr, prog.IntVal(171, prog.Int32()))
	finishStaticMapGate(b, end, &region)
	if region != nil {
		t.Fatal("source endpoint failed to close its gate")
	}
	b.Jump(merge)
	b.SetBlock(otherwise)
	b.Jump(merge)
	b.SetBlock(merge)
	phi := b.Phi(prog.Int32())
	b.Return(phi.Expr)
	phi.AddIncoming(b, []llssa.BasicBlock{then, otherwise}, func(i int, _ llssa.BasicBlock) llssa.Expr {
		return prog.IntVal(uint64(i+7), prog.Int32())
	})
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		prog.Dispose()
		t.Fatal("gate expansion lost the logical source-block PHI predecessor:", err)
	}
	return prog, pkg, producer
}

func TestStaticMapGateRetainProducerAndLogicalContinuation(t *testing.T) {
	prog, pkg, producer := buildStaticMapGateIR(t)
	defer prog.Dispose()
	producers, err := StaticMapInitProducers(pkg)
	if err != nil || len(producers) != 1 || producers[0] != producer {
		t.Fatalf("producers=%+v err=%v", producers, err)
	}
	global := pkg.Module().NamedGlobal(producer.GateSymbol)
	if global.Initializer().ZExtValue() != 1 || global.Linkage() != llvm.WeakAnyLinkage || global.IsGlobalConstant() {
		t.Fatal("missing-override producer does not retain initialization")
	}
	ir := pkg.String()
	if !strings.Contains(ir, "load i8, ptr @"+producer.GateSymbol) || !strings.Contains(ir, "store i32 171") || !strings.Contains(ir, "phi i32") {
		t.Fatal("gate load/body/merge absent from emitted IR:\n", ir)
	}
}

func TestStaticMapGateProducerCaptureRejectsInvalidReceipts(t *testing.T) {
	for _, name := range []string{"zero-default", "constant-gate", "wrong-linkage", "duplicate", "malformed", "unknown-fields"} {
		t.Run(name, func(t *testing.T) {
			prog, pkg, producer := buildStaticMapGateIR(t)
			defer prog.Dispose()
			mod := pkg.Module()
			global := mod.NamedGlobal(producer.GateSymbol)
			switch name {
			case "zero-default":
				global.SetInitializer(llvm.ConstInt(mod.Context().Int8Type(), 0, false))
			case "constant-gate":
				global.SetGlobalConstant(true)
			case "wrong-linkage":
				global.SetLinkage(llvm.WeakODRLinkage)
			default:
				data, err := json.Marshal(producer)
				if err != nil {
					t.Fatal(err)
				}
				if name == "malformed" {
					data = []byte("not-json")
				} else if name == "unknown-fields" {
					data = append(data[:len(data)-1], []byte(",\"Unknown\":1}")...)
				}
				ctx := mod.Context()
				mod.AddNamedMetadataOperand(staticMapProducerMetadata, ctx.MDNode([]llvm.Metadata{ctx.MDString(string(data))}))
			}
			if _, err := StaticMapInitProducers(pkg); err == nil {
				t.Fatal("invalid producer was accepted")
			}
		})
	}
	if _, err := StaticMapInitProducers(nil); err == nil {
		t.Fatal("nil package accepted")
	}
}
