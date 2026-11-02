//go:build !llgo

package cl

import (
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Keep the original eager-copy owner resolution as a semantic oracle. In
// particular, syntaxless objects and inherited closure substitutions must not
// turn a consumer into a local type's defining function.
func legacyLocalGenericTypeContext(p *context, typ *types.Named) *context {
	if p == nil || p.goFn == nil || typ == nil || typ.Obj() == nil {
		return nil
	}
	copied := *p
	for fn := p.goFn; fn != nil; fn = fn.Parent() {
		if len(fn.TypeArgs()) == 0 {
			continue
		}
		copied.goFn = fn
		if !copied.isGenericLocalType(typ.Obj()) {
			continue
		}
		if pos := typ.Obj().Pos(); pos.IsValid() {
			syntax := sourceFunctionSyntax(fn)
			if syntax == nil || pos < syntax.Pos() || pos > syntax.End() {
				continue
			}
		}
		return &copied
	}
	return nil
}

func TestLocalGenericContextMatchesLegacyOwners(t *testing.T) {
	pkg := buildSSAPackage(t, `package foo
 type Top struct { Value int }
 func plain() any { type Local struct { N int }; return Local{} }
 func outer[T any](value T) any {
  type Local struct { Value T }
  closure := func() any { nested := func() any { return Local{value} }; return nested() }
  return consume(closure())
 }
 func consume[T any](value T) any { return value }
 func use() { _ = outer[int](1); _ = outer[string]("s"); _ = plain(); _ = consume(Top{}) }
 `)
	owner, local := firstGenericLocalNamed(t, pkg)
	top := pkg.Pkg.Scope().Lookup("Top").Type().(*types.Named)
	syntax := sourceFunctionSyntax(owner)
	if syntax == nil {
		t.Fatal("fixture has no generic owner syntax")
	}
	manual := types.NewNamed(types.NewTypeName(syntax.Pos()+1, pkg.Pkg, "Parentless", nil), types.NewStruct(nil, nil), nil)
	outside := types.NewNamed(types.NewTypeName(token.Pos(1), pkg.Pkg, "Outside", nil), types.NewStruct(nil, nil), nil)
	scope := types.NewScope(pkg.Pkg.Scope(), token.NoPos, token.NoPos, "generic")
	param := types.NewTypeParam(types.NewTypeName(token.NoPos, pkg.Pkg, "T", nil), types.NewInterfaceType(nil, nil).Complete())
	scope.Insert(types.NewTypeName(token.NoPos, pkg.Pkg, "T", param))
	noPosObj := types.NewTypeName(token.NoPos, pkg.Pkg, "Scoped", nil)
	scope.Insert(noPosObj)
	noPos := types.NewNamed(noPosObj, types.NewStruct(nil, nil), nil)
	types_ := []*types.Named{nil, top, local, manual, outside, noPos}
	contexts := []*context{nil, {}}
	for fn := range ssautil.AllFunctions(pkg.Prog) {
		if fn == nil {
			continue
		}
		contexts = append(contexts, &context{goFn: fn, goTyps: pkg.Pkg, goPkg: pkg, fset: pkg.Prog.Fset})
	}
	successes, rejects := 0, 0
	for _, ctx := range contexts {
		for _, typ := range types_ {
			got, want := ctx.localGenericTypeContext(typ), legacyLocalGenericTypeContext(ctx, typ)
			if (got == nil) != (want == nil) {
				t.Fatalf("owner changed for context %p type %v", ctx, typ)
			}
			if got == nil {
				rejects++
				continue
			}
			successes++
			if !reflect.DeepEqual(*got, *want) {
				t.Fatalf("copied context changed for type %v", typ)
			}
			if got == ctx {
				t.Fatal("successful query returned its mutable input context")
			}
			original := ctx.goFn
			got.goFn = nil
			if ctx.goFn != original {
				t.Fatal("owner query mutated its caller")
			}
		}
	}
	if successes == 0 || rejects == 0 {
		t.Fatal("owner fixture omitted positive/negative queries")
	}
}

func TestLocalGenericContextUnownedQueriesAllocateZero(t *testing.T) {
	pkg := buildSSAPackage(t, `package foo
 type Top struct { N int }
 func plain() { closure := func() {};closure() }
 `)
	top := pkg.Pkg.Scope().Lookup("Top").Type().(*types.Named)
	local := types.NewNamed(types.NewTypeName(pkg.Func("plain").Syntax().Pos()+1, pkg.Pkg, "Parentless", nil), types.NewStruct(nil, nil), nil)
	for fn := range ssautil.AllFunctions(pkg.Prog) {
		if fn == nil {
			continue
		}
		ctx := &context{goFn: fn}
		for _, typ := range []*types.Named{top, local} {
			if got := ctx.localGenericTypeContext(typ); got != nil {
				t.Fatal("nongeneric fixture acquired an owner")
			}
			if n := testing.AllocsPerRun(100, func() {
				if ctx.localGenericTypeContext(typ) != nil {
					panic("unexpected owner")
				}
			}); n != 0 {
				t.Fatalf("unowned query from %s allocated %v times", fn, n)
			}
		}
	}
}

func TestLocalGenericContextScopedUnownedQueriesAllocateZero(t *testing.T) {
	pkg := buildSSAPackage(t, `package foo
 func plain() any {
  type Local struct { N int }
  closure := func() any { return Local{7} }
  return closure()
 }
 func consume[T any](value T) any { return value }
 func use() { _ = plain(); _ = consume(1) }
 `)
	var local *types.Named
	var consumers []*ssa.Function
	for fn := range ssautil.AllFunctions(pkg.Prog) {
		if fn == nil {
			continue
		}
		if len(fn.TypeArgs()) != 0 && strings.HasPrefix(fn.Name(), "consume") {
			consumers = append(consumers, fn)
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if value, ok := instruction.(*ssa.MakeInterface); ok {
					if named := findLocalNamed(value.X.Type(), pkg.Pkg); named != nil && named.Obj().Name() == "Local" {
						local = named
					}
				}
			}
		}
	}
	if local == nil || local.Obj().Parent() == nil {
		t.Fatal("fixture must use a real scope-owned local declaration")
	}
	plain := pkg.Func("plain")
	if plain == nil || len(plain.AnonFuncs) == 0 || len(consumers) == 0 {
		t.Fatal("fixture omitted plain, closure, or instantiated consumer")
	}
	functions := append([]*ssa.Function{plain}, plain.AnonFuncs...)
	functions = append(functions, consumers...)
	for _, fn := range functions {
		t.Run(fn.String(), func(t *testing.T) {
			ctx := &context{goFn: fn, goTyps: pkg.Pkg, goPkg: pkg, fset: pkg.Prog.Fset}
			if legacyLocalGenericTypeContext(ctx, local) != nil || ctx.localGenericTypeContext(local) != nil {
				t.Fatal("nongeneric scoped declaration acquired a generic owner")
			}
			allocations := testing.AllocsPerRun(100, func() {
				if ctx.localGenericTypeContext(local) != nil {
					panic("unexpected generic owner")
				}
			})
			t.Logf("scoped unowned query allocated %v times", allocations)
			if allocations != 0 {
				t.Fatalf("scoped unowned query allocated %v times, want zero", allocations)
			}
		})
	}
}
