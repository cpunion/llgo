//go:build !llgo

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package ssa_test

import (
	"go/types"
	"testing"

	"github.com/xgo-dev/llgo/ssa"
)

func TestCoroRuntimeContextStorageLayout(t *testing.T) {
	for _, test := range []struct {
		name   string
		target *ssa.Target
	}{
		{"darwin-arm64", &ssa.Target{GOOS: "darwin", GOARCH: "arm64"}},
		{"linux-amd64", &ssa.Target{GOOS: "linux", GOARCH: "amd64"}},
		{"wasm32", &ssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
		{"thumb32", &ssa.Target{GOOS: "linux", GOARCH: "arm", Resolved: &ssa.TargetSpec{
			Triple: "thumbv6m-unknown-unknown-eabi", CPU: "cortex-m0plus",
			Features: "+armv6-m,+soft-float,+strict-align,+thumb-mode",
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Load the actual production type declarations, then lay out their
			// target-independent task/context fields with LLVM TargetData. Host
			// Go's wasm layout has eight-byte pointers and cannot prove this ABI.
			prog := newRuntimeTestProgram(t, test.target)
			defer prog.Dispose()
			allocation := prog.RuntimeType("coroTaskAllocation")
			fields := allocation.RawType().Underlying().(*types.Struct)
			if fields.NumFields() != 2 || fields.Field(0).Name() != "task" || fields.Field(1).Name() != "context" {
				t.Fatal("task allocation no longer has a scheduler prefix and runtime tail")
			}
			task := prog.Type(fields.Field(0).Type(), ssa.InC)
			gp := prog.RuntimeType("g")
			context := prog.RuntimeType("coroRuntimeContext")
			want := [5]uint64{248, 96, 104, 352, 248}
			if prog.PointerSize() == 4 {
				// The runtime G contains uint64 IDs, unlike the scheduler G.
				// LLVM's eight-byte i64 alignment rounds its tail and the
				// combined allocation's context boundary independently.
				want = [5]uint64{156, 64, 72, 232, 160}
			}
			got := [5]uint64{prog.SizeOf(task), prog.SizeOf(gp), prog.SizeOf(context), prog.SizeOf(allocation), prog.OffsetOf(allocation, 1)}
			if got != want || prog.OffsetOf(allocation, 0) != 0 {
				t.Fatalf("ptr%d task/g/context/envelope/context-offset = %v, want %v", prog.PointerSize(), got, want)
			}
			checkOffset := func(typ ssa.Type, name string, want uint64) {
				t.Helper()
				fields := typ.RawType().Underlying().(*types.Struct)
				for index := 0; index < fields.NumFields(); index++ {
					if fields.Field(index).Name() == name {
						if got := prog.OffsetOf(typ, index); got != want {
							t.Fatalf("%s offset = %d, want %d", name, got, want)
						}
						return
					}
				}
				t.Fatalf("field %s missing", name)
			}
			checkOffset(task, "magic", 0)
			checkOffset(task, "preempt", 4)
			checkOffset(task, "root", 16)
			checkOffset(gp, "atomicstatus", uint64(6*prog.PointerSize()))
			checkOffset(gp, "contextKind", uint64(6*prog.PointerSize()+7))
			checkOffset(gp, "goid", uint64(6*prog.PointerSize()+8))
			t.Logf("ptr%d task/g/context/envelope/context-offset = %v", prog.PointerSize(), got)
		})
	}
}
