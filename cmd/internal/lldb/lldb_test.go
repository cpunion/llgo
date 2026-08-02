//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package lldb

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/goplus/llgo/cmd/internal/base"
	"github.com/goplus/llgo/internal/debugabi"
	"github.com/goplus/llgo/internal/mockable"
	"github.com/goplus/llgo/internal/wasmdebug"
)

func TestParseLLDBVersion(t *testing.T) {
	tests := []struct {
		version string
		want    lldbVersion
		ok      bool
	}{
		{"lldb version 18.1.8", lldbVersion{major: 18}, true},
		{"lldb-1703.0.236.21\nApple Swift version 6.2", lldbVersion{major: 1703, apple: true}, true},
		{"LLDB version 21.0.0git", lldbVersion{major: 21}, true},
		{"clang version 21.0.0", lldbVersion{}, false},
	}
	for _, test := range tests {
		got, ok := parseLLDBVersion(test.version)
		if got != test.want || ok != test.ok {
			t.Errorf("parseLLDBVersion(%q) = (%+v, %v), want (%+v, %v)", test.version, got, ok, test.want, test.ok)
		}
	}
}

func TestLLDBImportCommandEscapesPath(t *testing.T) {
	got := lldbImportCommand(`a\b"c.py`)
	want := `command script import "a\\b\"c.py"`
	if got != want {
		t.Fatalf("lldbImportCommand() = %q, want %q", got, want)
	}
}

func TestValidateLLDB(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	newLLDB := writeFakeLLDB(t, "lldb version 18.1.8", "")
	if got, err := validateLLDB(newLLDB); err != nil || got != newLLDB {
		t.Fatalf("validateLLDB() = (%q, %v), want (%q, nil)", got, err, newLLDB)
	}

	appleLLDB := writeFakeLLDB(t, "lldb-1703.0.236.21\nApple Swift version 6.2", "")
	if got, err := validateLLDB(appleLLDB); err != nil || got != appleLLDB {
		t.Fatalf("validateLLDB(Apple) = (%q, %v), want (%q, nil)", got, err, appleLLDB)
	}

	oldLLDB := writeFakeLLDB(t, "lldb version 17.0.6", "")
	if _, err := validateLLDB(oldLLDB); err == nil || !strings.Contains(err.Error(), "version 18 or newer") {
		t.Fatalf("validateLLDB(old) error = %v", err)
	}

	unparseable := writeFakeLLDB(t, "clang version 18.1.8", "")
	if _, err := validateLLDB(unparseable); err == nil || !strings.Contains(err.Error(), "cannot parse") {
		t.Fatalf("validateLLDB(unparseable) error = %v", err)
	}

	if _, err := validateLLDB(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "find") {
		t.Fatalf("validateLLDB(missing) error = %v", err)
	}
}

func TestFindLLDBPrecedenceAndFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	newLLDB := writeFakeLLDB(t, "lldb version 18.1.8", "")
	oldLLDB := writeFakeLLDB(t, "lldb version 17.0.6", "")

	if got, err := findLLDBFrom(newLLDB, oldLLDB, nil); err != nil || got != newLLDB {
		t.Fatalf("configured findLLDBFrom() = (%q, %v)", got, err)
	}
	if got, err := findLLDBFrom("", newLLDB, nil); err != nil || got != newLLDB {
		t.Fatalf("environment findLLDBFrom() = (%q, %v)", got, err)
	}
	if got, err := findLLDBFrom("", "", []string{oldLLDB, newLLDB, newLLDB}); err != nil || got != newLLDB {
		t.Fatalf("fallback findLLDBFrom() = (%q, %v)", got, err)
	}
	if _, err := findLLDBFrom("", "", []string{oldLLDB, filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("findLLDBFrom() succeeded without a supported LLDB")
	}
}

