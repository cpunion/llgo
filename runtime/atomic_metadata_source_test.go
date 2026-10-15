//go:build !llgo

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

package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

const (
	itabCacheSource        = "internal/runtime/z_face.go"
	weakCacheSource        = "internal/lib/runtime/weak_llgo.go"
	atomicCacheCore        = "internal/atomiccache/cache.go"
	atomicCacheLLGo        = "internal/atomiccache/atomic_llgo.go"
	atomicCacheESP32C3LLGo = "internal/atomiccache/atomic_esp32c3_llgo.go"
)

func readAtomicMetadataSource(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func parseAtomicMetadataSource(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, readAtomicMetadataSource(t, path), parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestItabCacheUsesCanonicalLockFreePublication(t *testing.T) {
	source := readAtomicMetadataSource(t, itabCacheSource)
	for _, required := range []string{
		"var itabTable atomiccache.PairTable",
		"itabTable.Find(unsafe.Pointer(inter), unsafe.Pointer(typ))",
		"itabTable.Intern(unsafe.Pointer(i.inter), unsafe.Pointer(i._type), unsafe.Pointer(i))",
		"return addItab(ret)",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("%s lacks lock-free canonicalization marker %q", itabCacheSource, required)
		}
	}
	for _, forbidden := range []string{"pthread", ".Lock()", ".Unlock()", "append(itabTable"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s retains forbidden locked-cache marker %q", itabCacheSource, forbidden)
		}
	}

	core := readAtomicMetadataSource(t, atomicCacheCore)
	findBeforeCAS := strings.Index(core, "if winner := findPair(head, first, second); winner != nil")
	publication := strings.Index(core, "compareAndSwapPointer(&table.head")
	if findBeforeCAS < 0 || publication < 0 || findBeforeCAS >= publication {
		t.Fatalf("PairTable.Intern no longer performs full snapshot recheck before CAS publication")
	}
	for _, required := range []string{
		"candidate := &pairEntry{first: first, second: second, value: value}",
		"for {",
		"head := table.load()",
		"candidate.next = head",
	} {
		if !strings.Contains(core, required) {
			t.Errorf("%s lacks pair-cache publication marker %q", atomicCacheCore, required)
		}
	}
	if strings.Contains(core, `"sync/atomic"`) {
		t.Fatalf("%s imports the standard-library atomic package and creates an unnecessary LLGo init edge", atomicCacheCore)
	}
	llgoAtomic := readAtomicMetadataSource(t, atomicCacheLLGo)
	for _, required := range []string{
		"//go:build llgo && !esp32c3",
		`catomic "github.com/xgo-dev/llgo/runtime/internal/sync/atomic"`,
		"catomic.Load(address)",
		"catomic.CompareAndExchange(address, old, new)",
	} {
		if !strings.Contains(llgoAtomic, required) {
			t.Errorf("%s lacks LLGo intrinsic-backed atomic marker %q", atomicCacheLLGo, required)
		}
	}

	esp32c3Atomic := readAtomicMetadataSource(t, atomicCacheESP32C3LLGo)
	for _, required := range []string{
		"//go:build llgo && esp32c3",
		"return *address",
		"if *address != old",
		"*address = new",
	} {
		if !strings.Contains(esp32c3Atomic, required) {
			t.Errorf("%s lacks serialized managed-cache marker %q", atomicCacheESP32C3LLGo, required)
		}
	}
	for _, forbidden := range []string{"catomic", `"sync/atomic"`, "__atomic_"} {
		if strings.Contains(esp32c3Atomic, forbidden) {
			t.Errorf("%s retains unsupported ESP32-C3 atomic marker %q", atomicCacheESP32C3LLGo, forbidden)
		}
	}
}

func TestWeakCollectorOwnsLivenessBeforeFinalization(t *testing.T) {
	source := readAtomicMetadataSource(t, weakCacheSource)
	for _, forbidden := range []string{"pthread", "make(map", "delete(", ".Lock()", ".Unlock()", "addCleanupPtr", "StoreUint32"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s retains forbidden weak-liveness dependency %q", weakCacheSource, forbidden)
		}
	}
	file := parseAtomicMetadataSource(t, weakCacheSource)
	ast.Inspect(file, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.ForStmt, *ast.RangeStmt, *ast.GoStmt, *ast.DeferStmt, *ast.FuncLit:
			t.Error("weak boundary contains a loop, spawn, defer, or callback closure")
		}
		return true
	})
	for _, required := range []string{"llgoWeakRegister(p, &h.Live)", "llgoWeakStrong(h.Key, &h.Live)"} {
		if !strings.Contains(source, required) {
			t.Errorf("weak boundary does not consume collector operation %q", required)
		}
	}
	native := readAtomicMetadataSource(t, "internal/clite/bdwgc/_wrap/coro_allocator.c")
	for _, required := range []string{
		"GC_general_register_disappearing_link((void **)live, base)", "GC_reachable_here(object)",
		"GC_call_with_alloc_lock(llgo_weak_reveal_locked, &lookup)", "if (*lookup->live == 0)", "return (void *)~lookup->key;",
	} {
		if !strings.Contains(native, required) {
			t.Errorf("native weak collector boundary lacks %q", required)
		}
	}
	tiny := readAtomicMetadataSource(t, "internal/runtime/tinygogc/gc_tinygo.go")
	mark := strings.Index(tiny, "\n\tfinishMark()")
	clear := strings.Index(tiny, "\n\tclearUnmarkedWeakReferences()")
	resume := strings.Index(tiny, "\n\tgcResumeWorld()")
	sweep := strings.Index(tiny, "\n\tfreeBytes = sweep()")
	if mark < 0 || clear <= mark || resume <= clear || sweep <= resume {
		t.Fatal("tinygc must clear weak references after marking and before resuming/sweeping")
	}
	tinyWeak := readAtomicMetadataSource(t, "internal/runtime/tinygogc/weak.go")
	for _, required := range []string{"hiddenBase uintptr", "record.hiddenBase = hideWeakBase(base)", "return ^uintptr(base)", "if live == nil || *live == 0", "*record.live = 0", "record.live = nil", "record.next = nil"} {
		if !strings.Contains(tinyWeak, required) {
			t.Errorf("tinygc weak record lifecycle lacks %q", required)
		}
	}
}

