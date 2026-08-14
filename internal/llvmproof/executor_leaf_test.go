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

package llvmproof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestProveExecutorLeafAcceptsClosedAcyclicAtomicClosure(t *testing.T) {
	module := parseExecutorLeafModule(t, `
target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"
target triple = "x86_64-unknown-linux-gnu"
define internal i32 @helper(ptr %cell) {
entry:
  %old = atomicrmw add ptr %cell, i32 1 seq_cst, align 4
  ret i32 %old
}
define i32 @leaf(ptr %cell) {
entry:
  %value = call i32 @helper(ptr %cell)
  %ok = icmp ne i32 %value, 0
  br i1 %ok, label %yes, label %no
yes:
  ret i32 1
no:
  ret i32 0
}`)
	proof, err := ProveExecutorLeaf(module, "leaf")
	if err != nil {
		t.Fatal(err)
	}
	if proof.Symbol != "leaf" || proof.Signature != "i32 (ptr)" ||
		proof.TargetTriple != "x86_64-unknown-linux-gnu" ||
		proof.DataLayout == "" ||
		len(proof.CallClosure) != 2 ||
		proof.CallClosure[0] != "helper" || proof.CallClosure[1] != "leaf" ||
		len(proof.ClosureSHA256) != 64 {
		t.Fatalf("executor-leaf proof = %+v", proof)
	}
}

func TestProveExecutorLeafAcceptsExactTerminalIntrinsics(t *testing.T) {
	module := parseExecutorLeafModule(t, `
declare void @llvm.debugtrap()
declare void @llvm.trap()
define void @leaf(i1 %fatal) {
entry:
  br i1 %fatal, label %trap, label %debug
debug:
  call void @llvm.debugtrap()
  ret void
trap:
  call void @llvm.trap()
  unreachable
}`)
	proof, err := ProveExecutorLeaf(module, "leaf")
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"leaf", "llvm.debugtrap", "llvm.trap"} {
		if !containsExecutorLeafSymbol(proof.CallClosure, symbol) {
			t.Fatalf("terminal executor-leaf closure %v omits %q", proof.CallClosure, symbol)
		}
	}
}

func TestProveExecutorLeafForDataLayoutIsClosureLocal(t *testing.T) {
	const (
		clangWasm = "e-m:e-p:32:32-p10:8:8-p20:8:8-i64:64-n32:64-S128-ni:1:10:20"
		llgoWasm  = "e-m:e-p:32:32-p10:8:8-p20:8:8-i64:64-i128:128-n32:64-S128-ni:1:10:20"
	)
	terminal := parseExecutorLeafModule(t, `
target datalayout = "`+clangWasm+`"
target triple = "wasm32-unknown-unknown"
declare void @llvm.debugtrap()
define void @leaf() {
entry:
  call void @llvm.debugtrap()
  ret void
}`)
	proof, err := ProveExecutorLeafForDataLayout(terminal, "leaf", llgoWasm)
	if err != nil {
		t.Fatal(err)
	}
	if proof.DataLayout != llgoWasm || len(proof.ClosureSHA256) != 64 {
		t.Fatalf("rebound terminal proof = %+v", proof)
	}

	wide := parseExecutorLeafModule(t, `
target datalayout = "`+clangWasm+`"
target triple = "wasm32-unknown-unknown"
define i128 @leaf(i128 %value) {
entry:
  ret i128 %value
}`)
	if _, err := ProveExecutorLeafForDataLayout(wide, "leaf", llgoWasm); err == nil ||
		!strings.Contains(err.Error(), "different ABI layout") {
		t.Fatalf("i128 alternate-layout proof error = %v", err)
	}
}

func TestProveExecutorLeafFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		ir   string
		want string
	}{
		{
			name: "external call",
			ir: `
declare i32 @read(i32, ptr, i64)
define i32 @leaf(ptr %buffer) {
entry:
  %value = call i32 @read(i32 0, ptr %buffer, i64 1)
  ret i32 %value
}`,
			want: "has no definition",
		},
		{
			name: "indirect call",
			ir: `
define void @leaf(ptr %callback) {
entry:
  call void %callback()
  ret void
}`,
			want: "indirect",
		},
		{
			name: "cfg cycle",
			ir: `
define void @leaf(i1 %again) {
entry:
  br label %loop
loop:
  br i1 %again, label %loop, label %done
done:
  ret void
}`,
			want: "CFG cycle",
		},
		{
			name: "recursion",
			ir: `
define void @leaf() {
entry:
  call void @leaf()
  ret void
}`,
			want: "recursive",
		},
		{
			name: "non-C root calling convention",
			ir: `
define fastcc void @leaf() {
entry:
  ret void
}`,
			want: "non-C calling convention",
		},
		{
			name: "retained pointer",
			ir: `
@retained = global ptr null
define void @leaf(ptr %value) {
entry:
  store ptr %value, ptr @retained
  ret void
}`,
			want: "stores pointer-derived data",
		},
		{
			name: "retained pointer through local spill",
			ir: `
@retained = global ptr null
define void @leaf(ptr %value) {
entry:
  %slot = alloca ptr
  store ptr %value, ptr %slot
  %copy = load ptr, ptr %slot
  store ptr %copy, ptr @retained
  ret void
}`,
			want: "stores pointer-derived data",
		},
		{
			name: "retained pointer encoding through integer spill",
			ir: `
@retained = global i64 0
define void @leaf(ptr %value) {
entry:
  %word = ptrtoint ptr %value to i64
  %slot = alloca i64
  store i64 %word, ptr %slot
  %copy = load i64, ptr %slot
  store i64 %copy, ptr @retained
  ret void
}`,
			want: "stores pointer-derived data",
		},
		{
			name: "retained pointer encoding loaded from caller memory",
			ir: `
@retained = global i64 0
define void @leaf(ptr %value) {
entry:
  %copy = load i64, ptr %value
  store i64 %copy, ptr @retained
  ret void
}`,
			want: "stores pointer-derived data",
		},
		{
			name: "local storage containing pointer is published",
			ir: `
@retained = global ptr null
define void @leaf(ptr %value) {
entry:
  %slot = alloca ptr
  store ptr %value, ptr %slot
  store ptr %slot, ptr @retained
  ret void
}`,
			want: "publishes a function-local storage address",
		},
		{
			name: "local storage content escapes through helper",
			ir: `
@retained = global ptr null
define internal void @helper(ptr %slot) {
entry:
  %value = load ptr, ptr %slot
  store ptr %value, ptr @retained
  ret void
}
define void @leaf(ptr %value) {
entry:
  %slot = alloca ptr
  store ptr %value, ptr %slot
  call void @helper(ptr %slot)
  ret void
}`,
			want: "stores pointer-derived data",
		},
		{
			name: "local storage address returned",
			ir: `
define ptr @leaf() {
entry:
  %slot = alloca i8
  ret ptr %slot
}`,
			want: "returns a function-local storage address",
		},
		{
			name: "pointer encoding passed to scalar helper",
			ir: `
define internal void @helper(i64 %value) {
entry:
  ret void
}
define void @leaf(ptr %value) {
entry:
  %word = ptrtoint ptr %value to i64
  call void @helper(i64 %word)
  ret void
}`,
			want: "passes pointer-derived data",
		},
		{
			name: "pointer returned through helper",
			ir: `
@retained = global ptr null
define internal ptr @helper(ptr %value) {
entry:
  ret ptr %value
}
define void @leaf(ptr %value) {
entry:
  %copy = call ptr @helper(ptr %value)
  store ptr %copy, ptr @retained
  ret void
}`,
			want: "stores pointer-derived data",
		},
		{
			name: "wide atomic may lower to a runtime call",
			ir: `
target datalayout = "e-p:64:64-i64:64-i128:128-n8:16:32:64-S128"
target triple = "x86_64-unknown-linux-gnu"
define i128 @leaf(ptr %cell) {
entry:
  %old = atomicrmw add ptr %cell, i128 1 seq_cst, align 16
  ret i128 %old
}`,
			want: "unsupported 128-bit atomic",
		},
		{
			name: "unsupported va_arg",
			ir: `
define i32 @leaf(ptr %ap) {
entry:
  %value = va_arg ptr %ap, i32
  ret i32 %value
}`,
			want: "unsupported LLVM opcode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := parseExecutorLeafModule(t, test.ir)
			if _, err := ProveExecutorLeaf(module, "leaf"); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("ProveExecutorLeaf error = %v; want %q", err, test.want)
			}
		})
	}
}

func containsExecutorLeafSymbol(symbols []string, want string) bool {
	for _, symbol := range symbols {
		if symbol == want {
			return true
		}
	}
	return false
}

func parseExecutorLeafModule(t *testing.T, ir string) llvm.Module {
	t.Helper()
	context := llvm.NewContext()
	path := filepath.Join(t.TempDir(), "executor-leaf.ll")
	if err := os.WriteFile(path, []byte(ir), 0o644); err != nil {
		context.Dispose()
		t.Fatal(err)
	}
	buffer, err := llvm.NewMemoryBufferFromFile(path)
	if err != nil {
		context.Dispose()
		t.Fatal(err)
	}
	module, err := context.ParseIR(buffer)
	if err != nil {
		context.Dispose()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		module.Dispose()
		context.Dispose()
	})
	return module
}
