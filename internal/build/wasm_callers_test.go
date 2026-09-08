package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

const panicLocationDecl = `declare void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm"(i32, ptr, i32, ptr, i32, i32)`
const panicLocationCall = `call void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm"(i32 1, ptr null, i32 2, ptr null, i32 3, i32 42)`

func parseWasmCallerIR(t *testing.T, source string) llvm.Module {
	t.Helper()
	ctx := llvm.NewContext()
	t.Cleanup(ctx.Dispose)
	path := filepath.Join(t.TempDir(), "caller.ll")
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

func TestCoalesceWasmPanicLocations(t *testing.T) {
	for _, tc := range []struct {
		name, between string
		removed       int
	}{
		{"adjacent", "", 1},
		{"three identical records", panicLocationCall, 2},
		{"read and arithmetic", "%x = load i32, ptr %p\n%y = add i32 %x, 1\n%z = zext i32 %y to i64\n%c = icmp eq i64 %z, 1", 1},
		{"aggregate construction", "%p1 = getelementptr i32, ptr %p, i32 1\n%a = insertvalue {ptr, i32} poison, ptr %p1, 0\n%b = extractvalue {ptr, i32} %a, 0", 1},
		{"write", "store i32 1, ptr %p", 0},
		{"call", "call void @other()", 0},
		{"volatile read", "%x = load volatile i32, ptr %p", 0},
		{"atomic read", "%x = load atomic i32, ptr %p acquire, align 4", 0},
		{"atomic write", "store atomic i32 1, ptr %p release, align 4", 0},
		{"fence", "fence seq_cst", 0},
		{"atomic modify", "%x = atomicrmw add ptr %p, i32 1 seq_cst", 0},
		{"compare exchange", "%x = cmpxchg ptr %p, i32 1, i32 2 seq_cst seq_cst", 0},
		{"block boundary", "br label %next\nnext:", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf("%s\ndeclare void @other()\ndefine void @f(ptr %%p) {\n%s\n%s\n%s\nret void\n}",
				panicLocationDecl, panicLocationCall, tc.between, panicLocationCall)
			mod := parseWasmCallerIR(t, source)
			if got := coalesceWasmPanicLocations("wasm", mod); got != tc.removed {
				t.Fatalf("removed %d calls, want %d:\n%s", got, tc.removed, mod.String())
			}
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCoalesceWasmPanicLocationsPreservesIdentity(t *testing.T) {
	for _, change := range [][2]string{
		{"i32 1,", "i32 9,"}, {"ptr null, i32 2", "ptr %p, i32 2"},
		{"i32 2,", "i32 9,"}, {"ptr null, i32 3", "ptr %p, i32 3"},
		{"i32 3,", "i32 9,"}, {"i32 42)", "i32 43)"},
	} {
		source := fmt.Sprintf("%s\ndefine void @f(ptr %%p) {\n%s\n%s\nret void\n}",
			panicLocationDecl, panicLocationCall, strings.Replace(panicLocationCall, change[0], change[1], 1))
		mod := parseWasmCallerIR(t, source)
		if got := coalesceWasmPanicLocations("wasm", mod); got != 0 {
			t.Fatalf("different position %v lost a recording call", change)
		}
	}
}

func TestCoalesceWasmPanicLocationsEligibility(t *testing.T) {
	source := fmt.Sprintf("%s\ndefine void @f() {\n%s\n%s\nret void\n}", panicLocationDecl, panicLocationCall, panicLocationCall)
	mod := parseWasmCallerIR(t, source)
	if got := coalesceWasmPanicLocations("amd64", mod); got != 0 || mod.String() == "" {
		t.Fatal("changed native instrumentation")
	}
	mod = parseWasmCallerIR(t, strings.ReplaceAll(source, "i32", "i64"))
	if got := coalesceWasmPanicLocations("wasm", mod); got != 1 {
		t.Fatal("memory64 scalar arguments were not coalesced")
	}
	mod = parseWasmCallerIR(t, "define void @f() { ret void }")
	if got := coalesceWasmPanicLocations("wasm", mod); got != 0 {
		t.Fatal("changed a module with no instrumentation")
	}
	for _, signature := range []string{
		`declare void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm"()`,
		`declare void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm"(i32)`,
	} {
		call := strings.Replace(signature, "declare", "call", 1)
		call = strings.Replace(call, "(i32)", "(i32 1)", 1)
		mod := parseWasmCallerIR(t, fmt.Sprintf("%s\ndefine void @f() {\n%s\n%s\nret void\n}", signature, call, call))
		if got := coalesceWasmPanicLocations("wasm", mod); got != 0 {
			t.Fatal("changed an unknown recording ABI")
		}
	}
	withBundle := panicLocationCall + ` [ "deopt"(i32 1) ]`
	mod = parseWasmCallerIR(t, fmt.Sprintf("%s\ndefine void @f() {\n%s\n%s\n%s\nret void\n}",
		panicLocationDecl, panicLocationCall, withBundle, panicLocationCall))
	if got := coalesceWasmPanicLocations("wasm", mod); got != 0 {
		t.Fatal("removed a recording with unknown operand bundles")
	}
}
