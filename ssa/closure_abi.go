package ssa

import (
	"go/types"
	"strings"

	"github.com/xgo-dev/llvm"
)

// closureContextABI describes only how the compiler-owned __llgo_ctx
// parameter is transported. It deliberately says nothing about the in-memory
// representation of a Go function value, which remains the fixed two-word
// {function-or-descriptor, environment} carrier.
type closureContextABI uint8

const (
	// closureContextExplicit keeps __llgo_ctx as an ordinary leading physical
	// parameter. It is the portable fallback for backends without a dedicated
	// closure-context register (notably WebAssembly).
	closureContextExplicit closureContextABI = iota
	closureContextNest
	closureContextSwiftSelf
)

func closureContextABIForTarget(triple, goos string, llvmMajor int) closureContextABI {
	arch, _, _ := strings.Cut(strings.ToLower(triple), "-")
	switch {
	case arch == "arm64", arch == "arm64_32", arch == "aarch64", arch == "aarch64_be":
		// handled below
	case arch == "x86_64",
		arch == "i386", arch == "i486", arch == "i586", arch == "i686",
		strings.HasPrefix(arch, "arm"), strings.HasPrefix(arch, "thumb"),
		arch == "riscv32", arch == "riscv64":
		return closureContextNest
	default:
		return closureContextExplicit
	}

	// LLVM 19 and 20 assign AArch64 nest to X18. Darwin and Windows reserve
	// X18 as a platform register, and Android may reserve it for shadow-call
	// stack. LLVM 21 moved nest to X15 for every AArch64 PCS. swiftself uses
	// X20 and is the safe hidden-context transport on the older affected
	// targets.
	if llvmMajor < 21 && aarch64PlatformReservesX18(triple, goos) {
		return closureContextSwiftSelf
	}
	return closureContextNest
}

func aarch64PlatformReservesX18(triple, goos string) bool {
	switch strings.ToLower(goos) {
	case "darwin", "ios", "tvos", "watchos", "windows", "android":
		return true
	}
	triple = strings.ToLower(triple)
	return strings.Contains(triple, "apple") ||
		strings.Contains(triple, "darwin") ||
		strings.Contains(triple, "windows") ||
		strings.Contains(triple, "win32") ||
		strings.Contains(triple, "android")
}

func (p Program) closureContextABI() closureContextABI {
	goos := ""
	if p.target != nil {
		goos = p.target.GOOS
	}
	return closureContextABIForTarget(p.spec.Triple, goos, llvmMajorVersion())
}

func (p Program) hasHiddenClosureContextABI() bool {
	return p.closureContextABI() != closureContextExplicit
}

func (p Program) closureContextAttribute() llvm.Attribute {
	var name string
	switch p.closureContextABI() {
	case closureContextNest:
		name = "nest"
	case closureContextSwiftSelf:
		name = "swiftself"
	default:
		return llvm.Attribute{}
	}
	kind := llvm.AttributeKindID(name)
	if kind == 0 {
		panic("ssa: LLVM has no " + name + " parameter attribute")
	}
	return p.ctx.CreateEnumAttribute(kind, 0)
}

func (p Program) markClosureContextFunction(fn llvm.Value, sig *types.Signature) {
	index := closureCtxPhysicalParamIndex(sig)
	attr := p.closureContextAttribute()
	if index < 0 || attr.IsNil() {
		return
	}
	fn.AddAttributeAtIndex(index+1, attr)
}

func (p Program) markClosureContextCall(call llvm.Value, sig *types.Signature) {
	index := closureCtxPhysicalParamIndex(sig)
	attr := p.closureContextAttribute()
	if index < 0 || attr.IsNil() {
		return
	}
	call.AddCallSiteAttribute(index+1, attr)
}