func TestWeakAddressConversionsAreNonSuspendingLeaves(t *testing.T) {
	file := parseAtomicMetadataSource(t, weakCacheSource)
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			functions[function.Name.Name] = function
		}
	}
	register := functions["llgoRegisterWeakPointer"]
	hide := functions["llgoWeakPointerKey"]
	makeStrong := functions["llgoMakeStrongFromWeak"]
	reconstruct := functions["llgoWeakKeyPointer"]
	if register == nil || hide == nil || makeStrong == nil || reconstruct == nil {
		t.Fatal("weak runtime lacks separated pointer/key conversion leaves")
	}
	usedHideLeaf := false
	directHideConversions := 0
	ast.Inspect(register.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := call.Fun.(*ast.Ident); ok {
			if name.Name == "llgoWeakPointerKey" {
				usedHideLeaf = true
			}
			if name.Name == "uintptr" {
				directHideConversions++
			}
		}
		return true
	})
	if !usedHideLeaf || directHideConversions != 0 {
		t.Fatal("weak registration must hide its pointer only through llgoWeakPointerKey")
	}
	if hide.Body == nil || len(hide.Body.List) != 1 {
		t.Fatal("weak pointer hiding leaf must contain exactly one return")
	}
	hideReturn, ok := hide.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(hideReturn.Results) != 1 {
		t.Fatal("weak pointer hiding leaf is not one scalar return")
	}
	hidden, ok := hideReturn.Results[0].(*ast.UnaryExpr)
	if !ok || hidden.Op != token.XOR {
		t.Fatal("weak pointer key must be complemented to evade conservative scanning")
	}
	hideConversion, ok := hidden.X.(*ast.CallExpr)
	if !ok || len(hideConversion.Args) != 1 {
		t.Fatal("weak pointer hiding leaf is not one uintptr conversion")
	}
	hideTarget, targetOK := hideConversion.Fun.(*ast.Ident)
	hidePointer, pointerOK := hideConversion.Args[0].(*ast.Ident)
	if !targetOK || hideTarget.Name != "uintptr" || !pointerOK || hidePointer.Name != "p" {
		t.Fatal("weak pointer hiding leaf changed its exact pointer-to-uintptr recipe")
	}
	usedLeaf := false
	directReconstruction := false
	ast.Inspect(makeStrong.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		if ok && name.Name == "llgoWeakStrong" {
			usedLeaf = true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			packageName, packageOK := selector.X.(*ast.Ident)
			if packageOK && packageName.Name == "unsafe" && selector.Sel.Name == "Pointer" {
				directReconstruction = true
			}
		}
		return true
	})
	if !usedLeaf || directReconstruction {
		t.Fatal("weak make-strong path must promote only through the collector's read/reveal boundary")
	}
	if reconstruct.Body == nil || len(reconstruct.Body.List) != 1 {
		t.Fatal("weak key reconstruction leaf must contain exactly one return")
	}
	statement, ok := reconstruct.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		t.Fatal("weak key reconstruction leaf is not one scalar return")
	}
	conversion, ok := statement.Results[0].(*ast.CallExpr)
	if !ok || len(conversion.Args) != 1 {
		t.Fatal("weak key reconstruction leaf is not one unsafe.Pointer conversion")
	}
	target, ok := conversion.Fun.(*ast.SelectorExpr)
	packageName, packageOK := target.X.(*ast.Ident)
	revealed, revealOK := conversion.Args[0].(*ast.UnaryExpr)
	if !revealOK || revealed.Op != token.XOR {
		t.Fatal("weak key reconstruction must undo the complemented address")
	}
	key, keyOK := revealed.X.(*ast.Ident)
	if !ok || !packageOK || packageName.Name != "unsafe" || target.Sel.Name != "Pointer" ||
		!keyOK || key.Name != "key" {
		t.Fatal("weak key reconstruction leaf changed its exact uintptr-to-pointer recipe")
	}
}

func TestAtomicMetadataRetryLoopsRemainLockFreeAndPreemptible(t *testing.T) {
	source := readAtomicMetadataSource(t, atomicCacheCore)
	for _, forbidden := range []string{"sync.Mutex", "pthread", "coroPark", "coroYield", "runtime.Gosched"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s retry loop contains forbidden ownership/scheduler marker %q", atomicCacheCore, forbidden)
		}
	}
	for _, required := range []string{
		"for entry := head; entry != nil; entry = entry.next",
		"func prune(bucket *unsafe.Pointer)",
		"func (table *WeakTable) PruneWeak",
		"func (table *WeakTable) InternWeak",
		"compareAndSwapPointer(link, raw, next)",
		"compareAndSwapPointer(bucket, head, unsafe.Pointer(candidate))",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("%s lacks preemptible lock-free retry marker %q", atomicCacheCore, required)
		}
	}
}
