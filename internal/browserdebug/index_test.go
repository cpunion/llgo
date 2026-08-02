//go:build !llgo

package browserdebug

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/debugabi"
	"github.com/goplus/llgo/internal/wasmdebug"
)

func TestLoadEmbeddedAndExternal(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "local-source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "fixture.c")
	if err := os.WriteFile(source, []byte("int add(int a, int b) { int result = a + b; return result; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	embedded := filepath.Join(dir, "fixture.wasm")
	compileWasmFixture(t, source, embedded)
	raw, err := os.ReadFile(embedded)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = wasmdebug.SetDebuggerRecord(raw, debugabi.NewRecord(2, 4, debugabi.ByteOrderLittle))
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err = wasmdebug.EnsureBuildID(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(embedded, raw, 0o755); err != nil {
		t.Fatal(err)
	}

	bundle, err := Load(embedded, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Index.Artifact != "embedded" || bundle.Index.Record.SchemaVersion != 1 || bundle.Index.BuildID == "" {
		t.Fatalf("embedded index header = %+v", bundle.Index)
	}
	if !hasSourceSuffix(bundle.Index.Sources, "fixture.c") || !hasFunction(bundle.Index.Functions, "add") || len(bundle.Index.Lines) == 0 {
		t.Fatalf("embedded index lacks source/function/lines: sources=%+v functions=%+v lines=%d diagnostics=%v",
			bundle.Index.Sources, bundle.Index.Functions, len(bundle.Index.Lines), bundle.Index.Diagnostics)
	}

	sidecar := filepath.Join(dir, "fixture debug.wasm")
	if err := os.WriteFile(sidecar, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	main, err := wasmdebug.Externalize(raw, "fixture%20debug.wasm")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(embedded, main, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle, err = Load(embedded, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Index.Artifact != "external" || bundle.SymbolsPath != sidecar {
		t.Fatalf("external bundle = mode %q symbols %q", bundle.Index.Artifact, bundle.SymbolsPath)
	}

	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	_, err = Load(embedded, nil)
	var missing *MissingSymbolsError
	if !errors.As(err, &missing) || missing.URL != "fixture%20debug.wasm" {
		t.Fatalf("missing sidecar error = %T %v", err, err)
	}

	stale, err := wasmdebug.SetBuildID(raw, bytes.Repeat([]byte{0xaa}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(embedded, nil); err == nil || !strings.Contains(err.Error(), "stale external") {
		t.Fatalf("stale sidecar error = %v", err)
	}
}

func TestPathMapping(t *testing.T) {
	mapping, err := ParsePathMapping("/build/source=/local/source")
	if err != nil {
		t.Fatal(err)
	}
	if suffix, ok := pathPrefix(filepath.Join("/build/source", "pkg/main.go"), mapping.From); !ok || suffix != filepath.Join("pkg", "main.go") {
		t.Fatalf("pathPrefix = %q, %v", suffix, ok)
	}
	if _, err := ParsePathMapping("missing-separator"); err == nil {
		t.Fatal("invalid source mapping was accepted")
	}
}

func TestLoadUsesLongestSourcePathMapping(t *testing.T) {
	dir := t.TempDir()
	recordedRoot := filepath.Join(dir, "recorded")
	recordedPackage := filepath.Join(recordedRoot, "pkg")
	if err := os.MkdirAll(recordedPackage, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(recordedPackage, "fixture.c")
	artifact := filepath.Join(dir, "fixture.wasm")
	if err := os.WriteFile(source, []byte("int add(int a, int b) { return a + b; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	compileWasmFixture(t, source, artifact)
	raw, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = wasmdebug.SetDebuggerRecord(raw, debugabi.NewRecord(2, 4, debugabi.ByteOrderLittle))
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err = wasmdebug.EnsureBuildID(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, raw, 0o755); err != nil {
		t.Fatal(err)
	}

	relocatedRoot := filepath.Join(dir, "relocated")
	if err := os.Rename(recordedRoot, relocatedRoot); err != nil {
		t.Fatal(err)
	}
	bundle, err := Load(artifact, []PathMapping{
		{From: recordedRoot, To: filepath.Join(dir, "wrong")},
		{From: recordedPackage, To: filepath.Join(relocatedRoot, "pkg")},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSource := filepath.Join(relocatedRoot, "pkg", "fixture.c")
	for _, indexed := range bundle.Index.Sources {
		if indexed.Path != source {
			continue
		}
		if !indexed.Local || bundle.SourceFiles[indexed.ID] != wantSource {
			t.Fatalf("mapped source = %+v, file %q, want %q", indexed, bundle.SourceFiles[indexed.ID], wantSource)
		}
		return
	}
	t.Fatalf("recorded source %q is absent from %+v", source, bundle.Index.Sources)
}

func TestLoadLLGoArtifact(t *testing.T) {
	path := os.Getenv("LLGO_BROWSER_DEBUG_ARTIFACT")
	if path == "" {
		t.Skip("LLGO_BROWSER_DEBUG_ARTIFACT is unset")
	}
	bundle, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sources=%d lines=%d functions=%d variables=%d types=%d diagnostics=%d",
		len(bundle.Index.Sources), len(bundle.Index.Lines), len(bundle.Index.Functions),
		len(bundle.Index.Variables), len(bundle.Index.Types), len(bundle.Index.Diagnostics))
	if !hasFunction(bundle.Index.Functions, "main.main") {
		t.Fatalf("LLGo index does not contain main.main")
	}
}

func TestLoadLLGoRuntimeFixture(t *testing.T) {
	path := os.Getenv("LLGO_BROWSER_DEBUG_RUNTIME_ARTIFACT")
	if path == "" {
		t.Skip("LLGO_BROWSER_DEBUG_RUNTIME_ARTIFACT is unset")
	}
	bundle, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"text", "values", "mapping", "queue", "greeter", "closure"} {
		if !hasVariable(bundle.Index.Variables, name) {
			t.Errorf("LLGo browser runtime fixture does not contain variable %q", name)
		}
	}
	for _, pattern := range []string{"string", "[]", "map[", "chan ", "interface{"} {
		if !hasTypePattern(bundle.Index.Types, pattern) {
			t.Errorf("LLGo browser runtime fixture does not contain a type matching %q", pattern)
		}
	}
}

func compileWasmFixture(t *testing.T, source, output string) {
	t.Helper()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	command := exec.Command(clang,
		"--target=wasm32-unknown-unknown", "-O0", "-g", "-nostdlib",
		"-Wl,--no-entry", "-Wl,--export=add", "-o", output, source,
	)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile WebAssembly fixture on %s/%s: %v\n%s", runtime.GOOS, runtime.GOARCH, err, data)
	}
}

func hasSourceSuffix(sources []Source, suffix string) bool {
	for _, source := range sources {
		if strings.HasSuffix(source.Path, suffix) {
			return true
		}
	}
	return false
}

func hasFunction(functions []Function, name string) bool {
	for _, function := range functions {
		if function.Name == name {
			return true
		}
	}
	return false
}

func hasVariable(variables []Variable, name string) bool {
	for _, variable := range variables {
		if variable.Name == name {
			return true
		}
	}
	return false
}

func hasTypePattern(types []Type, pattern string) bool {
	for _, item := range types {
		if strings.Contains(item.Name, pattern) {
			return true
		}
	}
	return false
}
