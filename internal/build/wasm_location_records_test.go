package build

import (
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestOptimizeLLVMModuleWasmLocationRecords(t *testing.T) {
	const source = `
declare void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm"(i64, ptr, i64, ptr, i64, i64)
define void @initializer(ptr %out) {
  call void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm"(i64 1, ptr null, i64 2, ptr null, i64 3, i64 4)
  store i32 7, ptr %out
  call void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm"(i64 1, ptr null, i64 2, ptr null, i64 3, i64 4)
  ret void
}`
	for _, arch := range []string{"wasm", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			mod := parseWasmAggregateIR(t, source)
			ctx := &context{buildConf: &Config{Goarch: arch}}
			// ModeGen omits LLVM optimization, but still needs Wasm records
			// simplified before the module is consumed by the backend.
			if err := optimizeLLVMModule(ctx, "initializer", mod); err != nil {
				t.Fatal(err)
			}
			want := 2
			if arch == "wasm" {
				want = 1
			}
			if got := strings.Count(mod.String(), `call void @"github.com/xgo-dev/llgo/runtime/internal/runtime.RecordPanicLocationWasm"`); got != want {
				t.Fatalf("kept %d location records, want %d", got, want)
			}
			if !strings.Contains(mod.String(), "store i32 7") {
				t.Fatal("lost initializer store")
			}
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}
