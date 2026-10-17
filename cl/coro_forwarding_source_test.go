//go:build !llgo

package cl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestCoroForwardingSourceShapeIsFrozenProgramIRInput(t *testing.T) {
	const source = `package foo
type Number int
func Child(first, second int) int { return first + second }
func Direct(first, second int) int { return Child(second, first) }
func Literal(value int) int { return Child(value, 17) }
func Retag(value Number) int { return Child(int(value), 1) }
//go:noinline
func NoInline(value int) int { return Child(value, 1) }
func ChangedResult(value int) int { return Child(value, 1) + 1 }
func TwoCalls(value int) int { Child(value, 1); return Child(value, 2) }
func Dynamic(call func(int, int) int, value int) int { return call(value, 1) }
func SourceProjection(value struct { Number int }) int { return Child(value.Number, 1) }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ir := universe.coroProgramIR
	for _, name := range []string{"Direct", "Literal", "Retag", "NoInline"} {
		shape, err := ir.forwardingSourceShape(ssaPkg.Func(name))
		if err != nil || shape == nil || shape.target != ssaPkg.Func("Child") || len(shape.args) != 2 {
			t.Fatalf("%s source shape = %+v, error=%v", name, shape, err)
		}
		// NoInline is deliberately a valid source observation. It still must
		// fail the separate physical frame-elision gate, not erase source facts.
		if name == "Direct" && (shape.args[0].sourceParameter != 1 || shape.args[1].sourceParameter != 0) {
			t.Fatal("forwarding parameter order was reconstructed incorrectly")
		}
		if name == "Literal" && (shape.args[1].sourceParameter != -1 || shape.args[1].constant == nil || shape.args[1].constant.Value.ExactString() != "17") {
			t.Fatal("forwarding literal lost its exact source value")
		}
		if name == "Retag" && (len(shape.adapters) != 1 || shape.args[0].direct || shape.args[0].sourceParameter != 0) {
			t.Fatal("forwarding retag lost its source parameter and adapter")
		}
	}
	for _, name := range []string{"ChangedResult", "TwoCalls", "Dynamic", "SourceProjection"} {
		shape, err := ir.forwardingSourceShape(ssaPkg.Func(name))
		if err != nil || shape != nil {
			t.Fatalf("unsupported %s acquired a forwarding source shape: %+v, error=%v", name, shape, err)
		}
	}
	if _, err := newCoroProgramIR().forwardingSourceShape(ssaPkg.Func("Direct")); err == nil {
		t.Fatal("unfrozen forwarding source lookup was accepted")
	}
	if _, err := ir.forwardingSourceShape(nil); err == nil {
		t.Fatal("forwarding source lookup without an exact function was accepted")
	}
}

func TestCoroForwardingShapeHasOneSourceOwnerAndNoLateBodyWalk(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	constructors := 0
	foundConsumer := false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			consumer := function.Name.Name == "planCoroPhysicalTailForward"
			foundConsumer = foundConsumer || consumer
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); consumer && ok {
					switch selector.Sel.Name {
					case "Blocks", "Instrs", "Locals":
						t.Errorf("physical forwarding consumer rediscovers source %s", selector.Sel.Name)
					}
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				identifier, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				if identifier.Name == "deriveCoroForwardingSourceShape" {
					constructors++
					if function.Name.Name != "freezeSiteOwner" {
						t.Errorf("source shape is reconstructed by %s", function.Name.Name)
					}
				}
				if consumer && (identifier.Name == "deriveCoroForwardingValueProjection" || identifier.Name == "validateCoroExactMethodWrapper") {
					t.Errorf("physical forwarding consumer calls source body reader %s", identifier.Name)
				}
				return true
			})
		}
	}
	if constructors != 1 || !foundConsumer {
		t.Fatalf("forwarding source constructors=%d physical consumer=%t; want one source owner and its consumer", constructors, foundConsumer)
	}
}
