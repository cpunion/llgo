package buildinfo_test

import (
	"debug/buildinfo"
	"os"
	"runtime"
	"runtime/debug"
	"testing"
)

func TestReadFileAndType(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable failed: %v", err)
	}

	info, err := buildinfo.ReadFile(exe)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", exe, err)
	}
	if info == nil || info.GoVersion != runtime.Version() {
		t.Fatalf("ReadFile build info = %v, want GoVersion %q", info, runtime.Version())
	}
	running, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("runtime/debug.ReadBuildInfo failed")
	}
	if info.String() != running.String() {
		t.Fatalf("file and runtime build information differ:\nfile: %s\nruntime: %s", info, running)
	}
	if info.Path == "" {
		t.Fatal("build information has no main package path")
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	for key, want := range map[string]string{"-compiler": runtime.Compiler, "GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH} {
		if got := settings[key]; got != want {
			t.Errorf("build setting %q = %q, want %q", key, got, want)
		}
	}

	// Ensure the exported alias type is referenced by tests.
	var _ *buildinfo.BuildInfo = info
}

func TestRead(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable failed: %v", err)
	}
	f, err := os.Open(exe)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", exe, err)
	}
	defer f.Close()

	info, err := buildinfo.Read(f)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if info == nil || info.GoVersion != runtime.Version() {
		t.Fatalf("Read build info = %v, want GoVersion %q", info, runtime.Version())
	}
}
