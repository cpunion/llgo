package ssa

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestNamedFunctionRawAndPhysicalTypeCacheIsolation(t *testing.T) {
	shapes := []struct {
		name string
		wrap func(*types.Named) types.Type
		leaf func(types.Type) types.Type
	}{
		{"direct", func(t *types.Named) types.Type { return t }, func(t types.Type) types.Type { return t }},
		{"pointer", func(t *types.Named) types.Type { return types.NewPointer(t) }, func(t types.Type) types.Type { return t.(*types.Pointer).Elem() }},
		{"array", func(t *types.Named) types.Type { return types.NewArray(t, 2) }, func(t types.Type) types.Type { return t.(*types.Array).Elem() }},
		{"tuple", func(t *types.Named) types.Type {
			return types.NewTuple(types.NewVar(0, nil, "f", t), types.NewVar(0, nil, "n", types.Typ[types.Int]))
		}, func(t types.Type) types.Type { return t.(*types.Tuple).At(0).Type() }},
		{"slice pointer", func(t *types.Named) types.Type { return types.NewPointer(types.NewSlice(t)) }, func(t types.Type) types.Type { return t.(*types.Pointer).Elem().(*types.Slice).Elem() }},
		{"named field", func(t *types.Named) types.Type {
			return types.NewNamed(types.NewTypeName(0, t.Obj().Pkg(), "Holder", nil), types.NewStruct([]*types.Var{types.NewField(0, nil, "F", t, false)}, nil), nil)
		}, func(t types.Type) types.Type { return t.Underlying().(*types.Struct).Field(0).Type() }},
		{"opaque named field", func(t *types.Named) types.Type {
			return types.NewNamed(types.NewTypeName(0, t.Obj().Pkg(), "Holder", nil), types.NewStruct([]*types.Var{types.NewField(0, nil, "F", types.NewPointer(t), false)}, nil), nil)
		}, func(t types.Type) types.Type {
			return t.Underlying().(*types.Struct).Field(0).Type().(*types.Pointer).Elem()
		}},
	}
	for _, shape := range shapes {
		for _, physicalFirst := range []bool{false, true} {
			t.Run(shape.name+"/"+map[bool]string{false: "raw-first", true: "physical-first"}[physicalFirst], func(t *testing.T) {
				p := NewProgram(nil)
				defer p.Dispose()
				pkg := types.NewPackage("example.com/namedresult", "namedresult")
				sig := types.NewSignatureType(nil, nil, nil, nil, nil, false)
				named := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Decompressor", nil), sig, nil)
				source := shape.wrap(named)
				var raw, physical Type
				if physicalFirst {
					physical, raw = p.Type(source, InGo), p.Type(source, InC)
				} else {
					raw, physical = p.Type(source, InC), p.Type(source, InGo)
				}
				if _, ok := shape.leaf(raw.RawType()).Underlying().(*types.Signature); !ok {
					t.Errorf("raw named function underlying = %T, want signature", shape.leaf(raw.RawType()).Underlying())
				}
				if closure, ok := shape.leaf(physical.RawType()).Underlying().(*types.Struct); !ok || !IsClosure(closure) {
					t.Errorf("physical named function underlying = %T, want closure", shape.leaf(physical.RawType()).Underlying())
				}
			})
		}
	}
}

