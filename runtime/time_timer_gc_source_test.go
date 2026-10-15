//go:build !llgo

package runtime_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestTimeSleepTimerWeakOwnership(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "internal/lib/runtime/time_coro_go123_llgo.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundManager, foundDetached := false, false
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.TypeSpec:
			if n.Name.Name != "timeTimer" {
				return true
			}
			for _, field := range n.Type.(*ast.StructType).Fields.List {
				if len(field.Names) == 1 && field.Names[0].Name == "state" {
					ptr, ok := field.Type.(*ast.StarExpr)
					foundDetached = ok && ptr.X.(*ast.Ident).Name == "coroTimeTimerState"
				}
			}
		case *ast.FuncDecl:
			if n.Name.Name == "coroTimerManager" {
				ptr, ok := n.Type.Params.List[0].Type.(*ast.StarExpr)
				foundManager = ok && ptr.X.(*ast.Ident).Name == "coroTimeTimerState"
			}
		}
		return true
	})
	if !foundDetached || !foundManager {
		t.Fatal("parked manager must retain detached control state, not public Timer/channel")
	}
}

func TestTimeSleepGCObserverHasOnlyRawNonblockingCalls(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "internal/lib/runtime/time_coro_gc_llgo.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"CompareAndSwapUint32": true,
		"LoadUint32":           true, "LoadUintptr": true, "StoreUint32": true,
		"coroTimerControlPhase": true, "llgoCoroTimerRequestControlledV2": true,
	}
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "coroTimerGCObserveV1" {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if _, ok := node.(*ast.GoStmt); ok {
				t.Error("raw GC observer must not publish a managed G")
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			switch f := call.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			}
			if !allowed[name] {
				t.Errorf("raw observer gained unaudited call %q (no allocations, strong upgrade, timer locks or user calls)", name)
			}
			return true
		})
	}
	if !found {
		t.Fatal("missing raw GC observer")
	}
}
