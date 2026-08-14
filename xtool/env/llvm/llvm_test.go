//go:build !llgo

package llvm

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestSetupPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a shell script")
	}

	binDir := t.TempDir()
	llvmConfig := writeLLVMConfig(t, "22.1.8", 0)
	t.Setenv("LLVM_CONFIG", llvmConfig)
	t.Setenv("LLGO_TEST_LLVM_BINDIR", binDir)
	original := filepath.Join(t.TempDir(), "original")
	t.Setenv("PATH", original)

	if err := SetupPath(); err != nil {
		t.Fatal(err)
	}
	want := binDir + string(os.PathListSeparator) + original
	if got := os.Getenv("PATH"); got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}

	if err := SetupPath(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PATH"); got != want {
		t.Fatalf("second setup changed PATH to %q, want %q", got, want)
	}
}

func TestSetupPathRejectsUnsupportedLLVM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a shell script")
	}

	llvmConfig := writeLLVMConfig(t, "19.1.7", 0)
	binDir := t.TempDir()
	t.Setenv("LLVM_CONFIG", llvmConfig)
	t.Setenv("LLGO_TEST_LLVM_BINDIR", binDir)
	t.Setenv("PATH", filepath.Join(t.TempDir(), "original"))
	before := os.Getenv("PATH")

	err := SetupPath()
	if err == nil || !strings.Contains(err.Error(), "LLGo requires LLVM 22") {
		t.Fatalf("SetupPath error = %v, want LLVM 22 requirement", err)
	}
	if got := os.Getenv("PATH"); got != before {
		t.Fatalf("PATH changed from %q to %q for unsupported LLVM", before, got)
	}
}

func TestSetupPathRejectsBrokenLLVMConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a shell script")
	}

	llvmConfig := writeLLVMConfig(t, "22.1.8", 7)
	t.Setenv("LLVM_CONFIG", llvmConfig)
	t.Setenv("PATH", filepath.Join(t.TempDir(), "original"))
	before := os.Getenv("PATH")

	err := SetupPath()
	if err == nil || !strings.Contains(err.Error(), "run LLVM configuration") {
		t.Fatalf("SetupPath error = %v, want llvm-config execution failure", err)
	}
	if got := os.Getenv("PATH"); got != before {
		t.Fatalf("PATH changed from %q to %q for broken llvm-config", before, got)
	}
}

func TestParseMajorVersion(t *testing.T) {
	for _, test := range []struct {
		version string
		want    int
		ok      bool
	}{
		{version: "22.1.8", want: 22, ok: true},
		{version: "19.1.7", want: 19, ok: true},
		{version: "LLVM 22.1.8"},
		{version: ""},
	} {
		got, err := parseMajorVersion(test.version)
		if (err == nil) != test.ok || got != test.want {
			t.Errorf("parseMajorVersion(%q) = %d, %v; want %d, ok=%v", test.version, got, err, test.want, test.ok)
		}
	}
}

func writeLLVMConfig(t *testing.T, version string, versionExit int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llvm-config")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --version) printf '%s\\n' '" + version + "'; exit " + strconv.Itoa(versionExit) + ";;\n" +
		"  --bindir) printf '%s\\n' \"${LLGO_TEST_LLVM_BINDIR}\";;\n" +
		"  *) exit 2;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
