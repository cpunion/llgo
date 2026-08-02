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

package debug

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/goplus/llgo/cmd/internal/flags"
	"github.com/goplus/llgo/internal/build"
	"github.com/goplus/llgo/internal/debugabi"
	"github.com/goplus/llgo/internal/optlevel"
	"github.com/goplus/llgo/internal/targets"
	"github.com/goplus/llgo/internal/wasmdebug"
)

func TestBackendRouting(t *testing.T) {
	tests := []struct {
		name   string
		conf   build.Config
		target *targets.Config
		want   backend
	}{
		{name: "native", conf: build.Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH}, want: backendLLDB},
		{name: "embedded", conf: build.Config{Target: "board"}, target: &targets.Config{LLVMTarget: "thumbv7m-none-eabi"}, want: backendGDB},
		{name: "WASI", conf: build.Config{Target: "wasip1"}, target: &targets.Config{GOOS: "wasip1", GOARCH: "wasm", LLVMTarget: "wasm32-unknown-wasi"}, want: backendWasmtime},
		{name: "browser", conf: build.Config{Target: "wasm"}, target: &targets.Config{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm32-unknown-wasi"}, want: backendBrowser},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind := classifyTarget(&test.conf, test.target)
			got, err := selectBackend(backendAuto, kind)
			if err != nil || got != test.want {
				t.Fatalf("selectBackend(auto) = (%q, %v), want (%q, nil)", got, err, test.want)
			}
		})
	}
	if _, err := selectBackend(backendGDB, targetWASI); err == nil {
		t.Fatal("GDB unexpectedly accepted a WASI target")
	}
	if err := (options{backend: "unknown"}).validate(); err == nil {
		t.Fatal("unknown backend was accepted")
	}
}

func TestSessionPlanning(t *testing.T) {
	remote, err := makeServerPlan(nil, "program", options{remote: ":1234"})
	if err != nil || remote.address != "127.0.0.1:1234" || len(remote.command) != 0 {
		t.Fatalf("remote plan = (%+v, %v)", remote, err)
	}

	openocd, err := makeServerPlan(&targets.Config{
		Name:             "board",
		OpenOCDInterface: "cmsis-dap",
		OpenOCDTransport: "swd",
		OpenOCDTarget:    "stm32f4x",
	}, "program.elf", options{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(openocd.command, " ")
	for _, want := range []string{"openocd", "gdb_port", "interface/cmsis-dap.cfg", "transport select swd", "target/stm32f4x.cfg"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("OpenOCD command %q does not contain %q", joined, want)
		}
	}
	if !openocd.load {
		t.Fatal("OpenOCD plan does not request image loading")
	}

	gdbArgs, err := debuggerArguments(backendGDB, "program.elf", []string{"--batch"}, openocd)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(gdbArgs, " ")
	for _, want := range []string{"target extended-remote", "monitor reset halt", " load ", "--batch"} {
		if !strings.Contains(" "+joined+" ", want) {
			t.Fatalf("GDB arguments %q do not contain %q", joined, want)
		}
	}
	if _, err := debuggerArguments(backendLLDB, "program.elf", nil, openocd); err == nil {
		t.Fatal("LLDB unexpectedly accepted automated OpenOCD loading")
	}
	command, err := parseServerCommand("server -kernel {} -port {debug-port}", filepath.Join("dir with space", "program.elf"), 4321)
	if err != nil || len(command) != 5 || command[2] != filepath.Join("dir with space", "program.elf") || command[4] != "4321" {
		t.Fatalf("parseServerCommand() = (%v, %v)", command, err)
	}

	lldbArgs, err := debuggerArguments(backendLLDB, `dir/program.elf`, []string{"--batch"}, remote)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(lldbArgs, " ")
	for _, want := range []string{"gdb-remote 127.0.0.1:1234", "target modules load", "--slide 0", "--batch"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("LLDB arguments %q do not contain %q", joined, want)
		}
	}
}

func TestGDBSessionStartsConfiguredServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	t.Setenv("LLGO_DEBUG_SERVER_HELPER", "1")
	capture := filepath.Join(t.TempDir(), "gdb-arguments")
	t.Setenv("LLGO_DEBUG_GDB_CAPTURE", capture)
	gdbPath := writeFakeGDB(t)
	template := fmt.Sprintf("%s -test.run=^TestDebugServerHelper$ -- {debug-port}", strconv.Quote(os.Args[0]))

	var stdout, stderr bytes.Buffer
	err := runSession(session{
		backend:  backendGDB,
		artifact: filepath.Join(t.TempDir(), "program.elf"),
		target: &targets.Config{
			Name:        "test-target",
			DebugServer: template,
			GDB:         []string{gdbPath},
		},
		options: options{backend: backendGDB},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runSession() error: %v; stderr=%s", err, stderr.String())
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	args := string(data)
	if !strings.Contains(args, "target remote 127.0.0.1:") || !strings.Contains(args, "program.elf") {
		t.Fatalf("GDB arguments do not contain the artifact and remote session: %q", args)
	}
}

func TestDebugServerFailureIncludesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	serverPath := filepath.Join(t.TempDir(), "failing-server")
	if err := os.WriteFile(serverPath, []byte("#!/bin/sh\necho 'server startup failed' >&2\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	port, err := freeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	_, err = startServer(serverPlan{
		command: []string{serverPath},
		address: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
	})
	if err == nil || !strings.Contains(err.Error(), "server startup failed") {
		t.Fatalf("startServer() error = %v", err)
	}
}

