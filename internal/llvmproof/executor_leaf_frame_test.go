// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package llvmproof

import "testing"

func TestExecutorLeafCurrentFrameScalarRead(t *testing.T) {
	for _, target := range []string{"aarch64-apple-macosx26.0.0", "aarch64-unknown-linux-gnu", "x86_64-apple-macosx26.0.0", "x86_64-unknown-linux-gnu"} {
		t.Run(target, func(t *testing.T) {
			module := parseExecutorLeafModule(t, `
target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"
target triple = "`+target+`"
declare ptr @llvm.frameaddress.p0(i32 immarg) nocallback nofree nosync nounwind willreturn
define i64 @leaf() noinline {
 %frame = call ptr @llvm.frameaddress.p0(i32 0)
 %present = icmp ne ptr %frame, null
 br i1 %present, label %read, label %empty
read:
 %same = call ptr @llvm.frameaddress.p0(i32 0)
 %value = load i64, ptr %same, align 8
 ret i64 %value
empty:
 ret i64 0
}`)
			proof, err := ProveExecutorLeaf(module, "leaf")
			if err != nil || proof.MayFault {
				t.Fatalf("current frame word proof = (%+v, %v)", proof, err)
			}
		})
	}
}

func TestExecutorLeafFrameProofDoesNotAuthorizeOtherMemory(t *testing.T) {
	for _, tc := range []struct{ name, body, attrs, target, layout string }{
		{name: "older frame", body: `%frame = call ptr @llvm.frameaddress.p0(i32 1)
%value = load i64, ptr %frame, align 8
ret i64 %value`},
		{name: "next frame dereference", body: `%frame = call ptr @llvm.frameaddress.p0(i32 0)
%link = load i64, ptr %frame, align 8
%next = inttoptr i64 %link to ptr
%value = load i64, ptr %next, align 8
ret i64 %value`},
		{name: "derived address", body: `%frame = call ptr @llvm.frameaddress.p0(i32 0)
%next = getelementptr i64, ptr %frame, i64 1
%value = load i64, ptr %next, align 8
ret i64 %value`},
		{name: "write frame", body: `%frame = call ptr @llvm.frameaddress.p0(i32 0)
store i64 0, ptr %frame, align 8
ret i64 0`},
		{name: "wide load", body: `%frame = call ptr @llvm.frameaddress.p0(i32 0)
%wide = load i128, ptr %frame, align 8
%value = trunc i128 %wide to i64
ret i64 %value`},
		{name: "overaligned", body: `%frame = call ptr @llvm.frameaddress.p0(i32 0)
%value = load i64, ptr %frame, align 16
ret i64 %value`},
		{name: "naked", attrs: "naked"},
		{name: "bare metal", target: "aarch64-unknown-none-elf"},
		{name: "wasm32", target: "wasm32-unknown-unknown", layout: "e-p:32:32-i64:64-n32:64-S128"},
		{name: "x32", target: "x86_64-unknown-linux-gnux32", layout: "e-p:32:32-i64:64-n8:16:32:64-S128"},
		{name: "ordinary pointer", body: `%value = load volatile i64, ptr %cell, align 8
ret i64 %value`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.target == "" {
				tc.target = "aarch64-unknown-linux-gnu"
			}
			if tc.layout == "" {
				tc.layout = "e-p:64:64-i64:64-n8:16:32:64-S128"
			}
			if tc.body == "" {
				tc.body = `%frame = call ptr @llvm.frameaddress.p0(i32 0)
%value = load i64, ptr %frame, align 8
ret i64 %value`
			}
			module := parseExecutorLeafModule(t, `target datalayout = "`+tc.layout+`"
target triple = "`+tc.target+`"
declare ptr @llvm.frameaddress.p0(i32 immarg) nocallback nofree nosync nounwind willreturn
define i64 @leaf(ptr %cell) `+tc.attrs+` {
`+tc.body+`
}`)
			proof, err := ProveExecutorLeaf(module, "leaf")
			if err != nil || !proof.MayFault {
				t.Fatalf("unsafe frame demand = (%+v, %v)", proof, err)
			}
		})
	}
}
