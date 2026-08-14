package ssa

import (
	"go/types"
	"strings"

	"github.com/xgo-dev/llvm"
)

// closureEnvABI describes only the physical transport of an environment
// parameter. It deliberately says nothing about the in-memory representation
// of a Go function value, which remains the fixed two-word {code, environment}
// carrier. The environment is not part of a Go or go/types signature.
type closureEnvABI uint8

const (
	// closureEnvExplicit is the typed fallback used by WebAssembly and targets
	// for which no hidden parameter transport has been validated.
	closureEnvExplicit closureEnvABI = iota
	closureEnvNest
	closureEnvSwiftSelf
)

// Keep the coroutine backend's original names as aliases. Coroutine physical
// signatures already use these helpers, while ordinary closure lowering uses
// the environment-oriented API below; both must select one machine ABI.
type closureContextABI = closureEnvABI

const (
	closureContextExplicit  = closureEnvExplicit
	closureContextNest      = closureEnvNest
	closureContextSwiftSelf = closureEnvSwiftSelf
)

func closureEnvABIForTarget(triple string) closureEnvABI {
	triple = strings.ToLower(triple)
	arch, _, _ := strings.Cut(triple, "-")
	// Select the long-term machine ABI by physical target even when LLGo does
	// not yet support the target OS. FFI final-hop support may follow later
	// without changing compiled closure entries.
	switch {
	case arch == "arm64", arch == "arm64_32", arch == "aarch64", arch == "aarch64_be":
		// Keep a stable runtime ABI across LLVM versions on platforms which
		// reserve X18. swiftself uses X20 and is also usable by the libffi bridge
		// without rebuilding libffi.
		if aarch64UsesSwiftSelf(triple) {
			return closureEnvSwiftSelf
		}
		return closureEnvNest
	case strings.HasPrefix(arch, "arm"), strings.HasPrefix(arch, "thumb"):
		// LLVM lowers swiftself through the platform's dedicated self register.
		// This keeps ordinary C arguments in their normal ABI locations.
		return closureEnvSwiftSelf
	case arch == "x86_64", arch == "amd64",
		arch == "x86", arch == "386",
		arch == "i386", arch == "i486", arch == "i586", arch == "i686":
		return closureEnvNest
	case arch == "riscv32", arch == "riscv64":
		// LLVM and libffi lower the RISC-V static chain through t2 (x7).
		return closureEnvNest
	default:
		return closureEnvExplicit
	}
}

func aarch64UsesSwiftSelf(triple string) bool {
	// Apple, Android, and Windows reserve X18, so LLGo uses LLVM's
	// swiftself/X20 transport there.
	return strings.Contains(triple, "apple") ||
		strings.Contains(triple, "darwin") ||
		strings.Contains(triple, "android") ||
		strings.Contains(triple, "windows") ||
		strings.Contains(triple, "win32") ||
		strings.Contains(triple, "mingw")
}

// closureContextABIForTarget is retained for the coroutine lowering API. The
// goos only disambiguates legacy generic AArch64 triples which did not encode
// their platform. The ABI does not vary within LLGo's LLVM 22 baseline.
func closureContextABIForTarget(triple, goos string) closureContextABI {
	lower := strings.ToLower(triple)
	arch, _, _ := strings.Cut(lower, "-")
	if (arch == "arm64" || arch == "arm64_32" || arch == "aarch64" || arch == "aarch64_be") &&
		!aarch64UsesSwiftSelf(lower) {
		switch strings.ToLower(goos) {
		case "darwin", "ios", "tvos", "watchos", "windows", "android":
			return closureEnvSwiftSelf
		}
	}
	return closureEnvABIForTarget(triple)
}

func aarch64PlatformReservesX18(triple, goos string) bool {
	return closureContextABIForTarget(triple, goos) == closureEnvSwiftSelf
}

func (p *Target) closureEnvABI() closureEnvABI {
	triple := p.LLVMTarget
	if triple == "" {
		triple = p.Spec().Triple
	}
	return closureEnvABIForTarget(triple)
}

