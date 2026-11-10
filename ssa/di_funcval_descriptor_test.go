package ssa

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"go/token"
	"go/types"
	"testing"

	"github.com/xgo-dev/llgo/internal/wasmdebug"
	"github.com/xgo-dev/llvm"
)

func TestDebugFuncvalDescriptorIdentity(t *testing.T) {
	Initialize(InitAllTargets | InitAllTargetInfos | InitAllTargetMCs | InitAllAsmPrinters)
	for _, target := range []Target{
		{GOOS: "linux", GOARCH: "386"},
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "wasip1", GOARCH: "wasm", WasmProfile: "w32", WasmProvider: "wasi"},
	} {
		t.Run(target.GOOS+"/"+target.GOARCH, func(t *testing.T) {
			prog := NewProgram(&target)
			defer prog.Dispose()
			prog.TypeSizes(types.SizesFor("gc", target.GOARCH))
			if target.GOARCH == "wasm" {
				prog.TypeSizes(&types.StdSizes{WordSize: 8, MaxAlign: 8})
			}
			prog.SetRuntime(newDebugRuntimePackage())
			pkg := prog.NewPackage("p", "example.com/descriptor")
			pkg.InitDebug("p", pkg.Path(), token.NewFileSet())
			fn := pkg.NewFunc(pkg.Path()+".owner", NoArgsNoRet, CDecl)
			builder := fn.MakeBody(1)
			defer builder.Dispose()
			record := types.NewStruct([]*types.Var{
				types.NewField(token.NoPos, nil, "code", NoArgsNoRet, false),
				types.NewField(token.NoPos, nil, "env", types.Typ[types.UnsafePointer], false),
			}, nil)
			for _, test := range []struct {
				name string
				typ  types.Type
				cc   CallConv
			}{
				{"Go", NoArgsNoRet, GoDecl},
				{"Native", NoArgsNoRet, CDecl},
				{"Record", record, CDecl},
			} {
				global := pkg.NewVar(pkg.Path()+"."+test.name, types.NewPointer(test.typ), test.cc)
				global.InitNil()
				builder.DIGlobal(global.Expr, test.name, token.Position{Filename: "descriptor.go", Line: 1})
			}
			builder.Return()
			builder.EndBuild()
			pkg.FinalizeDebug()
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), llvm.ObjectFile)
			if err != nil {
				t.Fatal(err)
			}
			defer object.Dispose()
			var data *dwarf.Data
			if target.GOARCH == "wasm" {
				sections, err := wasmdebug.DWARFSections(object.Bytes())
				if err != nil {
					t.Fatal(err)
				}
				data, err = dwarf.New(sections[".debug_abbrev"], nil, nil, sections[".debug_info"],
					sections[".debug_line"], nil, sections[".debug_ranges"], sections[".debug_str"])
				if err != nil {
					t.Fatal(err)
				}
			} else {
				file, err := elf.NewFile(bytes.NewReader(object.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				data, err = file.DWARF()
				if err != nil {
					t.Fatal(err)
				}
			}
			seen := make(map[string]bool)
			reader := data.Reader()
			for {
				entry, err := reader.Next()
				if err != nil {
					t.Fatal(err)
				}
				if entry == nil {
					break
				}
				name, _ := entry.Val(dwarf.AttrName).(string)
				if entry.Tag != dwarf.TagVariable || (name != "Go" && name != "Native" && name != "Record") {
					continue
				}
				typ, err := data.Type(entry.Val(dwarf.AttrType).(dwarf.Offset))
				if err != nil {
					t.Fatal(err)
				}
				seen[name] = true
				pointer, _ := typ.(*dwarf.PtrType)
				var marker *dwarf.TypedefType
				if pointer != nil {
					marker, _ = pointer.Type.(*dwarf.TypedefType)
				}
				if name != "Go" {
					if marker != nil && marker.Name == "$llgo.funcval" {
						t.Fatalf("%s acquired the Go descriptor identity", name)
					}
					continue
				}
				if marker == nil || marker.Name != "$llgo.funcval" {
					t.Fatalf("Go function lacks its compiler-only descriptor identity: %s", typ)
				}
				header, ok := marker.Type.(*dwarf.StructType)
				word := int64(prog.GoWordSize())
				if !ok {
					t.Fatalf("descriptor lacks its actual header: %s", marker.Type)
				}
				// The object reader reports the physical address width. Memory32
				// keeps separate eight-byte Go storage and header field offsets.
				if pointer.Size() != int64(prog.PointerSize()) || int64(prog.SizeOf(prog.Closure(NoArgsNoRet))) != word ||
					len(header.Field) != 2 || header.Field[0].Name != "code" || header.Field[0].ByteOffset != 0 ||
					header.Field[1].Name != "env" || header.Field[1].ByteOffset != word || header.Size() != 2*word {
					t.Fatalf("descriptor storage/header changed: pointer=%s bytes=%d physical=%d GoWord=%d header=%s", pointer, pointer.Size(), prog.PointerSize(), word, marker.Type)
				}
			}
			if len(seen) != 3 {
				t.Fatalf("incomplete actual object variable inventory: %v", seen)
			}
		})
	}
}
