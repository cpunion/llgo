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
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/goplus/llgo/cmd/internal/gdb"
	"github.com/goplus/llgo/cmd/internal/lldb"
	wasmtimetool "github.com/goplus/llgo/cmd/internal/wasmtime"
	"github.com/goplus/llgo/internal/build"
	"github.com/goplus/llgo/internal/env"
	"github.com/goplus/llgo/internal/shellparse"
	"github.com/goplus/llgo/internal/targets"
)

type backend string

const (
	backendAuto     backend = "auto"
	backendLLDB     backend = "lldb"
	backendGDB      backend = "gdb"
	backendWasmtime backend = "wasmtime"
	backendBrowser  backend = "browser"
)

type targetKind uint8

const (
	targetNative targetKind = iota
	targetEmbedded
	targetWASI
	targetBrowser
)

type options struct {
	backend  backend
	lldb     string
	gdb      string
	wasmtime string
	remote   string
	server   string
}

func (o options) validate() error {
	switch o.backend {
	case backendAuto, backendLLDB, backendGDB, backendWasmtime, backendBrowser:
		return nil
	default:
		return fmt.Errorf("llgo debug: unknown backend %q; use auto, lldb, gdb, wasmtime, or browser", o.backend)
	}
}

func classifyTarget(conf *build.Config, target *targets.Config) targetKind {
	goos, goarch, llvmTarget := conf.Goos, conf.Goarch, ""
	if target != nil {
		goos, goarch, llvmTarget = target.GOOS, target.GOARCH, target.LLVMTarget
	}
	if goarch == "wasm" || strings.HasPrefix(llvmTarget, "wasm") {
		if goos == "js" || strings.HasPrefix(conf.Target, "wasm") {
			return targetBrowser
		}
		return targetWASI
	}
	if target != nil {
		return targetEmbedded
	}
	return targetNative
}

func selectBackend(requested backend, kind targetKind) (backend, error) {
	if requested != backendAuto {
		switch kind {
		case targetWASI:
			if requested != backendWasmtime {
				return "", fmt.Errorf("llgo debug: backend %s cannot debug a WASI target; use wasmtime", requested)
			}
		case targetBrowser:
			if requested != backendBrowser {
				return "", fmt.Errorf("llgo debug: backend %s cannot debug a browser target; use browser", requested)
			}
		default:
			if requested != backendLLDB && requested != backendGDB {
				return "", fmt.Errorf("llgo debug: backend %s cannot debug this target", requested)
			}
		}
		return requested, nil
	}
	switch kind {
	case targetEmbedded:
		return backendGDB, nil
	case targetWASI:
		return backendWasmtime, nil
	case targetBrowser:
		return backendBrowser, nil
	default:
		return backendLLDB, nil
	}
}

type session struct {
	backend      backend
	artifact     string
	debuggerArgs []string
	target       *targets.Config
	options      options
}

func runSession(s session, stdin io.Reader, stdout, stderr io.Writer) error {
	cleanup := func() {}
	var plan *serverPlan
	var err error
	if s.backend == backendWasmtime {
		plan, cleanup, err = makeWASIServerPlan(s.artifact, s.options)
	} else {
		plan, err = makeServerPlan(s.target, s.artifact, s.options)
	}
	if err != nil {
		return err
	}
	defer cleanup()
	args, err := debuggerArguments(s.backend, s.artifact, s.debuggerArgs, plan)
	if err != nil {
		return err
	}
	var server *debugServer
	if plan != nil {
		server, err = startServer(*plan)
		if err != nil {
			return err
		}
		defer server.stop()
	}

	var debugErr error
	switch s.backend {
	case backendLLDB:
		if err := lldb.Run(s.options.lldb, args, stdin, stdout, stderr); err != nil {
			debugErr = fmt.Errorf("llgo debug: %w", err)
		}
	case backendGDB:
		var candidates []string
		if s.target != nil {
			candidates = s.target.GDB
		}
		if err := gdb.Run(s.options.gdb, candidates, args, stdin, stdout, stderr); err != nil {
			debugErr = err
		}
	case backendWasmtime:
		if err := lldb.RunWasm(s.options.lldb, args, stdin, stdout, stderr); err != nil {
			debugErr = fmt.Errorf("llgo debug: %w", err)
		}
	default:
		debugErr = fmt.Errorf("llgo debug: backend %s is not implemented", s.backend)
	}
	if debugErr != nil && server != nil {
		if output := server.logSuffix(); output != "" {
			return fmt.Errorf("%w\ndebug server output:%s", debugErr, output)
		}
	}
	return debugErr
}

