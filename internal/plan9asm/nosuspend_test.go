package plan9asm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	llvm "github.com/xgo-dev/llvm"
	extplan9asm "github.com/xgo-dev/plan9asm"
)

func TestProveNoSuspendLeafDirectClosure(t *testing.T) {
	translation := parseNoSuspendTestModule(t, `
declare i64 @llvm.ctpop.i64(i64) #0

define i64 @"example.com/asm.helper"(i64 %value) {
entry:
  %result = call i64 @llvm.ctpop.i64(i64 %value)
  ret i64 %result
}

define i64 @"example.com/asm.Leaf"(i64 %value) {
entry:
  %result = call i64 @"example.com/asm.helper"(i64 %value)
  ret i64 %result
}

attributes #0 = { nocallback nofree nosync nounwind speculatable willreturn memory(none) }
`)
	proof, err := ProveNoSuspendLeaf(translation, "example.com/asm.Leaf")
	if err != nil {
		t.Fatal(err)
	}
	if proof.Symbol != "example.com/asm.Leaf" || proof.Signature == "" || len(proof.CallClosure) != 3 || len(proof.ClosureSHA256) != 64 {
		t.Fatalf("proof = %+v; want exact leaf/helper/intrinsic closure and SHA-256", proof)
	}
	for _, name := range []string{"example.com/asm.Leaf", "example.com/asm.helper", "llvm.ctpop.i64"} {
		if !containsString(proof.CallClosure, name) {
			t.Fatalf("proof closure %v lacks %q", proof.CallClosure, name)
		}
	}
}

func TestProveNoSuspendLeafFloatingNegation(t *testing.T) {
	translation := parseNoSuspendTestModule(t, `
define double @"example.com/asm.Leaf"(double %value) {
entry:
  %result = fneg double %value
  ret double %result
}
`)
	if _, err := ProveNoSuspendLeaf(translation, "example.com/asm.Leaf"); err != nil {
		t.Fatal(err)
	}
}

func TestProveNoSuspendLeafBoundedInlineAssembly(t *testing.T) {
	translation := parseNoSuspendTestModule(t, `
define i64 @"example.com/asm.mrs"() {
entry:
  %counter = call i64 asm "mrs $0, CNTVCT_EL0", "=r"()
  %thread = call i64 asm sideeffect "mrs $0, TPIDR_EL0", "=r,~{memory}"()
  %result = xor i64 %counter, %thread
  ret i64 %result
}

define i32 @"example.com/asm.Leaf"(i32 %eax, i32 %ecx) {
entry:
  %cpuid = call { i32, i32, i32, i32 } asm sideeffect "cpuid", "={ax},={bx},={cx},={dx},{ax},{cx},~{dirflag},~{fpsr},~{flags}"(i32 %eax, i32 %ecx)
  %xgetbv = call { i32, i32 } asm sideeffect "xgetbv", "={ax},={dx},{cx},~{dirflag},~{fpsr},~{flags}"(i32 %ecx)
  %mrs = call i64 @"example.com/asm.mrs"()
  %cpuid.eax = extractvalue { i32, i32, i32, i32 } %cpuid, 0
  %xgetbv.eax = extractvalue { i32, i32 } %xgetbv, 0
  %mrs.i32 = trunc i64 %mrs to i32
  %x86 = xor i32 %cpuid.eax, %xgetbv.eax
  %result = xor i32 %x86, %mrs.i32
  ret i32 %result
}
`)
	if _, err := ProveNoSuspendLeaf(translation, "example.com/asm.Leaf"); err != nil {
		t.Fatal(err)
	}
}