func TestFindWasmLLDBCapabilities(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	wasmWithScript := writeFakeWasmLLDB(t, "lldb version 22.1.0", true, true, "")
	wasmWithoutScript := writeFakeWasmLLDB(t, "lldb version 22.1.0-wasi-sdk", true, false, "")
	withoutWasm := writeFakeWasmLLDB(t, "lldb version 22.1.0", false, true, "")
	old := writeFakeWasmLLDB(t, "lldb version 21.1.0", true, true, "")

	path, capabilities, err := findWasmLLDBFrom(wasmWithScript, "", nil)
	if err != nil || path != wasmWithScript || !capabilities.wasm || !capabilities.scripting {
		t.Fatalf("scripted Wasm LLDB = (%q, %+v, %v)", path, capabilities, err)
	}
	path, capabilities, err = findWasmLLDBFrom("", wasmWithoutScript, nil)
	if err != nil || path != wasmWithoutScript || !capabilities.wasm || capabilities.scripting {
		t.Fatalf("non-scripted Wasm LLDB = (%q, %+v, %v)", path, capabilities, err)
	}
	if _, _, err := findWasmLLDBFrom(withoutWasm, "", nil); err == nil || !strings.Contains(err.Error(), "WebAssembly process plugin") {
		t.Fatalf("non-Wasm LLDB error = %v", err)
	}
	if _, _, err := findWasmLLDBFrom(old, "", nil); err == nil || !strings.Contains(err.Error(), "version 22 or newer") {
		t.Fatalf("old Wasm LLDB error = %v", err)
	}
	if !hasWasmProcessPlugin("process\n  [+] wasm  WebAssembly process\nplatform\n") {
		t.Fatal("hasWasmProcessPlugin did not recognize the process plugin")
	}
	if hasWasmProcessPlugin("object-file\n  [+] wasm  WebAssembly object file\n") {
		t.Fatal("hasWasmProcessPlugin confused the object-file plugin with the process plugin")
	}
}

func TestRunWasmAdapterDowngrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	capture := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("LLGO_LLDB_TEST_CAPTURE", capture)
	fake := writeFakeWasmLLDB(t, "lldb version 22.1.0-wasi-sdk", true, false,
		`printf '%s\n' "$@" > "$LLGO_LLDB_TEST_CAPTURE"`)

	var stdout, stderr bytes.Buffer
	if err := RunWasm(fake, []string{"program.wasm", "-o", "process connect --plugin wasm connect://127.0.0.1:1234"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "command script import") {
		t.Fatalf("non-scripted LLDB arguments unexpectedly import the adapter: %q", string(data))
	}
	if !strings.Contains(stderr.String(), "runtime presentation is disabled") {
		t.Fatalf("downgrade warning = %q", stderr.String())
	}
}

func TestRunImportsEmbeddedPluginAndPassesArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	capture := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("LLGO_LLDB_TEST_CAPTURE", capture)
	fake := writeFakeLLDB(t, "lldb version 18.1.8", `
printf '%s\n' "$@" > "$LLGO_LLDB_TEST_CAPTURE"
plugin=$(printf '%s\n' "$2" | sed 's/^command script import "//; s/"$//')
test -s "$plugin"
schema=$(dirname "$plugin")/llgo_debugger_schema_v1.json
test -s "$schema"
grep -q '"contract": "llgo.debugger"' "$schema"
grep -q __llgo_debugger_marker_v1 "$schema"
`)

	var stdout, stderr bytes.Buffer
	if err := run(fake, []string{"--batch", "./program", "-o", "run"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("%v: %s", err, stderr.String())
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "-o\ncommand script import \"") {
		t.Fatalf("LLDB arguments %q do not import the plugin after target creation", got)
	}
	for _, want := range []string{"--batch\n", "./program\n", "-o\n", "run\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("LLDB arguments %q do not contain %q", got, want)
		}
	}
}

func TestRunRequiresExecutable(t *testing.T) {
	err := run("lldb", nil, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "llgo lldb: no executable specified" {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunCmdParseErrorExits(t *testing.T) {
	var cmd base.Command
	mockable.EnableMock()
	defer mockable.DisableMock()

	exited := false
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == "exit" {
					exited = true
					return
				}
				panic(recovered)
			}
		}()
		runCmd(&cmd, []string{"--batch", "./program"})
	}()
	if !exited || mockable.ExitCode() != 2 {
		t.Fatalf("runCmd parse error exit = (%v, %d), want (true, 2)", exited, mockable.ExitCode())
	}
}