type serverPlan struct {
	command  []string
	address  string
	load     bool
	readyLog string
}

func makeWASIServerPlan(artifact string, opts options) (*serverPlan, func(), error) {
	memory, err := validateWASIDebugArtifact(artifact)
	if err != nil {
		return nil, func() {}, err
	}
	if opts.remote != "" {
		if opts.server != "" {
			return nil, func() {}, errors.New("llgo debug: -remote and -server are mutually exclusive")
		}
		return &serverPlan{address: normalizeRemoteAddress(opts.remote)}, func() {}, nil
	}

	port, err := freeTCPPort()
	if err != nil {
		return nil, func() {}, fmt.Errorf("llgo debug: allocate Wasmtime guest-debug port: %w", err)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if opts.server != "" {
		command, err := parseServerCommand(opts.server, artifact, port)
		if err != nil {
			return nil, func() {}, err
		}
		return &serverPlan{command: command, address: address}, func() {}, nil
	}

	wasmtimePath, err := wasmtimetool.Find(opts.wasmtime)
	if err != nil {
		return nil, func() {}, err
	}
	environment, cleanup, err := writeWASIEnvironment(memory)
	if err != nil {
		return nil, func() {}, err
	}
	command := []string{
		wasmtimePath,
		"run",
		"-g", strconv.Itoa(port),
		"-W", "threads=y,shared-memory=y",
		"--preload", "env=" + environment,
		artifact,
	}
	return &serverPlan{
		command:  command,
		address:  address,
		readyLog: "Debugger listening on",
	}, cleanup, nil
}

func makeServerPlan(target *targets.Config, artifact string, opts options) (*serverPlan, error) {
	if target == nil {
		if opts.server != "" {
			return nil, errors.New("llgo debug: -server requires -target")
		}
		if opts.remote == "" {
			return nil, nil
		}
		return &serverPlan{address: normalizeRemoteAddress(opts.remote)}, nil
	}
	if opts.remote != "" {
		if opts.server != "" {
			return nil, errors.New("llgo debug: -remote and -server are mutually exclusive")
		}
		return &serverPlan{address: normalizeRemoteAddress(opts.remote)}, nil
	}

	port, err := freeTCPPort()
	if err != nil {
		return nil, fmt.Errorf("llgo debug: allocate debug-server port: %w", err)
	}
	serverTemplate := opts.server
	if serverTemplate == "" {
		serverTemplate = target.DebugServer
	}
	if serverTemplate != "" {
		command, err := parseServerCommand(serverTemplate, artifact, port)
		if err != nil {
			return nil, err
		}
		return &serverPlan{command: command, address: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}, nil
	}
	if target.OpenOCDInterface == "" && target.OpenOCDTarget == "" {
		return nil, fmt.Errorf("llgo debug: target %s has no debug server; use -remote or -server", target.Name)
	}
	command := []string{"openocd", "-c", fmt.Sprintf("gdb_port %d", port)}
	if target.OpenOCDInterface != "" {
		command = append(command, "-f", "interface/"+target.OpenOCDInterface+".cfg")
	}
	if target.OpenOCDTransport != "" {
		command = append(command, "-c", "transport select "+target.OpenOCDTransport)
	}
	if target.OpenOCDTarget != "" {
		command = append(command, "-f", "target/"+target.OpenOCDTarget+".cfg")
	}
	return &serverPlan{
		command: command,
		address: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		load:    true,
	}, nil
}

func normalizeRemoteAddress(address string) string {
	address = strings.TrimPrefix(address, "tcp://")
	if strings.HasPrefix(address, ":") {
		return "127.0.0.1" + address
	}
	return address
}

func parseServerCommand(template, artifact string, port int) ([]string, error) {
	replacer := strings.NewReplacer(
		"{debug-port}", strconv.Itoa(port),
		"{root}", quoteServerArgument(env.LLGoROOT()),
		"{tmpDir}", quoteServerArgument(os.TempDir()),
		"{elf}", quoteServerArgument(artifact),
		"{}", quoteServerArgument(artifact),
	)
	command, err := shellparse.Parse(replacer.Replace(template))
	if err != nil {
		return nil, fmt.Errorf("llgo debug: parse debug-server command: %w", err)
	}
	if len(command) == 0 {
		return nil, errors.New("llgo debug: debug-server command is empty")
	}
	return command, nil
}

func quoteServerArgument(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func freeTCPPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

type debugServer struct {
	cmd      *exec.Cmd
	done     <-chan error
	finished bool
	log      *os.File
	logPath  string
}

func startServer(plan serverPlan) (*debugServer, error) {
	if len(plan.command) == 0 {
		return nil, nil
	}
	log, err := os.CreateTemp("", "llgo-debug-server-*.log")
	if err != nil {
		return nil, fmt.Errorf("llgo debug: create debug-server log: %w", err)
	}
	command := exec.Command(plan.command[0], plan.command[1:]...)
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		log.Close()
		os.Remove(log.Name())
		return nil, fmt.Errorf("llgo debug: start debug server %q: %w", plan.command[0], err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	server := &debugServer{cmd: command, done: done, log: log, logPath: log.Name()}
	if err := server.waitReady(plan, 10*time.Second); err != nil {
		server.stop()
		return nil, err
	}
	return server, nil
}

func (s *debugServer) waitReady(plan serverPlan, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-s.done:
			s.finished = true
			return fmt.Errorf("llgo debug: debug server exited before listening at %s: %v%s", plan.address, err, s.logSuffix())
		default:
		}
		if plan.readyLog != "" {
			data, _ := os.ReadFile(s.logPath)
			if strings.Contains(string(data), plan.readyLog) {
				return nil
			}
		} else {
			connection, err := net.DialTimeout("tcp", plan.address, 100*time.Millisecond)
			if err == nil {
				connection.Close()
				time.Sleep(50 * time.Millisecond)
				return nil
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("llgo debug: timed out waiting for debug server at %s%s", plan.address, s.logSuffix())
}

func (s *debugServer) stop() {
	if s == nil {
		return
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if !s.finished {
		select {
		case <-s.done:
			s.finished = true
		case <-time.After(2 * time.Second):
		}
	}
	if s.log != nil {
		_ = s.log.Close()
	}
	if s.logPath != "" {
		_ = os.Remove(s.logPath)
	}
}

func (s *debugServer) logSuffix() string {
	if s.log != nil {
		_ = s.log.Sync()
	}
	data, err := os.ReadFile(s.logPath)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return ""
	}
	return "\n" + strings.TrimSpace(string(data))
}

func debuggerArguments(selected backend, artifact string, extra []string, server *serverPlan) ([]string, error) {
	if server == nil {
		return append([]string{artifact}, extra...), nil
	}
	if server.address == "" {
		return nil, errors.New("llgo debug: remote debug-server address is empty")
	}
	switch selected {
	case backendGDB:
		args := []string{"--quiet", artifact}
		remoteCommand := "target remote " + server.address
		if server.load {
			remoteCommand = "target extended-remote " + server.address
		}
		args = append(args, "-ex", remoteCommand)
		if server.load {
			args = append(args, "-ex", "monitor reset halt", "-ex", "load", "-ex", "monitor reset halt")
		}
		return append(args, extra...), nil
	case backendLLDB:
		if server.load {
			return nil, errors.New("llgo debug: LLDB does not yet automate OpenOCD image loading; use -backend=gdb")
		}
		args := []string{
			artifact,
			"-o", "gdb-remote " + server.address,
			"-o", "target modules load --file " + quoteLLDBArgument(artifact) + " --slide 0",
		}
		return append(args, extra...), nil
	case backendWasmtime:
		args := []string{
			artifact,
			"-o", "process connect --plugin wasm connect://" + server.address,
		}
		return append(args, extra...), nil
	default:
		return nil, fmt.Errorf("llgo debug: backend %s does not use GDB Remote", selected)
	}
}

func quoteLLDBArgument(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(filepath.ToSlash(value)) + `"`
}