func TestProveNoSuspendLeafInlineAssemblyFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		constraints string
		sideEffects bool
		alignStack  bool
		dialect     llvm.InlineAsmDialect
		canThrow    bool
		asm         string
		wideResult  bool
		want        string
	}{
		{name: "constraints", constraints: "={ax},={bx},={cx},={dx},{ax},{cx},~{dirflag},~{fpsr},~{memory}", sideEffects: true, dialect: llvm.InlineAsmDialectATT, want: "has constraints"},
		{name: "side effects", constraints: "={ax},={bx},={cx},={dx},{ax},{cx},~{dirflag},~{fpsr},~{flags}", dialect: llvm.InlineAsmDialectATT, want: "side-effects flag"},
		{name: "aligned stack", constraints: "={ax},={bx},={cx},={dx},{ax},{cx},~{dirflag},~{fpsr},~{flags}", sideEffects: true, alignStack: true, dialect: llvm.InlineAsmDialectATT, want: "aligned-stack"},
		{name: "dialect", constraints: "={ax},={bx},={cx},={dx},{ax},{cx},~{dirflag},~{fpsr},~{flags}", sideEffects: true, dialect: llvm.InlineAsmDialectIntel, want: "is not AT&T"},
		{name: "can throw", constraints: "={ax},={bx},={cx},={dx},{ax},{cx},~{dirflag},~{fpsr},~{flags}", sideEffects: true, dialect: llvm.InlineAsmDialectATT, canThrow: true, want: "can-throw"},
		{name: "unknown template", asm: "pause", constraints: "={ax},={bx},={cx},={dx},{ax},{cx},~{dirflag},~{fpsr},~{flags}", sideEffects: true, dialect: llvm.InlineAsmDialectATT, want: "not a bounded translator emission"},
		{name: "signature", constraints: "={ax},={bx},={cx},={dx},{ax},{cx},~{dirflag},~{fpsr},~{flags}", sideEffects: true, dialect: llvm.InlineAsmDialectATT, wideResult: true, want: "return fields[0]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			translation := newInlineAsmNoSuspendTestModule(t, test.asm, test.constraints, test.sideEffects, test.alignStack, test.dialect, test.canThrow, test.wideResult)
			if _, err := ProveNoSuspendLeaf(translation, "example.com/asm.Leaf"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ProveNoSuspendLeaf error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestProveNoSuspendLeafFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		ir   string
		want string
	}{
		{
			name: "external call",
			ir: `declare i64 @external(i64)
define i64 @"example.com/asm.Leaf"(i64 %value) {
entry:
  %result = call i64 @external(i64 %value)
  ret i64 %result
}`,
			want: "has no definition",
		},
		{
			name: "indirect call",
			ir: `define i64 @"example.com/asm.Leaf"(ptr %fn, i64 %value) {
entry:
  %result = call i64 %fn(i64 %value)
  ret i64 %result
}`,
			want: "indirect call",
		},
		{
			name: "unproved intrinsic",
			ir: `declare void @llvm.trap()
define i64 @"example.com/asm.Leaf"(i64 %value) {
entry:
  call void @llvm.trap()
  ret i64 %value
}`,
			want: "missing nofree attribute",
		},
		{
			name: "atomic synchronization",
			ir: `define i64 @"example.com/asm.Leaf"(ptr %value) {
entry:
  %result = atomicrmw add ptr %value, i64 1 seq_cst
  ret i64 %result
}`,
			want: "unsupported LLVM opcode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			translation := parseNoSuspendTestModule(t, test.ir)
			if _, err := ProveNoSuspendLeaf(translation, "example.com/asm.Leaf"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ProveNoSuspendLeaf error = %v; want %q", err, test.want)
			}
		})
	}
}

func newInlineAsmNoSuspendTestModule(t *testing.T, asm, constraints string, sideEffects, alignStack bool, dialect llvm.InlineAsmDialect, canThrow, wideResult bool) *ModuleTranslation {
	t.Helper()
	if asm == "" {
		asm = "cpuid"
	}
	context := llvm.NewContext()
	module := context.NewModule("nosuspend-inline-asm")
	i32 := context.Int32Type()
	returnTypes := []llvm.Type{i32, i32, i32, i32}
	if wideResult {
		returnTypes[0] = context.Int64Type()
	}
	returnType := context.StructType(returnTypes, false)
	functionType := llvm.FunctionType(returnType, []llvm.Type{i32, i32}, false)
	function := llvm.AddFunction(module, "example.com/asm.Leaf", functionType)
	builder := context.NewBuilder()
	builder.SetInsertPointAtEnd(context.AddBasicBlock(function, "entry"))
	inlineAsm := llvm.InlineAsm(functionType, asm, constraints, sideEffects, alignStack, dialect, canThrow)
	result := builder.CreateCall(functionType, inlineAsm, []llvm.Value{function.Param(0), function.Param(1)}, "result")
	builder.CreateRet(result)
	builder.Dispose()
	t.Cleanup(func() {
		module.Dispose()
		context.Dispose()
	})
	return &ModuleTranslation{
		Module: module,
		Signatures: map[string]extplan9asm.FuncSig{
			"example.com/asm.Leaf": {Name: "example.com/asm.Leaf"},
		},
	}
}

func parseNoSuspendTestModule(t *testing.T, ir string) *ModuleTranslation {
	t.Helper()
	context := llvm.NewContext()
	path := filepath.Join(t.TempDir(), "nosuspend.ll")
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
	return &ModuleTranslation{
		Module: module,
		Signatures: map[string]extplan9asm.FuncSig{
			"example.com/asm.Leaf": {Name: "example.com/asm.Leaf"},
		},
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
