package cl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestRuntimeLocationPositionPreservesEmptyDirectiveFilename(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "/work/original.go", `package p
func f() {
//line :17
g()
/*line :23*/ g()
}
`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var calls []*ast.CallExpr
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			calls = append(calls, call)
		}
		return true
	})
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	for index, line := range []int{17, 23} {
		pos := calls[index].Lparen
		position, ok := runtimeLocationPosition(fset, pos)
		if !ok || position.Filename != "" || position.Line != line {
			t.Fatalf("location %d = (%+v, %t), want empty filename:%d", index, position, ok, line)
		}
		if got := directiveFilename(fset, pos, position.Filename, nil); got != "??" {
			t.Fatalf("runtime filename = %q, want ??", got)
		}
	}
	if _, ok := runtimeLocationPosition(fset, token.NoPos); ok {
		t.Fatal("NoPos became a source location")
	}
	if _, ok := runtimeLocationPosition(nil, calls[0].Lparen); ok {
		t.Fatal("nil FileSet became a source location")
	}
}