func TestDebugServerHelper(t *testing.T) {
	if os.Getenv("LLGO_DEBUG_SERVER_HELPER") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		t.Fatal("missing helper port")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Args[separator+1])
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		connection.Close()
	}
}

func TestArtifactAndArgumentHandling(t *testing.T) {
	command, debugger := splitDebuggerArgs([]string{"-target=board", ".", "--", "--batch", "-ex", "run"})
	if strings.Join(command, " ") != "-target=board ." || strings.Join(debugger, " ") != "--batch -ex run" {
		t.Fatalf("splitDebuggerArgs() = (%v, %v)", command, debugger)
	}

	conf := &build.Config{Target: "cortex-m-qemu", OutFile: filepath.Join(t.TempDir(), "firmware")}
	cleanup, artifact, err := prepareArtifact(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !strings.HasSuffix(artifact, "firmware.elf") || conf.OutFile != artifact || conf.AppExt != ".elf" {
		t.Fatalf("prepareArtifact() = %q, config=(%q, %q)", artifact, conf.OutFile, conf.AppExt)
	}
	for _, test := range []struct {
		conf build.Config
		want string
	}{
		{conf: build.Config{Goos: "windows"}, want: ".exe"},
		{conf: build.Config{Goos: "wasip1", Goarch: "wasm"}, want: ".wasm"},
		{conf: build.Config{Goos: "linux", Goarch: "amd64"}, want: ""},
	} {
		if got := debugArtifactExtension(&test.conf); got != test.want {
			t.Errorf("debugArtifactExtension(%+v) = %q, want %q", test.conf, got, test.want)
		}
	}
	if target, err := resolveTarget("cortex-m-qemu"); err != nil || target.DebugServer == "" {
		t.Fatalf("resolveTarget(cortex-m-qemu) = (%+v, %v)", target, err)
	}
}

func TestResolvedWasmTargetConfig(t *testing.T) {
	conf := &build.Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH, Target: "wasip1"}
	applyResolvedTarget(conf, &targets.Config{GOOS: "wasip1", GOARCH: "wasm"})
	if conf.Goos != "wasip1" || conf.Goarch != "wasm" || conf.Target != "" {
		t.Fatalf("resolved target config = %s/%s target=%q, want wasip1/wasm GOOS path", conf.Goos, conf.Goarch, conf.Target)
	}

	native := &build.Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH, Target: "board"}
	applyResolvedTarget(native, &targets.Config{GOOS: "none", GOARCH: "arm"})
	if native.Goos != runtime.GOOS || native.Goarch != runtime.GOARCH {
		t.Fatalf("non-Wasm target unexpectedly changed config to %s/%s", native.Goos, native.Goarch)
	}
}

