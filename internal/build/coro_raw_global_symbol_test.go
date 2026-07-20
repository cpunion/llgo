/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package build

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/crosscompile"
	"github.com/goplus/llgo/internal/packages"
	"github.com/xgo-dev/llvm"
)

func TestCoroRawGlobalSymbolInventoryRequiresCompleteAbsence(t *testing.T) {
	complete := newCompleteCoroRawGlobalSymbolInventory("fixture")
	if proved, reason := complete.proveNoDefinitionOrReference("fixture", "syscall.copyenv"); !proved || reason != "" {
		t.Fatalf("complete empty inventory proof = %t, %q; want true", proved, reason)
	}
	if proved, reason := complete.proveNoDefinitionOrReference("fixture", ""); proved || !strings.Contains(reason, "non-empty") {
		t.Fatalf("empty physical symbol proof = %t, %q; want fail closed", proved, reason)
	}

	builder := newCoroRawGlobalSymbolInventoryBuilder()
	builder.mention("syscall.copyenv", "Plan9 global reference", "z.s")
	mentioned := builder.freeze()
	// A frozen inventory must not observe later builder mutations.
	builder.mention("other.symbol", "Plan9 global definition", "later.s")
	if proved, reason := mentioned.proveNoDefinitionOrReference("syscall.copyenv"); proved ||
		!strings.Contains(reason, "Plan9 global reference z.s") {
		t.Fatalf("mentioned physical symbol proof = %t, %q; want exact rejection", proved, reason)
	}
	if proved, reason := mentioned.proveNoDefinitionOrReference("other.symbol"); !proved || reason != "" {
		t.Fatalf("post-freeze mutation leaked into inventory: %t, %q", proved, reason)
	}

	incompleteBuilder := newCoroRawGlobalSymbolInventoryBuilder()
	incompleteBuilder.block("opaque syso input", "z.syso")
	incompleteBuilder.block("opaque C input", "a.c")
	incomplete := incompleteBuilder.freeze()
	if proved, reason := incomplete.proveNoDefinitionOrReference("syscall.copyenv"); proved ||
		reason != "raw data-symbol inventory is incomplete: opaque C input: a.c; opaque syso input: z.syso" {
		t.Fatalf("incomplete proof = %t, %q; want deterministic fail-closed reason", proved, reason)
	}
	if proved, reason := (*coroRawGlobalSymbolInventory)(nil).proveNoDefinitionOrReference("fixture", "syscall.copyenv"); proved ||
		!strings.Contains(reason, "not frozen") {
		t.Fatalf("nil inventory proof = %t, %q; want fail closed", proved, reason)
	}
}

func TestCoroRawGlobalSymbolInventoryDarwinDynimports(t *testing.T) {
	file := parseCoroRawInventoryFile(t, `package syscall
//go:cgo_import_dynamic libc_read read "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_write write "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic same same "/usr/lib/libSystem.B.dylib"
`)
	builder := newCoroRawGlobalSymbolInventoryBuilder()
	ctx := &context{buildConf: &Config{Goos: "darwin", Goarch: "arm64"}}
	if err := inventoryCoroDarwinDynimports(ctx, builder, "syscall", file); err != nil {
		t.Fatalf("inventory Darwin dynimports: %v", err)
	}
	inventory := builder.freeze()
	for _, symbol := range []string{
		"libc_read", "read", "libc_read_trampoline", "libc_read_trampoline_addr",
		"libc_write", "write", "libc_write_trampoline", "libc_write_trampoline_addr",
	} {
		if proved, _ := inventory.proveNoDefinitionOrReference(symbol); proved {
			t.Errorf("dynimport symbol %q was not inventoried", symbol)
		}
	}
	for _, symbol := range []string{"same", "syscall.copyenv"} {
		if proved, reason := inventory.proveNoDefinitionOrReference(symbol); !proved || reason != "" {
			t.Errorf("unemitted symbol %q proof = %t, %q; want true", symbol, proved, reason)
		}
	}
}

func TestCoroRawGlobalSymbolInventoryDarwinDynimportConflictDeterministic(t *testing.T) {
	file := parseCoroRawInventoryFile(t, `package p
//go:cgo_import_dynamic local zed
//go:cgo_import_dynamic local alpha
`)
	ctx := &context{buildConf: &Config{Goos: "darwin", Goarch: "amd64"}}
	err := inventoryCoroDarwinDynimports(ctx, newCoroRawGlobalSymbolInventoryBuilder(), "example.com/p", file)
	if err == nil || err.Error() != `example.com/p: conflicting go:cgo_import_dynamic for "local": alpha, zed` {
		t.Fatalf("conflict error = %v; want sorted aliases", err)
	}
}

