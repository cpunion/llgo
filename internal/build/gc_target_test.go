//go:build !llgo

package build

import (
	"slices"
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
