//go:build !llgo

package cabi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestArm64LargeArrayUsesIndirectABI(t *testing.T) {
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	llvm.InitializeAllTargetInfos()

	ctx := llvm.NewContext()
	defer ctx.Dispose()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	tr := NewTransformer(prog, "arm64-apple-darwin", "", ModeAllFunc, true)
	if tr.isLargeAggregate(ctx.Int8Type()) {
		t.Fatal("scalar type was classified as a large aggregate")
	}
	if tr.isLargeAggregate(llvm.ArrayType(ctx.Int8Type(), 16)) {
		t.Fatal("small array was classified as a large aggregate")
	}

	large := llvm.ArrayType(ctx.Int8Type(), 1<<28)
	if !tr.isLargeAggregate(large) {
		t.Fatal("large array was not classified as a large aggregate")
	}
	ftyp := llvm.FunctionType(large, nil, false)
	if info := tr.GetTypeInfo(ctx, ftyp, large, 0); info.Kind != AttrPointer {
		t.Fatalf("large array ABI kind = %v, want AttrPointer", info.Kind)
	}

	// A homogeneous floating-point aggregate remains register-returned even
	// though its size exceeds the ordinary 16-byte aggregate limit.
	hfa := llvm.ArrayType(ctx.DoubleType(), 4)
	ftyp = llvm.FunctionType(hfa, nil, false)
	if info := tr.GetTypeInfo(ctx, ftyp, hfa, 0); info.Kind != AttrNone {
		t.Fatalf("four-double HFA ABI kind = %v, want AttrNone", info.Kind)
	}
}

func TestLargeArrayIndirectReturnStaysInMemory(t *testing.T) {
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	llvm.InitializeAllTargetInfos()

	const testIR = `
%Large = type [1048577 x i8]

define %Large @callee(ptr %src) {
entry:
  %v = load %Large, ptr %src, align 1
  %first = getelementptr inbounds %Large, ptr %src, i64 0, i64 0
  store i8 9, ptr %first, align 1
  ret %Large %v
}

define i8 @caller(ptr %src) {
entry:
  %v = call %Large @callee(ptr %src)
  %dst = alloca %Large, align 1
  store %Large %v, ptr %dst, align 1
  %first = getelementptr inbounds %Large, ptr %dst, i64 0, i64 0
  %result = load i8, ptr %first, align 1
  ret i8 %result
}

define void @volatile_caller(ptr %src, ptr %dst) {
entry:
  %v = call %Large @callee(ptr %src)
  store volatile %Large %v, ptr %dst, align 1
  ret void
}

define %Large @self_copy(ptr %src) {
entry:
  %copy = load %Large, ptr %src, align 1
  store %Large %copy, ptr %src, align 1
  %result = load %Large, ptr %src, align 1
  ret %Large %result
}

define %Large @volatile_copy(ptr %src) {
entry:
  %copy = load volatile %Large, ptr %src, align 1
  store %Large %copy, ptr %src, align 1
  %result = load %Large, ptr %src, align 1
  ret %Large %result
}
`

	ctx := llvm.NewContext()
	defer ctx.Dispose()
	tmpfile := filepath.Join(t.TempDir(), "large_array_return.ll")
	if err := os.WriteFile(tmpfile, []byte(testIR), 0o644); err != nil {
		t.Fatalf("write test IR: %v", err)
	}
	buf, err := llvm.NewMemoryBufferFromFile(tmpfile)
	if err != nil {
		t.Fatalf("read test IR: %v", err)
	}
	mod, err := ctx.ParseIR(buf)
	if err != nil {
		t.Fatalf("parse test IR: %v", err)
	}
	defer mod.Dispose()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	tr := NewTransformer(prog, "arm64-apple-darwin", "", ModeAllFunc, true)
	tr.TransformModule("test", mod)

	calleeIR := mod.NamedFunction("callee").String()
	if !strings.Contains(calleeIR, "define void @callee(ptr sret([1048577 x i8])") {
		t.Fatalf("large return was not lowered to sret:\n%s", calleeIR)
	}
	if strings.Contains(calleeIR, "load %Large") || strings.Contains(calleeIR, "store %Large") {
		t.Fatalf("callee retained a direct large aggregate copy:\n%s", calleeIR)
	}
	copyAtLoad := strings.Index(calleeIR, "call void @llvm.memcpy")
	mutation := strings.Index(calleeIR, "store i8 9")
	if copyAtLoad < 0 || mutation < 0 || copyAtLoad >= mutation {
		t.Fatalf("return value was not copied before source mutation:\n%s", calleeIR)
	}

	callerIR := mod.NamedFunction("caller").String()
	for _, want := range []string{"call void @callee(ptr sret([1048577 x i8])", "call void @llvm.memcpy", "load i8"} {
		if !strings.Contains(callerIR, want) {
			t.Fatalf("transformed caller missing %q:\n%s", want, callerIR)
		}
	}
	if strings.Contains(callerIR, "load %Large") || strings.Contains(callerIR, "store %Large") {
		t.Fatalf("caller reconstructed the large return as an SSA value:\n%s", callerIR)
	}
	volatileCallerIR := mod.NamedFunction("volatile_caller").String()
	if !strings.Contains(volatileCallerIR, "load [1048577 x i8]") ||
		!strings.Contains(volatileCallerIR, "store volatile [1048577 x i8]") {
		t.Fatalf("volatile call result store was unexpectedly rewritten:\n%s", volatileCallerIR)
	}

	selfCopyIR := mod.NamedFunction("self_copy").String()
	if strings.Contains(selfCopyIR, "load %Large") || strings.Contains(selfCopyIR, "store %Large") {
		t.Fatalf("exact large self-copy was not removed:\n%s", selfCopyIR)
	}
	if got := strings.Count(selfCopyIR, "call void @llvm.memcpy"); got != 1 {
		t.Fatalf("self-copy function has %d memcpy calls, want 1:\n%s", got, selfCopyIR)
	}

	volatileIR := mod.NamedFunction("volatile_copy").String()
	if !strings.Contains(volatileIR, "load volatile [1048577 x i8]") ||
		!strings.Contains(volatileIR, "store [1048577 x i8]") {
		t.Fatalf("volatile self-copy was unexpectedly removed:\n%s", volatileIR)
	}
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("transformed module is invalid: %v\n%s", err, mod.String())
	}
}
