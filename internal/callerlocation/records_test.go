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

package callerlocation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

const recordDecl = `declare void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm"(i64, ptr, i64, ptr, i64, i64)
declare void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordCallerLocationWasm"(i64, ptr, i64, ptr, i64, i64)
declare void @ordinary()
declare void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocation"(i64, ptr, i64, ptr, i64, i64)
`

func record(kind string, args string) string {
	return `  call void @"` + runtimePrefix + kind + `"(` + args + ")\n"
}

const args = "i64 1, ptr null, i64 2, ptr null, i64 3, i64 4"

func TestDeduplicateWasmRecords(t *testing.T) {
	panicRecord := record("RecordPanicLocationWasm", args)
	callerRecord := record("RecordCallerLocationWasm", args)
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"repeated panic", panicRecord + "  store i32 7, ptr %p\n" + panicRecord, 1},
		{"repeated caller", callerRecord + callerRecord, 1},
		{"ordinary call", panicRecord + "  call void @ordinary()\n" + panicRecord, 0},
		{"indirect call", panicRecord + "  call void %f()\n" + panicRecord, 0},
		{"inline asm", panicRecord + `  call void asm sideeffect "", ""()` + "\n" + panicRecord, 0},
		{"different record kind", panicRecord + callerRecord + panicRecord, 0},
		{"new block", panicRecord + "  br label %next\nnext:\n" + panicRecord, 0},
		{"native record", record("RecordPanicLocation", args) + record("RecordPanicLocation", args), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod := parseIR(t, recordDecl+"define void @f(ptr %p, ptr %f) {\nentry:\n"+tc.body+"  ret void\n}\n")
			before := mod.String()
			if got := DeduplicateWasmRecords(mod); got != tc.want {
				t.Fatalf("removed %d records, want %d:\n%s", got, tc.want, mod.String())
			}
			if tc.want == 0 && mod.String() != before {
				t.Fatal("changed unrelated operations")
			}
			if strings.Contains(tc.body, "store i32 7") && !strings.Contains(mod.String(), "store i32 7") {
				t.Fatal("removed user store")
			}
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			if got := DeduplicateWasmRecords(mod); got != 0 {
				t.Fatalf("second pass removed %d more records", got)
			}
		})
	}
}

func TestDifferentRecordArguments(t *testing.T) {
	parts := strings.Split(args, ", ")
	for i, changed := range []string{"i64 9", "ptr %p", "i64 9", "ptr %p", "i64 9", "i64 9"} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			other := append([]string(nil), parts...)
			other[i] = changed
			body := record("RecordPanicLocationWasm", args) + record("RecordPanicLocationWasm", strings.Join(other, ", "))
			mod := parseIR(t, recordDecl+"define void @f(ptr %p) {\n"+body+"  ret void\n}\n")
			if got := DeduplicateWasmRecords(mod); got != 0 {
				t.Fatalf("removed record with different argument %d", i)
			}
		})
	}
}

func TestSameCallDifferentOperandCounts(t *testing.T) {
	mod := parseIR(t, recordDecl+"define void @f() {\n"+
		record("RecordPanicLocationWasm", args)+"  call void @ordinary()\n  ret void\n}\n")
	left := mod.NamedFunction("f").FirstBasicBlock().FirstInstruction()
	if sameCall(left, llvm.NextInstruction(left)) {
		t.Fatal("calls with different operand counts compared equal")
	}
}

func TestRecordDebugLocations(t *testing.T) {
	const debugInfo = `
!llvm.dbg.cu = !{!0}
!llvm.module.flags = !{!3}
!0 = distinct !DICompileUnit(language: DW_LANG_Go, file: !1, producer: "llgo", isOptimized: false, runtimeVersion: 0, emissionKind: FullDebug)
!1 = !DIFile(filename: "fixture.go", directory: ".")
!2 = distinct !DISubprogram(name: "f", scope: !1, file: !1, line: 1, type: !4, scopeLine: 1, spFlags: DISPFlagDefinition, unit: !0)
!3 = !{i32 2, !"Debug Info Version", i32 3}
!4 = !DISubroutineType(types: !5)
!5 = !{null}
!6 = !DILocation(line: 4, column: 1, scope: !2)
!7 = !DILocation(line: 4, column: 20, scope: !2)
`
	for _, tc := range []struct {
		name string
		loc  string
		want int
	}{
		{"same position", ", !dbg !6", 1},
		{"different column", ", !dbg !7", 0},
		{"missing position", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := strings.TrimSuffix(record("RecordPanicLocationWasm", args), "\n")
			mod := parseIR(t, recordDecl+"define void @f() !dbg !2 {\n"+
				call+", !dbg !6\n"+call+tc.loc+"\n  ret void, !dbg !6\n}\n"+debugInfo)
			before := mod.String()
			if got := DeduplicateWasmRecords(mod); got != tc.want {
				t.Fatalf("removed %d records, want %d", got, tc.want)
			}
			if tc.want == 0 && mod.String() != before {
				t.Fatal("lost a distinct debug location")
			}
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLargeInitializerRecords(t *testing.T) {
	const rows, repeats = 4096, 12
	var source strings.Builder
	source.WriteString(recordDecl + "define void @init(ptr %p) {\n")
	for i := 0; i < rows; i++ {
		lineArgs := fmt.Sprintf("i64 1, ptr null, i64 2, ptr null, i64 3, i64 %d", i+1)
		for j := 0; j < repeats; j++ {
			source.WriteString(record("RecordPanicLocationWasm", lineArgs))
			source.WriteString("  store i32 7, ptr %p\n")
		}
	}
	source.WriteString("  ret void\n}\n")
	mod := parseIR(t, source.String())
	if got, want := DeduplicateWasmRecords(mod), rows*(repeats-1); got != want {
		t.Fatalf("removed %d records, want %d", got, want)
	}
	ir := mod.String()
	if got := strings.Count(ir, `call void @"`+runtimePrefix+`RecordPanicLocationWasm"`); got != rows {
		t.Fatalf("kept %d records, want one per line (%d)", got, rows)
	}
	if got := strings.Count(ir, "store i32 7"); got != rows*repeats {
		t.Fatalf("changed initializer stores: %d", got)
	}
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}

func parseIR(t *testing.T, source string) llvm.Module {
	t.Helper()
	ctx := llvm.NewContext()
	t.Cleanup(ctx.Dispose)
	path := filepath.Join(t.TempDir(), "records.ll")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	buf, err := llvm.NewMemoryBufferFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := ctx.ParseIR(buf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mod.Dispose)
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	return mod
}