func TestWASIDebugArtifactAndEnvironment(t *testing.T) {
	raw := wasiDebugFixture(t)
	artifact := filepath.Join(t.TempDir(), "program.wasm")
	if err := os.WriteFile(artifact, raw, 0600); err != nil {
		t.Fatal(err)
	}
	memory, err := validateWASIDebugArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if memory == nil || memory.Module != "env" || memory.Name != "memory" || memory.Minimum != 1024 || memory.Maximum != 1024 || !memory.HasMax || !memory.Shared {
		t.Fatalf("validated memory = %+v", memory)
	}

	environment, cleanup, err := writeWASIEnvironment(memory)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(environment)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`(memory (export "memory") 1024 1024 shared)`, `export "longjmp"`, `export "pthread_exit"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("environment WAT %q does not contain %q", string(data), want)
		}
	}
	cleanup()
	if _, err := os.Stat(environment); !os.IsNotExist(err) {
		t.Fatalf("environment cleanup error = %v", err)
	}
	withoutMemory := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	debug := appendTestName(nil, ".debug_info")
	debug = append(debug, 1)
	withoutMemory = appendTestSection(withoutMemory, 0, debug)
	withoutMemory, err = wasmdebug.SetDebuggerRecord(withoutMemory, debugabi.NewRecord(2, 4, debugabi.ByteOrderLittle))
	if err != nil {
		t.Fatal(err)
	}
	withoutMemoryPath := filepath.Join(t.TempDir(), "self-contained.wasm")
	if err := os.WriteFile(withoutMemoryPath, withoutMemory, 0600); err != nil {
		t.Fatal(err)
	}
	if memory, err := validateWASIDebugArtifact(withoutMemoryPath); err != nil || memory != nil {
		t.Fatalf("self-contained WASI artifact = (%+v, %v), want (nil, nil)", memory, err)
	}

	plan, planCleanup, err := makeWASIServerPlan(artifact, options{remote: ":1234"})
	if err != nil {
		t.Fatal(err)
	}
	defer planCleanup()
	if plan.address != "127.0.0.1:1234" || len(plan.command) != 0 {
		t.Fatalf("remote WASI plan = %+v", plan)
	}
	args, err := debuggerArguments(backendWasmtime, artifact, []string{"--batch"}, plan)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "process connect --plugin wasm connect://127.0.0.1:1234") || !strings.Contains(joined, "--batch") {
		t.Fatalf("Wasmtime LLDB arguments = %q", joined)
	}
}

func wasiDebugFixture(t *testing.T) []byte {
	t.Helper()
	imports := appendTestULEB(nil, 1)
	imports = appendTestName(imports, "env")
	imports = appendTestName(imports, "memory")
	imports = append(imports, 2)
	imports = appendTestULEB(imports, 3)
	imports = appendTestULEB(imports, 1024)
	imports = appendTestULEB(imports, 1024)
	raw := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	raw = appendTestSection(raw, 2, imports)
	debug := appendTestName(nil, ".debug_info")
	debug = append(debug, 1, 2, 3)
	raw = appendTestSection(raw, 0, debug)
	result, err := wasmdebug.SetDebuggerRecord(raw, debugabi.NewRecord(2, 4, debugabi.ByteOrderLittle))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func appendTestName(dst []byte, name string) []byte {
	dst = appendTestULEB(dst, uint32(len(name)))
	return append(dst, name...)
}

func appendTestSection(dst []byte, id byte, payload []byte) []byte {
	dst = append(dst, id)
	dst = appendTestULEB(dst, uint32(len(payload)))
	return append(dst, payload...)
}

func appendTestULEB(dst []byte, value uint32) []byte {
	for {
		current := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			current |= 0x80
		}
		dst = append(dst, current)
		if value == 0 {
			return dst
		}
	}
}

func TestRunBuildsAndLaunchesNativeDebugger(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLGO_ROOT", repoRoot)
	moduleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module debugcommandtest\n\ngo 1.20\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte("package main\nfunc main() { value := 42; println(value) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "lldb-arguments")
	t.Setenv("LLGO_DEBUG_LLDB_CAPTURE", capture)
	fakeLLDB := writeFakeLLDB(t)

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(moduleDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	flags.Target = ""
	flags.OutputFile = ""
	flags.OptLevel = optlevel.Unset
	flags.Tags = ""
	flags.Verbose = false
	flags.CompilerVerbose = false
	goBuildFlags.Args = nil
	var stdout, stderr bytes.Buffer
	if err := run(nil, []string{"--batch"}, options{backend: backendAuto, lldb: fakeLLDB}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run() error: %v; stderr=%s", err, stderr.String())
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 4 || lines[0] != "-o" || !strings.Contains(lines[1], "command script import") || lines[len(lines)-1] != "--batch" {
		t.Fatalf("LLDB arguments = %q", string(data))
	}
	artifact := lines[2]
	if !strings.Contains(artifact, "llgo-debug-") {
		t.Fatalf("LLDB artifact = %q, want temporary debug artifact", artifact)
	}
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Fatalf("temporary artifact still exists after debugger exit: %v", err)
	}

	if err := run([]string{".", "./other"}, nil, options{backend: backendAuto}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("multiple packages were accepted")
	}
	if err := run(nil, nil, options{backend: "invalid"}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("invalid backend was accepted")
	}
	goBuildFlags.Args = []string{"-ldflags=-s"}
	if err := run(nil, nil, options{backend: backendAuto}, strings.NewReader(""), &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "debug information is required") {
		t.Fatalf("run(-ldflags=-s) error = %v", err)
	}
	goBuildFlags.Args = nil
}

func writeFakeGDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gdb")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo 'GNU gdb (GDB) 15.1'
  exit 0
fi
if [ "$1" = "--batch" ] && [ "$2" = "--nx" ]; then
  exit 0
fi
printf '%s\n' "$@" > "$LLGO_DEBUG_GDB_CAPTURE"
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFakeLLDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lldb")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo 'lldb version 19.1.0'
  exit 0
fi
printf '%s\n' "$@" > "$LLGO_DEBUG_LLDB_CAPTURE"
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
