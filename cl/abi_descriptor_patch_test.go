//go:build !llgo

package cl

import (
	"go/token"
	"go/types"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
)

func TestPhysicalDescriptorPatchPreservesSourceAndRawSignatures(t *testing.T) {
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	oldPkg := types.NewPackage("example.com/descriptorpatch", "descriptorpatch")
	newPkg := types.NewPackage(oldPkg.Path(), oldPkg.Name())
	callback := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	named := func(pkg *types.Package) *types.Named {
		obj := types.NewTypeName(0, pkg, "Box", nil)
		typ := types.NewNamed(obj, types.NewStruct([]*types.Var{
			types.NewField(0, pkg, "Callback", callback, false),
		}, nil), nil)
		pkg.Scope().Insert(obj)
		typ.AddMethod(types.NewFunc(0, pkg, "Method", types.NewSignatureType(types.NewVar(0, pkg, "self", typ), nil, nil, nil, nil, false)))
		return typ
	}
	original, replacement := named(oldPkg), named(newPkg)
	generic := func(pkg *types.Package) types.Type {
		parameter := types.NewTypeParam(types.NewTypeName(0, pkg, "T", nil), types.NewInterfaceType(nil, nil).Complete())
		fn := types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewParam(0, nil, "value", parameter)), nil, false)
		obj := types.NewTypeName(0, pkg, "Generic", nil)
		typ := types.NewNamed(obj, types.NewStruct([]*types.Var{types.NewField(0, pkg, "Callback", fn, false)}, nil), nil)
		typ.SetTypeParams([]*types.TypeParam{parameter})
		pkg.Scope().Insert(obj)
		instance, err := types.Instantiate(nil, typ, []types.Type{types.Typ[types.Int]}, true)
		if err != nil {
			t.Fatal(err)
		}
		return instance
	}
	originalGeneric := generic(oldPkg)
	generic(newPkg)
	ctx := &context{prog: prog, patches: Patches{oldPkg.Path(): {Types: newPkg}}}
	prog.SetPatch(ctx.patchPhysicalType)
	physical := prog.PhysicalType(replacement, llssa.InGo)
	if types.Identical(physical, replacement) {
		t.Fatal("fixture did not create a distinct physical callback field")
	}
	for _, root := range []types.Type{
		original,
		originalGeneric,
		types.NewPointer(original),
		types.NewStruct([]*types.Var{types.NewField(0, nil, "Box", original, false)}, nil),
		types.NewStruct([]*types.Var{types.NewField(0, nil, "Box", original, true)}, nil),
	} {
		t.Run(root.String(), func(t *testing.T) {
			wantSource := ctx.patchType(root)
			wantPhysical := prog.PhysicalType(wantSource, llssa.InGo)
			rawRoot := prog.PhysicalType(root, llssa.InGo)
			if got := ctx.patchPhysicalType(rawRoot); !types.Identical(got, wantPhysical) {
				t.Fatalf("descriptor remained source-shaped: %v; want physical %v", got, wantPhysical)
			}
			if got := ctx.patchType(root); !types.Identical(got, wantSource) {
				t.Fatal("descriptor projection polluted source identity")
			}
			var planned types.Type
			if err := walkEmissionABITypeDemandEx(rawRoot, ctx.patchPhysicalType, prog.SourceType, nil, nil, func(typ types.Type) error {
				if planned == nil {
					planned = typ
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// The ordinary codegen raw-field consumer and the planner must
			// observe the same physical replacement, not merely equal names.
			container := types.NewStruct([]*types.Var{types.NewField(0, nil, "Value", root, false)}, nil)
			emitted := prog.Field(prog.Type(container, llssa.InGo), 0).RawType()
			if !types.Identical(planned, emitted) || !types.Identical(emitted, wantPhysical) {
				t.Fatalf("planner/codegen physical identities diverged: %v / %v", planned, emitted)
			}
			if source := prog.SourceType(planned); !types.Identical(source, wantSource) {
				t.Fatalf("patched descriptor lost its exact source method owner: %v; want %v", source, wantSource)
			}
			if again := ctx.patchPhysicalType(planned); again != planned {
				t.Fatal("an already patched physical descriptor was allocated again")
			}
		})
	}
	// A raw C signature may contain an unnamed callback pointer beside a
	// patched named aggregate. Only the aggregate has a source replacement;
	// converting the entire signature as Go would incorrectly box the callback.
	raw := types.NewSignatureType(nil, nil, nil, types.NewTuple(
		types.NewParam(token.NoPos, nil, "callback", callback),
		types.NewParam(token.NoPos, nil, "box", original),
	), nil, false)
	got, ok := ctx.patchPhysicalType(raw).(*types.Signature)
	if !ok || got.Params().At(0).Type() != callback || got.Params().At(1).Type() != physical {
		t.Fatalf("raw signature changed callback representation or retained source aggregate: %v", got)
	}
	if ctx.patchPhysicalType(callback) != callback {
		t.Fatal("an unchanged raw/public signature was boxed into a closure")
	}
	goDeclaration := prog.PhysicalFuncDecl(raw, llssa.InGo)
	goPatched, ok := ctx.patchPhysicalType(goDeclaration).(*types.Signature)
	wantGo := prog.PhysicalFuncDecl(ctx.patchType(raw).(*types.Signature), llssa.InGo)
	if !ok || !types.Identical(goPatched, wantGo) {
		t.Fatalf("proven Go public signature was not restored as a declaration: %v", goPatched)
	}
	if _, closure := goPatched.Params().At(0).Type().(*types.Struct); !closure {
		t.Fatal("proven Go callback lost its physical closure representation")
	}
	unrelated := types.NewNamed(types.NewTypeName(0, types.NewPackage("example.com/unrelated", "unrelated"), "Box", nil), replacement.Underlying(), nil)
	if ctx.patchPhysicalType(unrelated) != unrelated {
		t.Fatal("unrelated same-name/layout type was treated as a source replacement")
	}
}
