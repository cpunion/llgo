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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/crosscompile"
	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
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

func TestCoroRawLLGoFilesInfersClosedExecutorLeaves(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("runtime C executor-leaf fixtures require Darwin or Linux")
	}
	compiler, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	ctx := &context{
		buildConf:    &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH},
		crossCompile: crosscompile.Export{CC: compiler},
	}
	sources := []string{
		"../../runtime/internal/lib/runtime/_wrap/poll.c",
		"../../runtime/internal/lib/runtime/_wrap/signal.c",
		"../../runtime/internal/lib/runtime/_wrap/debugtrap.c",
		"../../runtime/internal/clite/debug/_wrap/debug.c",
		"../../runtime/internal/clite/os/_os/os.c",
		"../../runtime/internal/coroworker/_worker/worker.c",
	}
	positive := []string{
		"__llgo_runtime_poll_desc_state_v1",
		"__llgo_runtime_poll_desc_deadline_v1",
		"__llgo_runtime_poll_desc_set_deadline_v1",
		"__llgo_runtime_poll_desc_mark_closing_v1",
		"__llgo_runtime_poll_desc_publish_operation_v1",
		"__llgo_runtime_poll_desc_clear_operation_v1",
		"__llgo_runtime_poll_desc_load_operation_v1",
		"__llgo_runtime_signal_generation_v1",
		"__llgo_runtime_signal_idle_v1",
		"__llgo_coro_worker_queue_can_release_v1",
		"cliteClearenv",
		"llgo_address",
		"llgo_debugtrap",
	}
	negative := []string{
		"__llgo_runtime_poll_fd_stream_v1",
		"__llgo_runtime_poll_read_attempt_v1",
		"__llgo_runtime_signal_receive_v1",
		"cliteErrno",
		"llgo_addrinfo",
		"llgo_stacktrace",
		"llgo_symbol",
	}
	for _, optimization := range []string{"-O0", "-O2", "-Oz"} {
		t.Run(optimization, func(t *testing.T) {
			inventory := &coroRawGlobalSymbolInventory{
				foreignExecutorLeafProofs: make(map[string]cl.CoroForeignExecutorLeafProof),
			}
			for _, relative := range sources {
				source, err := filepath.Abs(relative)
				if err != nil {
					t.Fatal(err)
				}
				inferCoroRawLLGoFileExecutorLeaves(
					ctx,
					inventory,
					"runtime-c-fixture",
					[]string{"-x", "c", optimization},
					source,
				)
			}
			for _, symbol := range positive {
				proof, inferred := inventory.foreignExecutorLeafProofs[symbol]
				if !inferred || proof.PhysicalSymbol != symbol ||
					proof.LLVMABISignature == "" ||
					proof.LLVMTargetTriple == "" ||
					proof.LLVMDataLayout == "" ||
					len(proof.CallClosure) == 0 ||
					len(proof.ClosureSHA256) != 64 {
					t.Errorf(
						"executor-leaf proof for %q = %+v, %t",
						symbol, proof, inferred,
					)
				}
			}
			for _, symbol := range negative {
				if proof, inferred := inventory.foreignExecutorLeafProofs[symbol]; inferred {
					t.Errorf(
						"externally calling or cyclic function %q was inferred: %+v",
						symbol, proof,
					)
				}
			}
		})
	}
}

func TestCoroRawLLGoFilesInfersWasmDebugTrap(t *testing.T) {
	compiler, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	ctx := &context{
		buildConf: &Config{Goos: "wasip1", Goarch: "wasm"},
		crossCompile: crosscompile.Export{
			CC:      compiler,
			CCFLAGS: []string{"-target", "wasm32-unknown-unknown"},
		},
	}
	inventory := &coroRawGlobalSymbolInventory{
		foreignExecutorLeafProofs: make(map[string]cl.CoroForeignExecutorLeafProof),
	}
	source, err := filepath.Abs(
		"../../runtime/internal/lib/runtime/_wrap/debugtrap.c",
	)
	if err != nil {
		t.Fatal(err)
	}
	inferCoroRawLLGoFileExecutorLeaves(
		ctx,
		inventory,
		"runtime-wasm-c-fixture",
		[]string{"-x", "c"},
		source,
	)
	proof, inferred := inventory.foreignExecutorLeafProofs["llgo_debugtrap"]
	if !inferred ||
		proof.LLVMTargetTriple != "wasm32-unknown-unknown" ||
		proof.LLVMABISignature != "void ()" ||
		len(proof.CallClosure) != 2 ||
		proof.CallClosure[0] != "llgo_debugtrap" ||
		proof.CallClosure[1] != "llvm.debugtrap" {
		t.Fatalf("WASM debugtrap executor-leaf proof = %+v, %t", proof, inferred)
	}

	prog := llssa.NewProgram(&llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	if proof.LLVMDataLayout != prog.DataLayout() {
		t.Fatalf(
			"WASM debugtrap proof data layout = %q, frontend = %q",
			proof.LLVMDataLayout, prog.DataLayout(),
		)
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