func TestPhysicalNamedTypePreservesSourceScopeAndGenericCycle(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "named.go", `package namedresult
type Node[T any] struct { F func(T); Next *Node[T] }
func first() { type Local struct { F func() }; _ = Local{} }
func second() { type Local struct { F func() }; _ = Local{} }
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Defs: make(map[*ast.Ident]types.Object)}
	pkg, err := (&types.Config{}).Check("example.com/namedresult", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	p := NewProgram(nil)
	defer p.Dispose()
	localLLVMNames := make(map[string]bool)
	for ident, object := range info.Defs {
		if ident.Name != "Local" || object == nil {
			continue
		}
		source := object.Type().(*types.Named)
		physical := p.Type(source, InGo)
		if types.Identical(source, physical.RawType()) {
			t.Fatal("physical type retained source nominal identity")
		}
		if p.Type(source, InGo) != physical {
			t.Fatal("repeated conversion did not reuse its exact physical type")
		}
		name := physical.ll.StructName()
		if !strings.HasPrefix(name, "example.com/namedresult.Local#") || localLLVMNames[name] {
			t.Fatalf("local lexical disambiguation lost: %q", name)
		}
		localLLVMNames[name] = true
	}
	if len(localLLVMNames) != 2 {
		t.Fatalf("found %d local types, want 2", len(localLLVMNames))
	}
	source, err := types.Instantiate(nil, pkg.Scope().Lookup("Node").Type(), []types.Type{types.Typ[types.Int]}, true)
	if err != nil {
		t.Fatal(err)
	}
	physical := p.Type(source, InGo)
	named := physical.RawType().(*types.Named)
	underlying := named.Underlying().(*types.Struct)
	if next := underlying.Field(1).Type().(*types.Pointer).Elem(); next != named {
		t.Fatal("generic converted back-edge does not reference the physical instance")
	}
	if types.Identical(source, named) || p.Type(source, InGo) != physical {
		t.Fatal("generic conversion conflated source identity or failed canonical reuse")
	}
	if got := p.SourceType(named); got != source {
		t.Fatal("generic instance lost its exact source-instance projection")
	}
}

func TestNamedFunctionDeclarationResultCacheIsolation(t *testing.T) {
	for _, count := range []int{1, 2} {
		for _, first := range []Background{InC, InGo} {
			p := NewProgram(nil)
			defer p.Dispose()
			pkg := types.NewPackage("example.com/namedresult", "namedresult")
			named := types.NewNamed(types.NewTypeName(0, pkg, "Handler", nil), types.NewSignatureType(nil, nil, nil, nil, nil, false), nil)
			results := []*types.Var{types.NewVar(0, nil, "f", named)}
			if count == 2 {
				results = append(results, types.NewVar(0, nil, "n", types.Typ[types.Int]))
			}
			sig := types.NewSignatureType(nil, nil, nil, nil, types.NewTuple(results...), false)
			p.FuncDecl(sig, first)
			for _, bg := range []Background{InC, InGo} {
				decl := p.FuncDecl(sig, bg)
				result := decl.RawType().(*types.Signature).Results().At(0).Type()
				lowered := p.rawType(result)
				_, closure := lowered.RawType().Underlying().(*types.Struct)
				if closure != (bg == InGo) {
					t.Fatalf("%d-result declaration after %v: bg=%v closure=%v", count, first, bg, closure)
				}
			}
		}
	}
}

func TestPhysicalTypeSourceProjectionIsExact(t *testing.T) {
	p := NewProgram(nil)
	defer p.Dispose()
	pkg := types.NewPackage("example.com/projection", "projection")
	sig := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	named := types.NewNamed(types.NewTypeName(0, pkg, "Handler", nil), sig, nil)
	anonymous := types.NewStruct([]*types.Var{types.NewField(0, pkg, "Handler", named, true)}, nil)
	physical := p.PhysicalType(anonymous, InGo)
	if got := p.SourceType(physical); got != anonymous {
		t.Fatalf("anonymous source projection = %v, want exact source object", got)
	}
	derived := types.NewPointer(physical)
	if got := p.SourceType(derived).(*types.Pointer).Elem(); got != anonymous {
		t.Fatal("derived pointer did not preserve exact source element")
	}
	unknown := types.NewNamed(types.NewTypeName(0, pkg, "Handler", nil), named.Underlying(), nil)
	if got := p.SourceType(unknown); got != unknown {
		t.Fatal("same-name unknown type was incorrectly reverse-inferred")
	}
	recv := types.NewVar(0, pkg, "self", named)
	method := types.NewSignatureType(recv, nil, nil, nil, nil, false)
	closure := p.PhysicalType(method, InGo)
	if got := p.SourceType(closure); got != method {
		t.Fatal("method closure lost the original receiver signature")
	}
}

func TestEquivalentGenericInstancesSharePhysicalIdentity(t *testing.T) {
	prog := NewProgram(nil)
	defer prog.Dispose()
	pkg := types.NewPackage("example.com/genericidentity", "genericidentity")
	origin := func() *types.Named {
		parameter := types.NewTypeParam(types.NewTypeName(0, pkg, "T", nil), types.NewInterfaceType(nil, nil).Complete())
		callback := types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewParam(0, nil, "value", parameter)), nil, false)
		named := types.NewNamed(types.NewTypeName(0, pkg, "Box", nil), types.NewStruct([]*types.Var{types.NewField(0, pkg, "F", callback, false)}, nil), nil)
		named.SetTypeParams([]*types.TypeParam{parameter})
		return named
	}
	instantiate := func(named *types.Named) types.Type {
		value, err := types.Instantiate(nil, named, []types.Type{types.Typ[types.Int]}, true)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	definition := origin()
	first, second := instantiate(definition), instantiate(definition)
	if first == second || !types.Identical(first, second) {
		t.Fatal("fixture must rebuild the same nominal generic instance")
	}
	physical := prog.PhysicalType(first, InGo)
	if got := prog.PhysicalType(second, InGo); got != physical {
		t.Fatal("same Go generic identity acquired two physical nominal identities")
	}
	if got := prog.SourceType(physical); got != first {
		t.Fatal("conversion did not retain its first exact canonical source")
	}
	unrelated := instantiate(origin())
	if types.Identical(first, unrelated) || types.Identical(physical, prog.PhysicalType(unrelated, InGo)) {
		t.Fatal("same-name distinct generic definitions shared physical identity")
	}
}
