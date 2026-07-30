package cl

import (
	"go/constant"
	"go/token"
	"go/types"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestReflectTypeMethodCheckRecordsDemands(t *testing.T) {
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackageEx("pkg", "pkg", true)
	fn := pkg.NewFunc("pkg.caller", types.NewSignatureType(nil, nil, nil, nil, nil, false), llssa.InGo)
	ctx := &context{pkg: pkg, fn: fn}

	reflectPkg := types.NewPackage("reflect", "reflect")
	iface := types.NewInterfaceType(nil, nil)
	iface.Complete()
	reflectType := types.NewNamed(types.NewTypeName(token.NoPos, reflectPkg, "Type", nil), iface, nil)
	recv := ssa.NewConst(nil, reflectType)
	method := func(name string) *types.Func {
		return types.NewFunc(token.NoPos, reflectPkg, name,
			types.NewSignatureType(nil, nil, nil, nil, nil, false))
	}

	check := ctx.reflectTypeMethodCheck(&ssa.CallCommon{
		Value: ssa.NewConst(constant.MakeInt64(0), types.Typ[types.Int]),
	}, method("Method"))
	if check != (llssa.ReflectMethodCheck{}) {
		t.Fatalf("non-reflect receiver check = %+v", check)
	}

	otherPkg := types.NewPackage("other", "other")
	otherMethod := types.NewFunc(token.NoPos, otherPkg, "Method",
		types.NewSignatureType(nil, nil, nil, nil, nil, false))
	check = ctx.reflectTypeMethodCheck(&ssa.CallCommon{Value: recv}, otherMethod)
	if check != (llssa.ReflectMethodCheck{}) {
		t.Fatalf("non-reflect method check = %+v", check)
	}

	check = ctx.reflectTypeMethodCheck(&ssa.CallCommon{Value: recv}, method("Method"))
	if check != (llssa.ReflectMethodCheck{}) {
		t.Fatalf("Method without index check = %+v", check)
	}
	check = ctx.reflectTypeMethodCheck(&ssa.CallCommon{Value: recv}, method("MethodByName"))
	if check != (llssa.ReflectMethodCheck{}) {
		t.Fatalf("MethodByName without name check = %+v", check)
	}

	check = ctx.reflectTypeMethodCheck(&ssa.CallCommon{
		Value: recv,
		Args:  []ssa.Value{ssa.NewConst(constant.MakeInt64(0), types.Typ[types.Int])},
	}, method("Method"))
	if check.Kind != llssa.ReflectTypeMethodByIndex {
		t.Fatalf("constant Method check = %+v", check)
	}

	check = ctx.reflectTypeMethodCheck(&ssa.CallCommon{
		Value: recv,
		Args:  []ssa.Value{&ssa.Parameter{}},
	}, method("Method"))
	if check.Kind != llssa.ReflectTypeMethodDynamic {
		t.Fatalf("dynamic Method check = %+v", check)
	}

	check = ctx.reflectTypeMethodCheck(&ssa.CallCommon{
		Value: recv,
		Args:  []ssa.Value{ssa.NewConst(constant.MakeString("Keep"), types.Typ[types.String])},
	}, method("MethodByName"))
	if check.Kind != llssa.ReflectTypeMethodByName || check.Name != "Keep" {
		t.Fatalf("constant MethodByName check = %+v", check)
	}

	check = ctx.reflectTypeMethodCheck(&ssa.CallCommon{
		Value: recv,
		Args:  []ssa.Value{&ssa.Parameter{}},
	}, method("MethodByName"))
	if check.Kind != llssa.ReflectTypeMethodDynamic|llssa.ReflectTypeMethodByName || check.Name != "" {
		t.Fatalf("dynamic MethodByName check = %+v", check)
	}

	if err := pkg.FinishMetaCollection(); err != nil {
		t.Fatal(err)
	}
	pm := pkg.Meta
	defer pm.Close()

	const want = `[UseNamedMethod]
pkg.caller:
    Keep

[Reflect]
    pkg.caller

`
	if got := pm.String(); got != want {
		t.Fatalf("metadata mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestReflectStaticCallABIInitKindUsesLogicalSSAIdentity(t *testing.T) {
	const source = `package foo
import (
	"reflect"
	"unsafe"
)
func Use(value reflect.Value, typ reflect.Type, pointer unsafe.Pointer) {
	_ = value.Addr()
	_ = value.Slice(0, 0)
	_ = value.Slice3(0, 0, 0)
	_ = reflect.ArrayOf(1, typ)
	_ = reflect.ChanOf(reflect.BothDir, typ)
	_ = reflect.FuncOf(nil, nil, false)
	_ = reflect.MapOf(typ, typ)
	_ = reflect.New(typ)
	_ = reflect.NewAt(typ, pointer)
	_ = reflect.PointerTo(typ)
	_ = reflect.PtrTo(typ)
	_ = reflect.SliceOf(typ)
	_ = reflect.StructOf(nil)
}
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	use := ssaPkg.Func("Use")
	got := make(map[string]int)
	for _, block := range use.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common().StaticCallee() == nil {
				continue
			}
			got[call.Common().StaticCallee().String()] = reflectStaticCallABIInitKind(call.Common())
		}
	}
	for name, want := range map[string]int{
		"(reflect.Value).Addr":   llssa.ReflectPointerTo,
		"(reflect.Value).Slice":  llssa.ReflectSliceOf,
		"(reflect.Value).Slice3": llssa.ReflectSliceOf,
		"reflect.ArrayOf":        llssa.ReflectArrayOf,
		"reflect.ChanOf":         llssa.ReflectChanOf,
		"reflect.FuncOf":         llssa.ReflectFuncOf,
		"reflect.MapOf":          llssa.ReflectMapOf,
		"reflect.New":            llssa.ReflectPointerTo,
		"reflect.NewAt":          llssa.ReflectPointerTo,
		"reflect.PointerTo":      llssa.ReflectPointerTo,
		"reflect.PtrTo":          llssa.ReflectPointerTo,
		"reflect.SliceOf":        llssa.ReflectSliceOf,
		"reflect.StructOf":       llssa.ReflectStructOf,
	} {
		if got[name] != want {
			t.Errorf("%s ABI-init kind = %d, want %d (all calls: %v)", name, got[name], want, got)
		}
	}
}

func TestCoroPhysicalCallDispatcherRecordsReflectABIInitDemand(t *testing.T) {
	const source = `package foo
import "reflect"
func Address(value reflect.Value) reflect.Value { return value.Addr() }
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	address := ssaPkg.Func("Address")
	var call *ssa.Call
	for _, block := range address.Blocks {
		for _, instruction := range block.Instrs {
			candidate, ok := instruction.(*ssa.Call)
			if ok && candidate.Common().StaticCallee() != nil &&
				candidate.Common().StaticCallee().Name() == "Addr" {
				call = candidate
			}
		}
	}
	if call == nil {
		t.Fatal("Address has no exact reflect.Value.Addr call")
	}

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("foo", "foo")
	ctx := &context{
		pkg: pkg,
		coroEmission: &coroPhysicalEmissionSession{
			phase: coroPhysicalEmissionBody,
			body:  &coroBodyContext{},
		},
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("unplanned physical call unexpectedly completed")
			}
		}()
		ctx.tryCompileCoroPhysicalCall(nil, call)
	}()
	if pkg.NeedAbiInit&llssa.ReflectPointerTo == 0 {
		t.Fatalf("physical Addr call ABI-init demand = %d, want ReflectPointerTo", pkg.NeedAbiInit)
	}
}
