package plan9asm

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAssemblyIncludesDirectoryFailureRetainsSourceAndPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "source.s")
	source := []byte("#include \"textflag.h\"\nTEXT ·probe(SB),NOSPLIT,$0-0\nRET\n")
	if err := os.WriteFile(file, source, 0600); err != nil {
		t.Fatal(err)
	}
	pkg := mustTestPackage(t, "example.org/headers", "package headers")
	pkg.Dir = filepath.Join(dir, "missing-package-directory")
	_, err := ReadAssemblyFileWithIncludes(pkg, file, nil, "linux", "amd64", TranslateOptions{})
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid actual package directory must remain a filesystem failure: %v", err)
	}
	for _, detail := range []string{pkg.Dir, file} {
		// Quoted Windows paths escape backslashes, so compare the complete
		// diagnostic with the same quoting used for the failed directory.
		quoted := strconv.Quote(detail)
		if !strings.Contains(err.Error(), quoted) {
			t.Errorf("directory failure omitted %q: %v", detail, err)
		}
	}
	if !strings.Contains(err.Error(), "resolve symlinks") {
		t.Errorf("directory failure omitted its canonicalization stage: %v", err)
	}
	// An invalid package role must not fall back to the readable source's
	// directory. The exact package-directory and source bindings are distinct.
	pkg.Dir = dir
	if _, err := ReadAssemblyFileWithIncludes(pkg, file, nil, "linux", "amd64", TranslateOptions{}); err != nil {
		t.Fatalf("valid original source/package directory rejected: %v", err)
	}
}

func TestAssemblyIncludesUseSelectedInputs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"sub/outer.h": "#include \"inner.h\"\n#define VALUE INNER\n",
		"sub/inner.h": "#define INNER 99\n", // nested search must not select this
		"inner.h":     "#define INNER 13\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pkg := mustTestPackage(t, "example.org/headers", "package headers")
	pkg.Dir = dir
	file := filepath.Join(dir, "source.s")
	source := []byte("#ifdef GOAMD64_v3\n#include \"sub/outer.h\"\n#else\n#include notQuoted missing\n#define LEAK 1\n#endif\n" +
		"#ifdef LEAK\nBAD\n#endif\nDATA value(SB)/4,$VALUE\n")
	overlay := map[string][]byte{file: source, filepath.Join(dir, "inner.h"): []byte("#define INNER 42\n")}
	got, err := ReadAssemblyFileWithIncludes(pkg, file, overlay, "linux", "amd64", TranslateOptions{
		AssemblyDefines: []string{"GOOS_linux", "GOARCH_amd64", "GOAMD64_v3"},
	})
	if err != nil || !strings.Contains(string(got), "$42") || strings.Contains(string(got), "BAD") {
		t.Fatalf("actual selected CPU/overlay/fixed search: %q, %v", got, err)
	}
	toolRoot := t.TempDir()
	toolInclude := filepath.Join(toolRoot, "pkg", "include")
	if err := os.MkdirAll(toolInclude, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolInclude, "selected.h"), []byte("#define VALUE 67\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = preprocessAssemblyForPkg(pkg, file, []byte("#include \"selected.h\"\nDATA value(SB)/4,$VALUE\n"), nil, "linux", "amd64", TranslateOptions{SourceGOROOT: toolRoot})
	if err != nil || !strings.Contains(string(got), "$67") {
		t.Fatalf("selected source toolchain rather than build toolchain: %q, %v", got, err)
	}
}

func TestAssemblyIncludesBindGeneratedHeader(t *testing.T) {
	pkg := mustTestPackage(t, "example.org/generated", `package generated
const K = 42
const Fraction = 1.25
type Frame struct { first byte; value uint64 }
type Generic[T any] struct { value T }
type Concrete = Generic[uint64]
`)
	pkg.Dir = t.TempDir()
	source := []byte("#include \"go_asm.h\"\n#ifdef const_K\n#ifdef Frame_value\n#ifdef Concrete_value\nDATA value(SB)/4,$const_K\n#endif\n#endif\n#endif\n" +
		"#ifdef const_Fraction\nBAD_FLOAT\n#endif\n#ifdef Generic__size\nBAD_GENERIC\n#endif\n")
	got, err := preprocessAssemblyForPkg(pkg, filepath.Join(pkg.Dir, "source.s"), source, nil, "linux", "amd64", TranslateOptions{})
	if err != nil || strings.TrimSpace(string(got)) != "DATA value(SB)/4,$42" {
		t.Fatalf("actual generated macro existence/layout: %q, %v", got, err)
	}
	pkg.Types = nil
	if _, err := preprocessAssemblyForPkg(pkg, filepath.Join(pkg.Dir, "source.s"), source, nil, "linux", "amd64", TranslateOptions{}); err == nil {
		t.Fatal("missing generated header binding accepted")
	}
}

func TestAssemblyIncludesRejectActiveMissingCycleAndEscape(t *testing.T) {
	pkg := mustTestPackage(t, "example.org/bounded", "package bounded")
	pkg.Dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(pkg.Dir, "cycle.h"), []byte("#include \"cycle.h\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	outFile := filepath.Join(outDir, "outside.h")
	if err := os.WriteFile(outFile, []byte("#define VALUE 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outFile, filepath.Join(pkg.Dir, "escaped.h")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ include, want string }{
		{"missing.h", "not found"}, {"cycle.h", "cycle"}, {"../outside.h", "escapes"}, {"escaped.h", "symlink"}, {outFile, "unsupported"},
	} {
		source := []byte("#include \"" + tc.include + "\"\nRET\n")
		_, err := preprocessAssemblyForPkg(pkg, filepath.Join(pkg.Dir, "source.s"), source, nil, "linux", "amd64", TranslateOptions{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("active include %q: %v; want %s", tc.include, err, tc.want)
		}
	}
	// Neither an inactive malformed operand nor its definitions cause any I/O
	// or require even a valid toolchain root.
	got, err := preprocessAssemblyForPkg(pkg, "source.s", []byte("#ifdef ABSENT\n#include unquoted\n#define LEAK 1\n#endif\n#ifdef LEAK\nBAD\n#endif\nRET\n"), nil, "linux", "amd64", TranslateOptions{SourceGOROOT: filepath.Join(pkg.Dir, "absent-toolchain")})
	if err != nil || strings.TrimSpace(string(got)) != "RET" {
		t.Fatalf("inactive headers/defines leaked: %q, %v", got, err)
	}
}
