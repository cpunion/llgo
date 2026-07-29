//go:build !llgo

package build

import (
	"slices"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/crosscompile"
)

func TestTargetGCBuildTags(t *testing.T) {
	tests := []struct {
		gc      string
		wantTag bool
		wantErr bool
	}{
		{gc: ""},
		{gc: "precise"},
		{gc: "conservative"},
		{gc: "leaking", wantTag: true},
		{gc: "none", wantTag: true},
		{gc: "invented", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.gc, func(t *testing.T) {
			tags, err := targetGCBuildTags(test.gc)
			if (err != nil) != test.wantErr {
				t.Fatalf("targetGCBuildTags(%q) error = %v, wantErr %v", test.gc, err, test.wantErr)
			}
			if !test.wantErr && slices.Contains(tags, "nogc") != test.wantTag {
				t.Fatalf("targetGCBuildTags(%q) = %v, want nogc=%v", test.gc, tags, test.wantTag)
			}
		})
	}
}

func TestTargetWasmGCBuildTags(t *testing.T) {
	t.Setenv(llgoWasiThreads, "0")
	wasm := crosscompile.Export{GC: "conservative", LLVMTarget: "wasm32-unknown-wasi"}
	tags, err := targetWasmGCBuildTags(
		&Config{Target: "wasip1-gc", Goos: "wasip1", Goarch: "wasm", BuildMode: BuildModeExe},
		wasm,
	)
	if err != nil || !slices.Equal(tags, []string{coroWasmGCBuildTag}) {
		t.Fatalf("conservative wasm tags = %v, %v", tags, err)
	}
	wasm.GC = "precise"
	if _, err := targetWasmGCBuildTags(&Config{Target: "precise-wasm"}, wasm); err == nil ||
		!strings.Contains(err.Error(), "only the non-moving conservative") {
		t.Fatalf("precise wasm error = %v", err)
	}
	wasm.GC = "conservative"
	t.Setenv(llgoWasiThreads, "1")
	if _, err := targetWasmGCBuildTags(
		&Config{Target: "wasip1-gc", Goos: "wasip1", Goarch: "wasm", BuildMode: BuildModeCShared},
		wasm,
	); err == nil || !strings.Contains(err.Error(), "WASI threads") {
		t.Fatalf("threaded wasm GC error = %v", err)
	}
	t.Setenv(llgoWasiThreads, "0")
	if _, err := targetWasmGCBuildTags(
		&Config{Target: "wasip2-gc", Goos: "linux", Goarch: "arm", BuildMode: BuildModeCShared},
		wasm,
	); err == nil || !strings.Contains(err.Error(), "only implemented for the serialized WASI Preview 1 command") {
		t.Fatalf("non-command wasm GC error = %v", err)
	}
	if _, err := targetWasmGCBuildTags(nil, wasm); err == nil ||
		!strings.Contains(err.Error(), "only implemented for the serialized WASI Preview 1 command") {
		t.Fatalf("configuration-free wasm GC error = %v", err)
	}
}

func TestEffectiveBuildTagsOwnsWasmGCCapability(t *testing.T) {
	t.Setenv(llgoWasiThreads, "0")
	export := crosscompile.Export{
		GC:         "conservative",
		GOOS:       "wasip1",
		GOARCH:     "wasm",
		LLVMTarget: "wasm32-unknown-wasi",
	}
	conf := &Config{
		Target:    "wasip1-gc",
		Goos:      "wasip1",
		Goarch:    "wasm",
		BuildMode: BuildModeExe,
	}
	tags, err := effectiveBuildTags(conf, export)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(strings.Split(tags, ","), coroWasmGCBuildTag) ||
		slices.Contains(strings.Split(tags, ","), "nogc") {
		t.Fatalf("effective conservative wasm tags = %q", tags)
	}

	conf.Tags = coroWasmGCBuildTag
	export.GC = "leaking"
	if _, err := effectiveBuildTags(conf, export); err == nil ||
		!strings.Contains(err.Error(), "compiler-reserved capability") {
		t.Fatalf("user-forged wasm GC tag error = %v", err)
	}
}

func TestTargetGCProfileAffectsFingerprint(t *testing.T) {
	fingerprint := func(gc string) string {
		ctx := &context{
			buildConf:    &Config{Goos: "linux", Goarch: "arm", Target: "wasip2"},
			crossCompile: crosscompile.Export{GC: gc},
		}
		manifest := newManifestBuilder()
		ctx.collectCommonInputs(manifest)
		if got := manifest.common.RuntimeGC; got != gc {
			t.Fatalf("manifest runtime GC = %q, want %q", got, gc)
		}
		return manifest.Fingerprint()
	}
	if leaking, precise := fingerprint("leaking"), fingerprint("precise"); leaking == precise {
		t.Fatal("runtime GC capability did not affect package fingerprint")
	}
}
