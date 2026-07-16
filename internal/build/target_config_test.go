//go:build !llgo

package build

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/goplus/llgo/internal/crosscompile"
	"github.com/goplus/llgo/internal/optlevel"
	"github.com/goplus/llgo/internal/targets"
	intllvm "github.com/goplus/llgo/internal/xtool/llvm"
	llssa "github.com/goplus/llgo/ssa"
)

func TestCoroPlanDigestMetadataUsesEffectiveLLVMTarget(t *testing.T) {
	for _, tt := range []struct {
		name        string
		target      *llssa.Target
		pointerBits int
	}{
		{name: "native", target: &llssa.Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, pointerBits: 64},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, pointerBits: 32},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prog := llssa.NewProgram(tt.target)
			defer prog.Dispose()
			ctx := &context{
				prog:      prog,
				buildConf: &Config{EnableCoroEntryResolution: true},
			}
			metadata, err := buildCoroPlanDigestMetadata(ctx)
			if err != nil {
				t.Fatal(err)
			}
			effective := prog.TargetSpec()
			if metadata.TargetTriple != effective.Triple || metadata.TargetCPU != effective.CPU ||
				metadata.TargetFeatures != effective.Features || metadata.TargetABI != effective.TargetABI {
				t.Fatalf("metadata target = %+v, want effective target %+v", metadata, effective)
			}
			if metadata.PointerBits != tt.pointerBits {
				t.Fatalf("metadata pointer bits = %d, want %d", metadata.PointerBits, tt.pointerBits)
			}
			if metadata.DataLayout != prog.DataLayout() || metadata.DataLayout == "" {
				t.Fatalf("metadata data layout = %q, want %q", metadata.DataLayout, prog.DataLayout())
			}
			if metadata.Endianness != "little" && metadata.Endianness != "big" {
				t.Fatalf("metadata endianness = %q", metadata.Endianness)
			}
		})
	}
}

func TestNewLLSSATargetUsesResolvedLLVMConfig(t *testing.T) {
	nativeConf := &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH, OptLevel: optlevel.O2}
	nativeSpec := intllvm.GetTargetSpec(runtime.GOOS, runtime.GOARCH, "")
	nativeWant := llssa.TargetSpec{Triple: nativeSpec.Triple, CPU: nativeSpec.CPU, Features: nativeSpec.Features}
	tests := []struct {
		name   string
		conf   *Config
		export crosscompile.Export
		want   llssa.TargetSpec
	}{
		{
			name: "native",
			conf: nativeConf,
			export: crosscompile.Export{
				LLVMTarget: nativeSpec.Triple,
				CPU:        nativeSpec.CPU,
				Features:   nativeSpec.Features,
			},
			want: nativeWant,
		},
		{
			name: "wasm32",
			conf: &Config{Goos: "wasip1", Goarch: "wasm"},
			export: crosscompile.Export{
				LLVMTarget: "wasm32-unknown-wasip1",
				CPU:        "generic",
				Features:   "+bulk-memory,+mutable-globals,+nontrapping-fptoint,+sign-ext",
			},
			want: llssa.TargetSpec{
				Triple:   "wasm32-unknown-wasip1",
				CPU:      "generic",
				Features: "+bulk-memory,+mutable-globals,+nontrapping-fptoint,+sign-ext",
			},
		},
		{
			name: "wasm32-threads",
			conf: &Config{Goos: "wasip1", Goarch: "wasm"},
			export: crosscompile.Export{
				LLVMTarget: "wasm32-unknown-wasip1",
				CPU:        "generic",
				Features:   "+bulk-memory,+mutable-globals,+nontrapping-fptoint,+sign-ext,+atomics",
			},
			want: llssa.TargetSpec{
				Triple:   "wasm32-unknown-wasip1",
				CPU:      "generic",
				Features: "+bulk-memory,+mutable-globals,+nontrapping-fptoint,+sign-ext,+atomics",
			},
		},
		{
			name: "thumb",
			conf: &Config{Goos: "linux", Goarch: "arm", Target: "rp2040", OptLevel: optlevel.Oz},
			export: crosscompile.Export{
				LLVMTarget: "thumbv6m-unknown-unknown-eabi",
				CPU:        "cortex-m0plus",
				Features:   "+armv6-m,+soft-float,+strict-align,+thumb-mode",
			},
			want: llssa.TargetSpec{
				Triple:   "thumbv6m-unknown-unknown-eabi",
				CPU:      "cortex-m0plus",
				Features: "+armv6-m,+soft-float,+strict-align,+thumb-mode",
			},
		},
		{
			name: "riscv32",
			conf: &Config{Goos: "linux", Goarch: "arm", Target: "riscv32", OptLevel: optlevel.Oz},
			export: crosscompile.Export{
				LLVMTarget: "riscv32-unknown-none",
				CPU:        "generic-rv32",
				Features:   "+m,+a,+c",
				TargetABI:  "ilp32",
			},
			want: llssa.TargetSpec{
				Triple:    "riscv32-unknown-none",
				CPU:       "generic-rv32",
				Features:  "+m,+a,+c",
				TargetABI: "ilp32",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := newLLSSATarget(tt.conf, tt.export)
			if got := target.Spec(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("target.Spec() = %#v, want %#v", got, tt.want)
			}
			if target.OptLevel != tt.conf.OptLevel {
				t.Fatalf("target OptLevel = %v, want %v", target.OptLevel, tt.conf.OptLevel)
			}
		})
	}
}

