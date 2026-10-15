package ssa

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestVerifyCoroAtomicCostModuleFixedVectorReduction(t *testing.T) {
	for _, test := range []struct {
		name, intrinsic, result, vector string
		cost                            uint64
	}{
		{"four_i64", "llvm.vector.reduce.add.v4i64", "i64", "<4 x i64>", 9},
		{"sixteen_i8", "llvm.vector.reduce.add.v16i8", "i8", "<16 x i8>", 33},
		{"one_i32", "llvm.vector.reduce.add.v1i32", "i32", "<1 x i32>", 3},
		{"scalable", "llvm.vector.reduce.add.nxv4i64", "i64", "<vscale x 4 x i64>", 0},
		{"wide_integer", "llvm.vector.reduce.add.v4i128", "i128", "<4 x i128>", 0},
		{"mismatched_return", "llvm.vector.reduce.add.v4i64", "i32", "<4 x i64>", 0},
		{"mismatched_name", "llvm.vector.reduce.add.v8i64", "i64", "<4 x i64>", 0},
		{"counterfeit", "llvm.vector.reduce.add.v4i64.fake", "i64", "<4 x i64>", 0},
		{"unsupported_reduction", "llvm.vector.reduce.mul.v4i64", "i64", "<4 x i64>", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Textual IR can express scalable vectors without adding a binding
			// extension solely for this negative capability gate.
			source := fmt.Sprintf(`declare %s @%s(%s)
define %s @atomic(%s %%input) {
  %%result = call %s @%s(%s %%input)
  ret %s %%result
}
`, test.result, test.intrinsic, test.vector, test.result, test.vector,
				test.result, test.intrinsic, test.vector, test.result)
			path := filepath.Join(t.TempDir(), "reduction.ll")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			buffer, err := llvm.NewMemoryBufferFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := llvm.NewContext()
			defer ctx.Dispose()
			module, err := ctx.ParseIR(buffer) // ParseIR takes ownership of buffer.
			if err != nil {
				t.Fatal(err)
			}
			defer module.Dispose()
			// The IR reader upgrades overloaded intrinsic names from their
			// types. Restore the supplied name so malformed/counterfeit cases
			// test the verifier itself, not the reader's canonicalization.
			for fn := module.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
				if fn.Name() != "atomic" {
					fn.SetName(test.intrinsic)
				}
			}
			addCoroAtomicCostTestMetadata(ctx, module, "atomic")
			report, err := VerifyCoroAtomicCostModule(module)
			if test.cost == 0 {
				if err == nil || !strings.Contains(err.Error(), "calls uncertified helper") {
					t.Fatalf("unsupported reduction error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Functions) != 1 || report.Functions[0].LLVMMaxCost != test.cost {
				t.Fatalf("reduction report = %+v, want scalarized cost %d", report, test.cost)
			}
		})
	}
}
