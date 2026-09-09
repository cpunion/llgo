package build

import (
	"fmt"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestLowerWasmAggregateCopies(t *testing.T) {
	for _, bits := range []int{32, 64} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			td := llvm.NewTargetData(fmt.Sprintf("e-p:%d:%d-i64:64-n32:64-S128", bits, bits))
			defer td.Dispose()
			for _, tc := range []struct {
				name, body, intrinsic string
				volatile              bool
			}{
				{"copy", "%v = load [8192 x i8], ptr %src\nstore [8192 x i8] %v, ptr %dst", "memmove", false},
				{"volatile source", "%v = load volatile [8192 x i8], ptr %src\nstore [8192 x i8] %v, ptr %dst", "memmove", true},
				{"volatile destination", "%v = load [8192 x i8], ptr %src\nstore volatile [8192 x i8] %v, ptr %dst", "memmove", true},
				{"overlap", "%v = load volatile [8192 x i8], ptr %src\nstore volatile [8192 x i8] %v, ptr %src", "memmove", true},
				{"zero", "store [8192 x i8] zeroinitializer, ptr %dst", "memset", false},
				{"volatile zero", "store volatile [8192 x i8] zeroinitializer, ptr %dst", "memset", true},
				{"pointer fields", "%v = load {ptr, [8192 x i8]}, ptr %src\nstore {ptr, [8192 x i8]} %v, ptr %dst", "memmove", false},
				{"threshold", "%v = load [4096 x i8], ptr %src\nstore [4096 x i8] %v, ptr %dst", "memmove", false},
				{"small", "%v = load [4095 x i8], ptr %src\nstore [4095 x i8] %v, ptr %dst", "", false},
				{"scalar", "%v = load i64, ptr %src\nstore i64 %v, ptr %dst", "", false},
				{"intervening store", "%v = load [8192 x i8], ptr %src\nstore i8 1, ptr %src\nstore [8192 x i8] %v, ptr %dst", "", false},
				{"intervening call", "%v = load [8192 x i8], ptr %src\ncall void @other()\nstore [8192 x i8] %v, ptr %dst", "", false},
				{"another use", "%v = load [8192 x i8], ptr %src\nstore [8192 x i8] %v, ptr %dst\nstore [8192 x i8] %v, ptr %src", "", false},
				{"unknown value", "store [8192 x i8] poison, ptr %dst", "", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					mod := parseWasmCallerIR(t, "declare void @other()\ndefine void @copy(ptr %dst, ptr %src) {\n"+tc.body+"\nret void\n}")
					before := mod.String()
					want := 0
					if tc.intrinsic != "" {
						want = 1
					}
					if got := lowerWasmAggregateCopies("wasm", td, mod); got != want {
						t.Fatalf("lowered %d operations, want %d:\n%s", got, want, mod.String())
					}
					if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
						t.Fatal(err)
					}
					if want == 0 {
						if mod.String() != before {
							t.Fatal("changed an ineligible memory operation")
						}
						return
					}
					body := mod.NamedFunction("copy").String()
					intrinsic := fmt.Sprintf("@llvm.%s.p0.", tc.intrinsic)
					if !strings.Contains(body, intrinsic) || !strings.Contains(body, fmt.Sprintf("i1 %t", tc.volatile)) {
						t.Fatalf("copy lost its intrinsic or volatility:\n%s", body)
					}
					if strings.Contains(body, "alloca ") || strings.Contains(body, "AllocU") || strings.Contains(body, "load ") || strings.Contains(body, "store ") {
						t.Fatalf("copy allocated storage or retained aggregate accesses:\n%s", body)
					}
					if got := lowerWasmAggregateCopies("wasm", td, mod); got != 0 {
						t.Fatal("copy lowering is not idempotent")
					}
				})
			}
			mod := parseWasmCallerIR(t, "define void @copy(ptr %p) { store volatile [8192 x i8] zeroinitializer, ptr %p\nret void }")
			before := mod.String()
			if lowerWasmAggregateCopies("amd64", td, mod) != 0 || mod.String() != before {
				t.Fatal("changed native aggregate operations")
			}
		})
	}
}