func TestCoroRawGlobalSymbolInventoryLLVMModuleSymbols(t *testing.T) {
	llvm.InitializeAllTargets()
	llvmContext := llvm.NewContext()
	defer llvmContext.Dispose()
	module := llvmContext.NewModule("raw-symbol-inventory")
	defer module.Dispose()
	i32 := llvmContext.Int32Type()
	defined := llvm.AddGlobal(module, i32, "example.com/p.defined")
	defined.SetInitializer(llvm.ConstInt(i32, 1, false))
	llvm.AddGlobal(module, i32, "example.com/p.referenced")
	llvm.AddFunction(module, "example.com/p.called", llvm.FunctionType(llvmContext.VoidType(), nil, false))

	builder := newCoroRawGlobalSymbolInventoryBuilder()
	inventoryCoroLLVMModuleSymbols(builder, module, "asm.s")
	inventory := builder.freeze()
	for _, symbol := range []string{"example.com/p.defined", "example.com/p.referenced", "example.com/p.called"} {
		if proved, _ := inventory.proveNoDefinitionOrReference(symbol); proved {
			t.Errorf("LLVM module symbol %q was not inventoried", symbol)
		}
	}
	if proved, reason := inventory.proveNoDefinitionOrReference("syscall.copyenv"); !proved || reason != "" {
		t.Fatalf("absent LLVM module symbol proof = %t, %q; want true", proved, reason)
	}
}

func TestCoroRawGlobalSymbolInventoryOpaqueInputsFailClosed(t *testing.T) {
	typesPackage := types.NewPackage("example.com/raw", "raw")
	typesPackage.Scope().Insert(types.NewConst(token.NoPos, typesPackage, "LLGoFiles", types.Typ[types.UntypedString], constant.MakeString("leaf.c")))
	pkg := &packages.Package{
		ID:         "example.com/raw",
		PkgPath:    "example.com/raw",
		Name:       "raw",
		Types:      typesPackage,
		OtherFiles: []string{"opaque.syso"},
	}
	ctx := &context{buildConf: &Config{Goos: "darwin", Goarch: "arm64"}}
	inventory, err := freezeCoroRawGlobalSymbolInventory(ctx, []*aPackage{{Package: pkg}})
	if err != nil {
		t.Fatalf("freeze opaque inputs: %v", err)
	}
	proved, reason := inventory.proveNoDefinitionOrReference("example.com/raw", "syscall.copyenv")
	if proved || !strings.Contains(reason, "opaque LLGoFiles input") || !strings.Contains(reason, "opaque syso input") {
		t.Fatalf("opaque input proof = %t, %q; want both fail-closed blockers", proved, reason)
	}
}

func TestCoroRawGlobalSymbolInventoryIsolatesPackageProfiles(t *testing.T) {
	cleanTypes := types.NewPackage("example.com/clean", "clean")
	rawTypes := types.NewPackage("example.com/raw", "raw")
	rawTypes.Scope().Insert(types.NewConst(token.NoPos, rawTypes, "LLGoFiles", types.Typ[types.UntypedString], constant.MakeString("leaf.c")))
	clean := &aPackage{Package: &packages.Package{ID: "clean-id", PkgPath: cleanTypes.Path(), Types: cleanTypes}}
	raw := &aPackage{Package: &packages.Package{ID: "raw-id", PkgPath: rawTypes.Path(), Types: rawTypes}}
	ctx := &context{
		buildConf:    &Config{Goos: "darwin", Goarch: "arm64"},
		crossCompile: crosscompile.Export{ExtraFiles: []string{"unowned-extra.c"}},
	}
	inventory, err := freezeCoroRawGlobalSymbolInventory(ctx, []*aPackage{raw, clean})
	if err != nil {
		t.Fatal(err)
	}
	if proved, reason := inventory.proveNoDefinitionOrReference("clean-id", "example.com/clean.slot"); !proved || reason != "" {
		t.Fatalf("clean package profile proof = %t, %q; unrelated raw/extra input must not block", proved, reason)
	}
	if proved, reason := inventory.proveNoDefinitionOrReference("raw-id", "example.com/raw.slot"); proved || !strings.Contains(reason, "LLGoFiles") {
		t.Fatalf("raw package profile proof = %t, %q; want same-package blocker", proved, reason)
	}
}

func TestCoroRawGlobalSymbolInventoryPlan9AltSelection(t *testing.T) {
	replacement := &aPackage{
		Package: &packages.Package{PkgPath: "syscall"},
		AltPkg:  &packages.Cached{Package: &packages.Package{PkgPath: "syscall"}},
	}
	if coroRawIncludesOriginalPlan9(replacement) {
		t.Fatal("replacement Alt unexpectedly retained original Plan9 SFiles")
	}
	additive := &aPackage{
		Package: &packages.Package{PkgPath: "internal/runtime/sys"},
		AltPkg:  &packages.Cached{Package: &packages.Package{PkgPath: "internal/runtime/sys"}},
	}
	if !coroRawIncludesOriginalPlan9(additive) {
		t.Fatal("additive Alt unexpectedly omitted original Plan9 SFiles")
	}
	if !coroRawIncludesOriginalPlan9(&aPackage{Package: &packages.Package{PkgPath: "syscall"}}) {
		t.Fatal("package without Alt unexpectedly omitted original Plan9 SFiles")
	}
}

func parseCoroRawInventoryFile(t *testing.T, source string) []*ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "inventory.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return []*ast.File{file}
}
