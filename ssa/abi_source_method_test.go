package ssa

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"runtime"
	"strings"
	"testing"
)

func TestPhysicalABIMethodSelectionsUseExactSource(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "methods.go", `package methods
type Box[T any] struct { F func(T) T }
func (b Box[T]) Value() func(T) T { return b.F }
func (b *Box[T]) Pointer() {}
type Holder struct { *Box[int] }
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{}).Check("example.com/abimethods", fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	box, err := types.Instantiate(nil, pkg.Scope().Lookup("Box").Type(), []types.Type{types.Typ[types.Int]}, true)
	if err != nil {
		t.Fatal(err)
	}
	anonymous := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, pkg, "Box", types.NewPointer(box), true),
	}, nil)
	for _, source := range []types.Type{box, types.NewPointer(box), pkg.Scope().Lookup("Holder").Type(), anonymous, types.NewPointer(anonymous)} {
		t.Run(source.String(), func(t *testing.T) {
			prog := NewProgram(nil)
			defer prog.Dispose()
			physical := prog.PhysicalType(source, InGo)
			if types.Identical(physical, source) {
				t.Fatal("fixture did not produce distinct physical method receiver")
			}
			compiled := 0
			prog.compileMethods = func(_ Package, typ types.Type) {
				compiled++
				if !types.Identical(typ, source) {
					t.Fatalf("GoSSA demand receiver = %v, want exact source %v", typ, source)
				}
			}
			builder := &aBuilder{Prog: prog, Pkg: prog.NewPackage("methods", "example.com/abimethods")}
			got, ok := builder.abiUncommonMethodSet(physical)
			want := types.NewMethodSet(source)
			if !ok || compiled != 1 || got.Len() != want.Len() {
				t.Fatalf("method selection ok=%v compiled=%d count=%d, want %d", ok, compiled, got.Len(), want.Len())
			}
			for index := 0; index < want.Len(); index++ {
				if got.At(index).Obj() != want.At(index).Obj() || !types.Identical(got.At(index).Recv(), source) ||
					!types.Identical(got.At(index).Type(), want.At(index).Type()) {
					t.Fatalf("method %d lost source object/receiver/signature", index)
				}
			}
			if ptr, static := prog.staticPtrToThis(physical); static && ptr.(*types.Pointer).Elem() != physical {
				t.Fatal("source method selection changed physical descriptor storage")
			}
		})
	}
}

func TestABITypeSourceIdentityAcrossPrograms(t *testing.T) {
	runtimeTypes := RuntimeTypesForTest(t)
	newProgram := func() Program {
		p := NewProgram(nil)
		p.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
		p.SetRuntime(runtimeTypes)
		return p
	}
	typesPkg := types.NewPackage("example.com/abisource", "abisource")
	object := types.NewTypeName(token.NoPos, typesPkg, "Named", nil)
	callback := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	named := types.NewNamed(object, types.NewStruct([]*types.Var{types.NewField(0, typesPkg, "F", callback, false)}, nil), nil)
	typesPkg.Scope().Insert(object)
	named.AddMethod(types.NewFunc(0, typesPkg, "M", types.NewSignatureType(types.NewVar(0, typesPkg, "", named), nil, nil, nil, nil, false)))
	anonymous := types.NewStruct([]*types.Var{types.NewField(0, typesPkg, "Named", types.NewPointer(named), true)}, nil)
	for _, source := range []types.Type{named, anonymous, types.NewPointer(anonymous)} {
		t.Run(source.String(), func(t *testing.T) {
			producer := newProgram()
			physical := producer.PhysicalType(source, InGo)
			pkg := producer.NewPackage("producer", "example.com/producer")
			builder := pkg.NewFunc("producer", NoArgsNoRet, InC).MakeBody(1)
			builder.abiType(physical)
			builder.Return()
			infos := producer.AbiTypes()
			producer.Dispose()
			var original AbiTypeInfo
			for _, info := range infos {
				if info.Raw == physical {
					original = info
				}
			}
			if original.source != source {
				t.Fatal("producer snapshot lost exact source identity")
			}
			consumer := newProgram()
			defer consumer.Dispose()
			consumerPkg := consumer.NewPackage("consumer", "example.com/consumer")
			consumerPkg.RegisterAbiTypes(infos)
			consumerPkg.RegisterAbiTypes(infos)
			if consumer.SourceType(physical) != source || consumer.abiSymbol[original.Name].Raw != physical {
				t.Fatal("consumer conflated source selection and physical layout")
			}
			methods := consumer.abiSymbol[original.Name].MSet
			if methods.Len() == 0 || !types.Identical(methods.At(0).Recv(), source) {
				t.Fatal("cross-Program promoted receiver retained physical identity")
			}
			for _, info := range consumer.AbiTypes() {
				if info.Name == original.Name && info.source != source {
					t.Fatal("second export lost the source projection")
				}
			}
			assertPanic := func(info AbiTypeInfo, want string) {
				t.Helper()
				defer func() {
					if got := fmt.Sprint(recover()); !strings.Contains(got, want) {
						t.Fatalf("invalid ABI source panic = %q, want %q", got, want)
					}
				}()
				consumerPkg.RegisterAbiTypes([]AbiTypeInfo{info})
			}
			missing := original
			missing.source = nil
			assertPanic(missing, "no exact source")
			wrong := original
			wrong.source = types.NewNamed(types.NewTypeName(0, typesPkg, "Named", nil), named.Underlying(), nil)
			assertPanic(wrong, "conflicting source identities")
			wrong.Name += ".other"
			assertPanic(wrong, "conflicts with its exact source projection")
		})
	}
}

func TestPhysicalNamedABISymbolNamesIgnoreFileSetPositions(t *testing.T) {
	runtimeTypes := RuntimeTypesForTest(t)
	newProgram := func() Program {
		p := NewProgram(nil)
		p.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
		p.SetRuntime(runtimeTypes)
		return p
	}
	typesPkg := types.NewPackage("example.com/pos", "pos")
	cb := types.NewSignatureType(nil, nil, nil, nil, nil, false)

	// 1. Top-level struct with a func field and a non-zero FileSet token.Pos.
	poolObj := types.NewTypeName(token.Pos(3148538), typesPkg, "Pool", nil)
	poolNamed := types.NewNamed(poolObj, types.NewStruct([]*types.Var{
		types.NewField(token.Pos(3148540), typesPkg, "New", cb, false),
	}, nil), nil)
	typesPkg.Scope().Insert(poolObj)

	// 2. Generic top-level struct with a func field and a non-zero token.Pos.
	anyIface := types.Universe.Lookup("any").Type().Underlying().(*types.Interface)
	kParam := types.NewTypeParam(types.NewTypeName(token.Pos(3081676), typesPkg, "K", nil), anyIface)
	vParam := types.NewTypeParam(types.NewTypeName(token.Pos(3081677), typesPkg, "V", nil), anyIface)
	htObj := types.NewTypeName(token.Pos(3081675), typesPkg, "HashTrieMap", nil)
	htOrigin := types.NewNamed(htObj, types.NewStruct([]*types.Var{
		types.NewField(token.Pos(3081680), typesPkg, "eq", cb, false),
	}, nil), nil)
	htOrigin.SetTypeParams([]*types.TypeParam{kParam, vParam})
	typesPkg.Scope().Insert(htObj)
	htInstType, err := types.Instantiate(types.NewContext(), htOrigin, []types.Type{anyIface, anyIface}, false)
	if err != nil {
		t.Fatalf("instantiate HashTrieMap: %v", err)
	}
	htInst := htInstType.(*types.Named)

	// 3. Function-local struct with a func field and a non-zero token.Pos.
	fnScope := types.NewScope(typesPkg.Scope(), token.Pos(4000000), token.Pos(4000100), "fn")
	localObj := types.NewTypeName(token.Pos(4000050), typesPkg, "localItem", nil)
	localNamed := types.NewNamed(localObj, types.NewStruct([]*types.Var{
		types.NewField(token.Pos(4000060), typesPkg, "fn", cb, false),
	}, nil), nil)
	fnScope.Insert(localObj)

	// 4. Composite map containing converted named types.
	mapTyp := types.NewMap(types.NewPointer(poolNamed), types.NewPointer(htInst))

	producer := newProgram()
	defer producer.Dispose()
	pkg := producer.NewPackage("pos", "example.com/pos")
	b := pkg.NewFunc("use", NoArgsNoRet, InC).MakeBody(1)

	cases := []struct {
		name    string
		source  types.Type
		wantSym string
		wantPub bool
	}{
		{"top-level", poolNamed, "_llgo_example.com/pos.Pool", true},
		{"generic-instance", htInst, "_llgo_example.com/pos.HashTrieMap[interface{},interface{}]", true},
		{"function-local", localNamed, "_llgo_example.com/pos.localItem.0", false},
		{"composite-map", mapTyp, "map[*_llgo_example.com/pos.Pool]*_llgo_example.com/pos.HashTrieMap[interface{},interface{}]", true},
	}
	physicals := make([]types.Type, len(cases))
	for i, tc := range cases {
		phys := producer.PhysicalType(tc.source, InGo)
		physicals[i] = phys
		b.abiType(phys)
		got, pub := producer.abi.TypeName(phys)
		if got != tc.wantSym || pub != tc.wantPub {
			t.Fatalf("%s producer TypeName = (%q, %v), want (%q, %v)", tc.name, got, pub, tc.wantSym, tc.wantPub)
		}
	}
	b.Return()

	infos := producer.AbiTypes()
	consumer := newProgram()
	defer consumer.Dispose()
	consumerPkg := consumer.NewPackage("consumer", "example.com/consumer")
	consumerPkg.RegisterAbiTypes(infos)
	for i, tc := range cases {
		got, pub := consumer.abi.TypeName(physicals[i])
		if got != tc.wantSym || pub != tc.wantPub {
			t.Fatalf("%s consumer TypeName = (%q, %v), want (%q, %v)", tc.name, got, pub, tc.wantSym, tc.wantPub)
		}
	}
}