func TestLLVMCPUAndFeaturesAffectBuildFingerprint(t *testing.T) {
	fingerprint := func(cpu, features string) string {
		ctx := &context{
			buildConf: &Config{Target: "board"},
			crossCompile: crosscompile.Export{
				CPU:      cpu,
				Features: features,
			},
		}
		manifest := newManifestBuilder()
		ctx.collectCommonInputs(manifest)
		return manifest.Fingerprint()
	}

	base := fingerprint("cortex-m0", "+thumb-mode")
	if got := fingerprint("cortex-m0plus", "+thumb-mode"); got == base {
		t.Fatal("different LLVM CPUs produced the same build fingerprint")
	}
	if got := fingerprint("cortex-m0", "+thumb-mode,+strict-align"); got == base {
		t.Fatal("different LLVM features produced the same build fingerprint")
	}
}

func TestDefaultTargetKeepsLegacyCacheIdentity(t *testing.T) {
	spec := intllvm.GetTargetSpec("linux", "amd64", "")
	ctx := &context{
		buildConf: &Config{Goos: "linux", Goarch: "amd64"},
		crossCompile: crosscompile.Export{
			LLVMTarget: spec.Triple,
			CPU:        spec.CPU,
			Features:   spec.Features,
		},
		llvmVersion: "test",
	}

	manifest := newManifestBuilder()
	ctx.collectEnvInputs(manifest)
	ctx.collectCommonInputs(manifest)
	if manifest.env.LlvmTriple != "" {
		t.Fatalf("default manifest LLVM triple = %q, want legacy empty value", manifest.env.LlvmTriple)
	}
	if manifest.common.LLVMCPU != "" || manifest.common.LLVMFeatures != "" {
		t.Fatalf("default manifest unexpectedly records resolved CPU/features: %#v", manifest.common)
	}
	if got := ctx.targetTriple(); got != "amd64-linux" {
		t.Fatalf("default cache target = %q, want legacy %q", got, "amd64-linux")
	}

	legacy := &context{
		buildConf:   &Config{Goos: "linux", Goarch: "amd64"},
		llvmVersion: "test",
	}
	legacyManifest := newManifestBuilder()
	legacy.collectEnvInputs(legacyManifest)
	legacy.collectCommonInputs(legacyManifest)
	if got, want := manifest.Fingerprint(), legacyManifest.Fingerprint(); got != want {
		t.Fatalf("resolved defaults changed the legacy fingerprint: got %s, want %s", got, want)
	}
}

func TestNonDefaultLLVMFeaturesEnterCacheIdentity(t *testing.T) {
	defaults := intllvm.GetTargetSpec("wasip1", "wasm", "")
	ctx := &context{
		buildConf: &Config{Goos: "wasip1", Goarch: "wasm"},
		crossCompile: crosscompile.Export{
			LLVMTarget: defaults.Triple,
			CPU:        defaults.CPU,
			Features:   defaults.Features + ",+atomics",
		},
		llvmVersion: "test",
	}
	if !ctx.hasNonDefaultLLVMConfig() {
		t.Fatal("WASI threads target features were classified as defaults")
	}
	manifest := newManifestBuilder()
	ctx.collectEnvInputs(manifest)
	ctx.collectCommonInputs(manifest)
	if manifest.env.LlvmTriple != defaults.Triple {
		t.Fatalf("manifest triple = %q, want %q", manifest.env.LlvmTriple, defaults.Triple)
	}
	if manifest.common.LLVMFeatures != ctx.crossCompile.Features {
		t.Fatalf("manifest features = %q, want %q", manifest.common.LLVMFeatures, ctx.crossCompile.Features)
	}
	if got := ctx.targetTriple(); got != defaults.Triple {
		t.Fatalf("cache target = %q, want %q", got, defaults.Triple)
	}
}

func TestResolvedTargetCompatibilityAudit(t *testing.T) {
	configs, err := targets.NewDefaultResolver().ResolveAll()
	if err != nil {
		t.Fatal(err)
	}
	llssa.Initialize(llssa.InitAll)
	tests := []struct {
		name    string
		applied bool
	}{
		{name: "atmega328p", applied: false},    // 16-bit AVR with a 32-bit arm frontend
		{name: "riscv64", applied: false},       // 64-bit backend with a 32-bit arm frontend
		{name: "k210", applied: false},          // incompatible RV64 layout falls back before lp64 ABI validation
		{name: "rp2040", applied: true},         // thumb/arm are layout-compatible
		{name: "riscv32", applied: true},        // riscv32/arm are layout-compatible
		{name: "wasip1", applied: true},         // llgo's wasm32 frontend override is compatible
		{name: "nintendoswitch", applied: true}, // aarch64/arm64 are layout-compatible
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, ok := configs[tt.name]
			if !ok {
				t.Fatalf("target %q missing from ResolveAll", tt.name)
			}
			target := newLLSSATarget(&Config{
				Goos:   cfg.GOOS,
				Goarch: cfg.GOARCH,
				Target: tt.name,
			}, crosscompile.Export{
				LLVMTarget: cfg.LLVMTarget,
				CPU:        cfg.CPU,
				Features:   cfg.Features,
				TargetABI:  cfg.TargetABI,
			})
			prog := llssa.NewProgram(target)
			defer prog.Dispose()
			wantRequested := llssa.TargetSpec{
				Triple:    cfg.LLVMTarget,
				CPU:       cfg.CPU,
				Features:  cfg.Features,
				TargetABI: cfg.TargetABI,
			}
			if got := prog.RequestedTargetSpec(); !reflect.DeepEqual(got, wantRequested) {
				t.Fatalf("requested target = %#v, want resolved config %#v", got, wantRequested)
			}
			applied := reflect.DeepEqual(prog.TargetSpec(), prog.RequestedTargetSpec())
			if applied != tt.applied {
				t.Fatalf("requested target applied = %v, want %v (requested=%#v effective=%#v)",
					applied, tt.applied, prog.RequestedTargetSpec(), prog.TargetSpec())
			}
		})
	}
}
