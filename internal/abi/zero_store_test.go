//go:build !llgo

package abi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestAggregateZeroStoreScalarizationBudget(t *testing.T) {
	for _, layout := range []string{
		"e-m:o-i64:64-i128:128-n32:64-S128",
		"e-m:e-p:32:32-i64:64-n32:64-S128",
	} {
		ctx := llvm.NewContext()
		defer ctx.Dispose()
		path := filepath.Join(t.TempDir(), "zero.ll")
		const input = `
declare void @observe(ptr)
define void @zero_boundary(ptr %dst) {
  store [1024 x i8] zeroinitializer, ptr %dst, align 1
  ret void
}
define void @zero_64k(ptr %dst) {
  store [65536 x i8] zeroinitializer, ptr %dst, align 1
  ret void
}
define void @zero_struct(ptr %dst) {
  store { i8, [1024 x i8], i64 } zeroinitializer, ptr %dst, align 1
  ret void
}
define void @small_zero(ptr %dst) {
  store [1023 x i8] zeroinitializer, ptr %dst, align 1
  ret void
}
define void @volatile_zero(ptr %dst) {
  store volatile [65536 x i8] zeroinitializer, ptr %dst, align 1
  ret void
}
define void @nonzero(ptr %dst, [65536 x i8] %value) {
  store [65536 x i8] %value, ptr %dst, align 1
  ret void
}
define void @local_buffer(i64 %index, i8 %value) {
  %buffer = alloca [65536 x i8], align 1
  store [65536 x i8] zeroinitializer, ptr %buffer, align 1
  %element = getelementptr [65536 x i8], ptr %buffer, i64 0, i64 %index
  store i8 %value, ptr %element, align 1
  call void @observe(ptr %buffer)
  ret void
}
`
		if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
			t.Fatal(err)
		}
		buffer, err := llvm.NewMemoryBufferFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		module, err := ctx.ParseIR(buffer)
		if err != nil {
			t.Fatal(err)
		}
		defer module.Dispose()
		module.SetDataLayout(layout)
		data := llvm.NewTargetData(layout)
		defer data.Dispose()
		LowerLargeAggregates(data, module)
		for _, name := range []string{"zero_boundary", "zero_64k", "zero_struct", "local_buffer"} {
			body := module.NamedFunction(name).String()
			if strings.Count(body, "call void @llvm.memset") != 1 || strings.Contains(body, "zeroinitializer") {
				t.Fatalf("%s retained an aggregate zero store before SROA:\n%s", name, body)
			}
		}
		for name, want := range map[string]string{
			"small_zero":    "store [1023 x i8] zeroinitializer",
			"volatile_zero": "store volatile [65536 x i8] zeroinitializer",
			"nonzero":       "store [65536 x i8] %value",
		} {
			body := module.NamedFunction(name).String()
			if !strings.Contains(body, want) || strings.Contains(body, "llvm.memset") {
				t.Fatalf("%s changed an operation outside the zero-store budget:\n%s", name, body)
			}
		}
		if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
			t.Fatal(err)
		}
		options := llvm.NewPassBuilderOptions()
		err = module.RunPasses("function(sroa)", llvm.TargetMachine{}, options)
		options.Dispose()
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		function := module.NamedFunction("local_buffer")
		for block := function.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
			for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
				count++
			}
		}
		if count > 32 {
			t.Fatalf("64KiB zeroing expanded into %d instructions after SROA", count)
		}
		if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
			t.Fatal(err)
		}
	}
}
