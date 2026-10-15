package crosscompile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMacOSSysrootHonorsExplicitSDK(t *testing.T) {
	sdk := t.TempDir()
	t.Setenv("SDKROOT", sdk)
	t.Setenv("PATH", "") // An explicit path must not consult xcrun.
	got, err := getMacOSSysroot()
	if err != nil || got != sdk {
		t.Fatalf("explicit SDK = %q, %v; want %q", got, err, sdk)
	}
}

func TestMacOSSysrootRejectsInvalidExplicitSDK(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, sdk := range []string{file, filepath.Join(dir, "missing")} {
		t.Setenv("SDKROOT", sdk)
		t.Setenv("PATH", "")
		if _, err := getMacOSSysroot(); err == nil || !strings.Contains(err.Error(), "SDKROOT") {
			t.Fatalf("invalid SDK %q must not silently fall back: %v", sdk, err)
		}
	}
}

func TestMacOSSysrootResolvesNamedAndDefaultSDK(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("xcrun shell fixture")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "xcrun"), []byte("#!/bin/sh\n[ \"$1\" = --sdk ] && [ \"$3\" = --show-sdk-path ] || exit 1\nprintf '/SDKs/%s.sdk\\n' \"$2\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, sdk := range []string{"", "macosx26.5"} {
		t.Setenv("SDKROOT", sdk)
		name := sdk
		if name == "" {
			name = "macosx"
		}
		got, err := getMacOSSysroot()
		if want := "/SDKs/" + name + ".sdk"; err != nil || got != want {
			t.Fatalf("SDK %q = %q, %v; want %q", sdk, got, err, want)
		}
	}
}
