//go:build !llgo

package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/goplus/llgo/internal/debugabi"
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
		&Config{Goarch: "wasm", AbiMode: 2, DebugArtifactMode: DebugArtifactExternal},
		&OutFmtDetails{Out: module, DWARF: sidecar},
		false,
	); err != nil {
		t.Fatal(err)
	}
	debugModule, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	wantRecord := debugabi.NewRecord(2, 4, debugabi.ByteOrderLittle)
	wantDebugModule, err := wasmdebug.SetDebuggerRecord(original, wantRecord)
	if err != nil {
		t.Fatal(err)
	}
	wantDebugModule, _, err = wasmdebug.EnsureBuildID(wantDebugModule)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(debugModule, wantDebugModule) {
		t.Fatal("external DWARF sidecar differs from the recorded debug module")
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
	for name, contents := range map[string][]byte{"main": main, "sidecar": debugModule} {
		if got, ok, err := wasmdebug.DebuggerRecord(contents); err != nil || !ok || got != wantRecord {
			t.Fatalf("%s DebuggerRecord = %+v, %v, %v", name, got, ok, err)
		}
	}
	mainID, mainHasID, err := wasmdebug.BuildID(main)
	if err != nil || !mainHasID {
		t.Fatalf("main BuildID = %x, %v, %v", mainID, mainHasID, err)
	}
	sidecarID, sidecarHasID, err := wasmdebug.BuildID(debugModule)
	if err != nil || !sidecarHasID || !bytes.Equal(mainID, sidecarID) {
		t.Fatalf("sidecar BuildID = %x, %v, %v; main = %x", sidecarID, sidecarHasID, err, mainID)
	}

	embedded := filepath.Join(dir, "embedded.wasm")
	if err := os.WriteFile(embedded, original, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := finalizeDebugArtifact(
		&Config{Goarch: "wasm", AbiMode: 1, DebugArtifactMode: DebugArtifactEmbedded},
		&OutFmtDetails{Out: embedded},
		false,
	); err != nil {
		t.Fatal(err)
	}
	embeddedModule, err := os.ReadFile(embedded)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := wasmdebug.DebuggerRecord(embeddedModule); err != nil || !ok || got.CABIMode != 1 {
		t.Fatalf("embedded DebuggerRecord = %+v, %v, %v", got, ok, err)
	}
	if id, ok, err := wasmdebug.BuildID(embeddedModule); err != nil || !ok || len(id) == 0 {
		t.Fatalf("embedded BuildID = %x, %v, %v", id, ok, err)
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
	if err := finalizeDebugArtifact(
		&Config{DebugArtifactMode: DebugArtifactExternal},
		&OutFmtDetails{Out: "app.wasm"}, false,
	); err == nil {
		t.Fatal("external mode accepted an empty sidecar path")
	}
	if err := finalizeDebugArtifact(
		&Config{Goarch: "wasm", DebugArtifactMode: DebugArtifactEmbedded},
		&OutFmtDetails{}, false,
	); err == nil {
		t.Fatal("embedded Wasm mode accepted an empty executable path")
	}
	missing := filepath.Join(t.TempDir(), "missing.wasm")
	if err := finalizeDebugArtifact(
		&Config{Goarch: "wasm", DebugArtifactMode: DebugArtifactEmbedded},
		&OutFmtDetails{Out: missing}, false,
	); err == nil {
		t.Fatal("embedded Wasm mode accepted a missing executable")
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
