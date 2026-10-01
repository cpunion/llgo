package plan9asm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTranslateAssemblyGoMetadataHeaders(t *testing.T) {
	config := os.Getenv("LLVM_CONFIG")
	if config == "" {
		config = "llvm-config"
	}
	version, err := exec.Command(config, "--version").CombinedOutput()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(version)), "22.") {
		t.Fatalf("LLVM 22 required: %s, %v", version, err)
	}
	bindir, err := exec.Command(config, "--bindir").Output()
	if err != nil {
		t.Fatal(err)
	}
	clang := filepath.Join(strings.TrimSpace(string(bindir)), "clang")
	for _, target := range []struct{ arch, goos, triple, move, register string }{
		{"amd64", "linux", "x86_64-unknown-linux-gnu", "MOVQ", "AX"},
		{"amd64", "linux", "x86_64-unknown-linux-musl", "MOVQ", "AX"},
		{"amd64", "darwin", "x86_64-apple-darwin", "MOVQ", "AX"},
		{"amd64", "windows", "x86_64-pc-windows-msvc", "MOVQ", "AX"},
		{"arm64", "linux", "aarch64-unknown-linux-gnu", "MOVD", "R0"},
		{"arm64", "linux", "aarch64-unknown-linux-musl", "MOVD", "R0"},
		{"arm64", "darwin", "aarch64-apple-darwin", "MOVD", "R0"},
		{"arm64", "windows", "aarch64-pc-windows-msvc", "MOVD", "R0"},
	} {
		t.Run(target.triple, func(t *testing.T) {
			dir := t.TempDir()
			pkg := mustTestPackage(t, "example.org/metadata", "package metadata\nfunc Metadata(value int64) int64\n")
			pkg.Dir = dir
			source := "#include \"funcdata.h\"\n#include \"textflag.h\"\nTEXT ·Metadata(SB),NOSPLIT,$0-16\n" +
				"GO_ARGS\nGO_RESULTS_INITIALIZED\nNO_LOCAL_POINTERS\n" +
				target.move + " value+0(FP), " + target.register + "\n" +
				target.move + " " + target.register + ", ret+8(FP)\nRET\n"
			asm := filepath.Join(dir, "metadata_"+target.arch+".s")
			if err := os.WriteFile(asm, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			goObject := filepath.Join(dir, "go.o")
			command := exec.Command("go", "tool", "asm", "-p", pkg.PkgPath, "-I", filepath.Join(runtime.GOROOT(), "pkg", "include"), "-o", goObject, asm)
			command.Env = append(os.Environ(), "GOOS="+target.goos, "GOARCH="+target.arch)
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("actual Go metadata object: %v\n%s", err, out)
			}
			translated, err := TranslateFileForPkg(pkg, asm, target.goos, target.arch, nil)
			if err != nil {
				t.Fatal(err)
			}
			ll := filepath.Join(dir, "metadata.ll")
			if err := os.WriteFile(ll, []byte(translated.LLVMIR), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(clang, "-target", target.triple, "-O2", "-c", ll, "-o", filepath.Join(dir, "llvm.o")).CombinedOutput(); err != nil {
				t.Fatalf("LLVM metadata object: %v\n%s", err, out)
			}
			t.Log("actual Go object and LLVM 22 object; no runtime claim")
		})
	}
}

func TestAssemblyMetadataMacroWithoutHeaderRejected(t *testing.T) {
	pkg := mustTestPackage(t, "example.org/metadata", "package metadata\nfunc Metadata()\n")
	for _, name := range []string{"GO_ARGS", "GO_RESULTS_INITIALIZED", "NO_LOCAL_POINTERS"} {
		for _, prefix := range []string{"", "label: ", "label·unicode: "} {
			source := []byte("TEXT ·Metadata(SB),NOSPLIT,$0-0\n" + prefix + name + "\nRET\n")
			dir := t.TempDir()
			file := filepath.Join(dir, "metadata.s")
			if err := os.WriteFile(file, source, 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("go", "tool", "asm", "-o", filepath.Join(dir, "go.o"), file)
			command.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
			if out, err := command.CombinedOutput(); err == nil {
				t.Fatalf("actual Go accepted undefined macro %s: %s", name, out)
			}
			if _, err := TranslateSourceForPkg(pkg, "metadata.s", source, "linux", "amd64"); err == nil {
				t.Errorf("undefined metadata macro %s with prefix %q was accepted", name, prefix)
			}
		}
	}
}

func TestAssemblyMetadataLexerDoesNotInspectStringOperands(t *testing.T) {
	pkg := mustTestPackage(t, "example.org/metadata", "package metadata")
	source := []byte("DATA literal(SB)/8,$\";NO_LOCAL_POINTERS;ignored\"\n")
	if _, err := preprocessAssemblyForPkg(pkg, "literal.s", source, nil, "linux", "amd64", TranslateOptions{}); err != nil {
		t.Fatalf("metadata word inside a string was mistaken for an instruction: %v", err)
	}
}