// ClosureEnvBuildTag selects the runtime half of the same physical ABI used
// by the backend. It must be added after a named target has resolved its real
// LLVM triple; GOARCH may only be a package-selection compatibility value.
func (p *Target) ClosureEnvBuildTag() string {
	switch p.closureEnvABI() {
	case closureEnvNest:
		return "llgo_closure_env_nest"
	case closureEnvSwiftSelf:
		return "llgo_closure_env_swiftself"
	default:
		return "llgo_closure_env_explicit"
	}
}

func (p Program) closureEnvABI() closureEnvABI {
	return p.Target().closureEnvABI()
}

func (p Program) closureContextABI() closureContextABI {
	return p.closureEnvABI()
}

func (p Program) hasHiddenClosureContextABI() bool {
	return p.closureEnvABI() != closureEnvExplicit
}

func (p Program) closureEnvAttribute() llvm.Attribute {
	var name string
	switch p.closureEnvABI() {
	case closureEnvNest:
		name = "nest"
	case closureEnvSwiftSelf:
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

func (p Program) closureContextAttribute() llvm.Attribute {
	return p.closureEnvAttribute()
}

func (p Program) markClosureEnvFunction(fn llvm.Value, physicalIndex int) {
	attr := p.closureEnvAttribute()
	if physicalIndex < 0 || attr.IsNil() {
		return
	}
	fn.AddAttributeAtIndex(physicalIndex+1, attr)
}

func (p Program) markClosureEnvCall(call llvm.Value, physicalIndex int) {
	attr := p.closureEnvAttribute()
	if physicalIndex < 0 || attr.IsNil() {
		return
	}
	call.AddCallSiteAttribute(physicalIndex+1, attr)
}

func (p Program) markClosureContextFunction(fn llvm.Value, sig *types.Signature) {
	p.markClosureEnvFunction(fn, closureCtxPhysicalParamIndex(sig))
}

func (p Program) markClosureContextCall(call llvm.Value, sig *types.Signature) {
	p.markClosureEnvCall(call, closureCtxPhysicalParamIndex(sig))
}

func isClosureCtxParam(param *types.Var) bool {
	if param == nil {
		return false
	}
	// NewEnvFunc tracks its environment out of band, while coroutine physical
	// entries expose the same compiler-owned word in their complete
	// (g,out,env,args...) signature. Both spellings are impossible in Go source
	// and therefore unambiguously identify the hidden-context parameter.
	name := param.Name()
	if name != closureCtx && name != "$env" {
		return false
	}
	switch typ := param.Type().Underlying().(type) {
	case *types.Pointer:
		return true
	case *types.Basic:
		return typ.Kind() == types.UnsafePointer
	default:
		return false
	}
}

// closureCtxParam returns the leading compiler-owned environment parameter if
// present. Source closure signatures keep the environment first even when a
// later physical ABI prefixes coroutine-owned parameters.
func closureCtxParam(sig *types.Signature) *types.Var {
	if sig == nil || sig.Params().Len() == 0 {
		return nil
	}
	first := sig.Params().At(0)
	if !isClosureCtxParam(first) {
		return nil
	}
	return first
}

// closureCtxPhysicalParamIndex returns the environment's actual LLVM
// parameter position. Coroutine entries can prefix __llgo_g and __llgo_out,
// so ABI attribute placement must not assume parameter zero.
func closureCtxPhysicalParamIndex(sig *types.Signature) int {
	if sig == nil {
		return -1
	}
	params := sig.Params()
	for index := 0; index < params.Len(); index++ {
		if isClosureCtxParam(params.At(index)) {
			return index
		}
	}
	return -1
}

// hideClosureCodeIdentity keeps LLVM from devirtualizing a native funcval call
// across the intentionally different IR prototypes of env and no-env entries.
// The empty tied-register asm is a machine-code no-op: it returns the same code
// pointer, but LLVM can no longer reinterpret a known no-env body as though the
// hidden environment were an ordinary first argument.
func (b Builder) hideClosureCodeIdentity(fn Expr) Expr {
	ftype := llvm.FunctionType(fn.Type.ll, []llvm.Type{fn.Type.ll}, false)
	asm := llvm.InlineAsm(ftype, "", "=r,0", false, false, llvm.InlineAsmDialectATT, false)
	return Expr{
		b.impl.CreateCall(ftype, asm, []llvm.Value{fn.impl}, "__llgo_funcval_code"),
		fn.Type,
	}
}
