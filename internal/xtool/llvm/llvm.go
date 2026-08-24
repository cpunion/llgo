package llvm

import (
	"runtime"

	archcfg "github.com/xgo-dev/llgo/internal/goarch"
)

// TargetSpec is the LLVM target-machine configuration derived from Go target
// settings. Target-specific JSON configuration may replace all of these fields
// after inheritance resolution.
type TargetSpec struct {
	Triple   string
	CPU      string
	Features string
}

func GetTargetTriple(goos, goarch string) string {
	return GetTargetTripleWithGOARM(goos, goarch, "")
}

// GetTargetTripleWithGOARM returns the LLVM target triple for a Go target.
// goarm selects the ARM version and floating-point ABI for GOARCH=arm.
func GetTargetTripleWithGOARM(goos, goarch, goarm string) string {
	return GetTargetSpec(goos, goarch, goarm).Triple
}

// GetTargetSpec resolves the legacy GOOS/GOARCH/GOARM target defaults shared by
// the cross-compile driver and the SSA backend.
func GetTargetSpec(goos, goarch, goarm string) (spec TargetSpec) {
	var llvmarch string
	var armConfig archcfg.ARM
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	switch goarch {
	case "386":
		if goos == "windows" {
			// LLVM's 32-bit MSVC target spelling uses i686.
			llvmarch = "i686"
		} else {
			llvmarch = "i386"
		}
	case "amd64":
		llvmarch = "x86_64"
	case "arm64":
		llvmarch = "aarch64"
	case "arm":
		armConfig, _ = archcfg.ParseARM(goarm)
		switch armConfig.Version {
		case "5":
			llvmarch = "armv5"
		case "6":
			llvmarch = "armv6"
		default:
			llvmarch = "armv7"
		}
	case "wasm":
		llvmarch = "wasm32"
	default:
		llvmarch = goarch
	}
	llvmvendor := "unknown"
	llvmos := goos
	switch goos {
	case "darwin":
		// Use macosx* instead of darwin, otherwise darwin/arm64 will refer
		// to iOS!
		llvmos = "macosx"
		if llvmarch == "aarch64" {
			// Looks like Apple prefers to call this architecture ARM64
			// instead of AArch64.
			llvmarch = "arm64"
		}
		llvmvendor = "apple"
	case "wasip1":
		llvmos = "wasip1"
	case "windows":
		// GOOS=windows defaults to the native Microsoft ABI. MinGW is a
		// separate target toolchain and must not be inferred from the host
		// shell.
		llvmvendor = "pc"
	}
	// Target triples (which actually have four components, but are called
	// triples for historical reasons) have the form:
	//   arch-vendor-os-environment
	spec.Triple = llvmarch + "-" + llvmvendor + "-" + llvmos
	if llvmos == "windows" {
		spec.Triple += "-msvc"
	} else if goarch == "arm" {
		spec.Triple += "-gnueabi"
		if !armConfig.SoftFloat {
			spec.Triple += "hf"
		}
	}

	switch goarch {
	case "386":
		spec.CPU = "pentium4"
		spec.Features = "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"
	case "amd64":
		spec.CPU = "x86-64"
		spec.Features = "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"
	case "arm":
		spec.CPU = "generic"
		switch llvmarch {
		case "armv5":
			if armConfig.SoftFloat {
				spec.Features = "+armv5t,+strict-align,-aes,-bf16,-d32,-dotprod,-fp-armv8,-fp-armv8d16,-fp-armv8d16sp,-fp-armv8sp,-fp16,-fp16fml,-fp64,-fpregs,-fullfp16,-mve.fp,-neon,-sha2,-thumb-mode,-vfp2,-vfp2sp,-vfp3,-vfp3d16,-vfp3d16sp,-vfp3sp,-vfp4,-vfp4d16,-vfp4d16sp,-vfp4sp"
			} else {
				spec.Features = "+armv5t,+strict-align,-aes,-bf16,-d32,-dotprod,-fp-armv8,-fp-armv8d16,-fp-armv8d16sp,-fp-armv8sp,-fp16,-fp16fml,+fp64,+fpregs,-fullfp16,-mve.fp,-neon,-sha2,-thumb-mode,+vfp2,+vfp2sp,-vfp3,-vfp3d16,-vfp3d16sp,-vfp3sp,-vfp4,-vfp4d16,-vfp4d16sp,-vfp4sp"
			}
		case "armv6":
			spec.Features = "+armv6,+dsp,+fp64,+strict-align,+vfp2,+vfp2sp,-aes,-d32,-fp-armv8,-fp-armv8d16,-fp-armv8d16sp,-fp-armv8sp,-fp16,-fp16fml,-fullfp16,-neon,-sha2,-thumb-mode,-vfp3,-vfp3d16,-vfp3d16sp,-vfp3sp,-vfp4,-vfp4d16,-vfp4d16sp,-vfp4sp"
		case "armv7":
			spec.Features = "+armv7-a,+d32,+dsp,+fp64,+neon,+vfp2,+vfp2sp,+vfp3,+vfp3d16,+vfp3d16sp,+vfp3sp,-aes,-fp-armv8,-fp-armv8d16,-fp-armv8d16sp,-fp-armv8sp,-fp16,-fp16fml,-fullfp16,-sha2,-thumb-mode,-vfp4,-vfp4d16,-vfp4d16sp,-vfp4sp"
		}
		if armConfig.SoftFloat {
			spec.Features += ",+soft-float"
		}
	case "arm64":
		spec.CPU = "generic"
		if goos == "darwin" {
			spec.Features = "+neon"
		} else {
			spec.Features = "+neon,-fmv"
		}
	case "wasm":
		spec.CPU = "generic"
		spec.Features = "+bulk-memory,+mutable-globals,+nontrapping-fptoint,+sign-ext"
	}
	return
}