func TestEmbeddedPluginIdentity(t *testing.T) {
	source := string(pluginSource)
	for _, want := range []string{
		"__lldb_init_module",
		"LLGO_DEBUGGER_MARKER_PREFIX",
		"is_llgo_compiler",
		"inspect_target",
		"LLGO_DEBUGGER_SCHEMAS",
		"LLGO_RUNTIME_LAYOUTS",
		"LLGO_DEBUGGER_RECORD_SYMBOL",
		"llgo_debugger_schema_v1.json",
		"string_summary",
		"slice_summary",
		"SliceSyntheticProvider",
		"interface_summary",
		"function_summary",
		"map_summary",
		"MapSyntheticProvider",
		"channel_summary",
		"ChannelSyntheticProvider",
		"LLGO_GOROUTINE_LAYOUTS",
		"print_goroutines",
		"print_goroutine",
		"llgo status",
		"llgo print",
		"llgo vars",
		"llgo goroutines",
		"llgo goroutine",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("embedded plugin is missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"llgo_plugin.print_go_expression p'",
		"llgo_plugin.print_all_variables v'",
	} {
		if strings.Contains(source, unwanted) {
			t.Errorf("embedded plugin overrides stock LLDB command in %q", unwanted)
		}
	}
}

func TestEmbeddedPluginReadsWasmDebuggerRecord(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "llgo_plugin.py")
	if err := os.WriteFile(pluginPath, pluginSource, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, debuggerSchemaFilename), debugabi.SchemaV1(), 0600); err != nil {
		t.Fatal(err)
	}
	record := debugabi.NewRecord(2, 4, debugabi.ByteOrderLittle)
	recordBytes, err := record.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	module, err := wasmdebug.SetDebuggerRecord([]byte{0, 'a', 's', 'm', 1, 0, 0, 0}, record)
	if err != nil {
		t.Fatal(err)
	}
	modulePath := filepath.Join(dir, "program.wasm")
	if err := os.WriteFile(modulePath, module, 0600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import importlib.util
from pathlib import Path
import sys
import types
sys.modules["lldb"] = types.ModuleType("lldb")
spec = importlib.util.spec_from_file_location("llgo_plugin", %q)
plugin = importlib.util.module_from_spec(spec)
sys.modules["llgo_plugin"] = plugin
spec.loader.exec_module(plugin)
assert plugin._wasm_debugger_records(Path(%q)) == [bytes.fromhex(%q)]
`, pluginPath, modulePath, fmt.Sprintf("%x", recordBytes))
	if output, err := exec.Command(python, "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("Wasm record parser failed: %v\n%s", err, output)
	}
}

func writeFakeLLDB(t *testing.T, version, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lldb")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n" +
		"  printf '%s\\n' '" + version + "'\n" +
		"  exit 0\n" +
		"fi\n" + body
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFakeWasmLLDB(t *testing.T, version string, wasm, scripting bool, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lldb")
	wasmPlugin := ""
	if wasm {
		wasmPlugin = "  [+] wasm  GDB Remote protocol based WebAssembly debugging plug-in."
	}
	scriptStatus := "exit 1"
	if scripting {
		scriptStatus = "echo LLGO_SCRIPT_OK; exit 0"
	}
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf '%s\n' '` + version + `'
  exit 0
fi
if [ "$1" = "--batch" ] && [ "$2" = "-o" ] && [ "$3" = "plugin list" ]; then
  echo process
  echo '` + wasmPlugin + `'
  echo platform
  exit 0
fi
if [ "$1" = "--batch" ] && [ "$2" = "-o" ]; then
  case "$3" in
    script*) ` + scriptStatus + ` ;;
  esac
fi
` + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
