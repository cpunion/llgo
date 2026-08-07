//go:build !llgo

package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/goplus/llgo/internal/wasmdebug"
)

func TestFinalizeExternalWasmDWARF(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "main.c")
	module := filepath.Join(dir, "app.wasm")
	sidecar := filepath.Join(dir, "app debug.wasm")
	if err := os.WriteFile(source, []byte("int add(int a, int b) { return a + b; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(clang,
		"--target=wasm32-unknown-unknown", "-g", "-nostdlib",
		"-Wl,--no-entry", "-Wl,--export=add", "-o", module, source,
	).CombinedOutput(); err != nil {
		t.Fatalf("compile Wasm DWARF fixture: %v\n%s", err, out)
	}
	original, err := os.ReadFile(module)
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizeDebugArtifact(
		&Config{DebugArtifactMode: DebugArtifactExternal},
		&OutFmtDetails{Out: module, DWARF: sidecar},
		false,
	); err != nil {
		t.Fatal(err)
	}
	debugModule, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(debugModule, original) {
		t.Fatal("external DWARF sidecar differs from the linked debug module")
	}
	main, err := os.ReadFile(module)
	if err != nil {
		t.Fatal(err)
	}
	if has, err := wasmdebug.HasDWARF(main); err != nil || has {
		t.Fatalf("main module HasDWARF = %v, %v", has, err)
	}
	if has, err := wasmdebug.HasDWARF(debugModule); err != nil || !has {
		t.Fatalf("sidecar HasDWARF = %v, %v", has, err)
	}
	url, ok, err := wasmdebug.ExternalURL(main)
	if err != nil || !ok || url != "app%20debug.wasm" {
		t.Fatalf("main external URL = %q, %v, %v", url, ok, err)
	}
}

func TestFinalizeDebugArtifactValidation(t *testing.T) {
	if err := finalizeDebugArtifact(nil, nil, false); err != nil {
		t.Fatalf("nil configuration: %v", err)
	}
	if err := finalizeDebugArtifact(&Config{}, &OutFmtDetails{}, false); err != nil {
		t.Fatalf("non-external mode: %v", err)
	}
	if err := finalizeDebugArtifact(&Config{DebugArtifactMode: DebugArtifactExternal}, &OutFmtDetails{}, false); err == nil {
		t.Fatal("external mode accepted an empty executable path")
	}
}

func TestFinalizeDebugArtifactRemovesStaleSidecar(t *testing.T) {
	module := filepath.Join(t.TempDir(), "app.wasm")
	sidecar := dwarfSidecarPath(module)
	if err := os.WriteFile(sidecar, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := finalizeDebugArtifact(
		&Config{DebugArtifactMode: DebugArtifactEmbedded},
		&OutFmtDetails{Out: module},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar still exists (stat error %v)", err)
	}
}
