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

// Package debug implements the cross-platform "llgo debug" command.
package debug

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/goplus/llgo/cmd/internal/base"
	"github.com/goplus/llgo/cmd/internal/flags"
	"github.com/goplus/llgo/internal/build"
	"github.com/goplus/llgo/internal/mockable"
	"github.com/goplus/llgo/internal/optlevel"
	"github.com/goplus/llgo/internal/targets"
)

// Cmd is the llgo debug command.
var Cmd = &base.Command{
	UsageLine: "llgo debug [-backend auto|lldb|gdb|wasmtime|browser] [-target platform] [build flags] [package] [-- debugger arguments...]",
	Short:     "Build and debug an LLGo program",
}

var (
	goBuildFlags  *base.PassArgs
	backendFlag   string
	lldbPath      string
	gdbPath       string
	wasmtimePath  string
	chromePath    string
	browserTools  bool
	remoteAddress string
	serverCommand string
	sourceMapFlag stringListFlag
)

type stringListFlag []string

func (values *stringListFlag) String() string { return strings.Join(*values, ",") }

func (values *stringListFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func init() {
	Cmd.Run = runCmd
	goBuildFlags = flags.CaptureGoBuildFlags(Cmd)
	flags.AddCommonFlags(&Cmd.Flag)
	flags.AddCompilerVerboseFlag(&Cmd.Flag)
	flags.AddBuildFlags(&Cmd.Flag)
	flags.AddEmbeddedFlags(&Cmd.Flag)
	flags.AddOutputFlags(&Cmd.Flag)
	Cmd.Flag.StringVar(&backendFlag, "backend", string(backendAuto), "debug backend: auto, lldb, gdb, wasmtime, or browser")
	Cmd.Flag.StringVar(&lldbPath, "lldb", "", "path to LLDB (default $LLGO_LLDB or auto-detect)")
	Cmd.Flag.StringVar(&gdbPath, "gdb", "", "path to GDB (default $LLGO_GDB, target candidates, or auto-detect)")
	Cmd.Flag.StringVar(&wasmtimePath, "wasmtime", "", "path to Wasmtime (default $LLGO_WASMTIME or auto-detect)")
	Cmd.Flag.StringVar(&chromePath, "chrome", "", "path to Chromium (default $LLGO_CHROME or auto-detect)")
	Cmd.Flag.BoolVar(&browserTools, "browser-devtools", true, "open Chrome DevTools for a browser debug session")
	Cmd.Flag.StringVar(&remoteAddress, "remote", "", "connect to an existing debug server at host:port")
	Cmd.Flag.StringVar(&serverCommand, "server", "", "debug-server command template; {} is the artifact and {debug-port} is the allocated port")
	Cmd.Flag.Var(&sourceMapFlag, "source-map", "browser source path mapping FROM=TO (repeatable)")
}

func runCmd(cmd *base.Command, args []string) {
	commandArgs, debuggerArgs := splitDebuggerArgs(args)
	if err := cmd.Flag.Parse(commandArgs); err != nil {
		mockable.Exit(2)
		return
	}
	if err := run(cmd.Flag.Args(), debuggerArgs, options{
		backend:      backend(backendFlag),
		lldb:         lldbPath,
		gdb:          gdbPath,
		wasmtime:     wasmtimePath,
		chrome:       chromePath,
		browserTools: browserTools,
		remote:       remoteAddress,
		server:       serverCommand,
		sourceMap:    append([]string(nil), sourceMapFlag...),
	}, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		mockable.Exit(1)
	}
}

func splitDebuggerArgs(args []string) (command, debugger []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func run(packageArgs, debuggerArgs []string, opts options, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(packageArgs) > 1 {
		return errors.New("llgo debug: exactly one package may be debugged")
	}
	if len(packageArgs) == 0 {
		packageArgs = []string{"."}
	}
	if err := opts.validate(); err != nil {
		return err
	}

	conf := build.NewDefaultConf(build.ModeBuild)
	if err := flags.UpdateConfig(conf); err != nil {
		return fmt.Errorf("llgo debug: %w", err)
	}
	if err := flags.ApplyGoBuildFlags(conf, goBuildFlags.Args); err != nil {
		return fmt.Errorf("llgo debug: %w", err)
	}
	conf.BuildMode = build.BuildModeExe
	conf.OmitDWARFByDefault = false
	if conf.LinkOptions.EffectiveOmitDWARF() ||
		(conf.DebugArtifactModeSet && conf.DebugArtifactMode == build.DebugArtifactNone) {
		return errors.New("llgo debug: debug information is required; remove -ldflags=-w or -debug-artifact=none")
	}
	if conf.OptLevel == optlevel.Unset {
		conf.OptLevel = optlevel.O0
	}

	target, err := resolveTarget(conf.Target)
	if err != nil {
		return err
	}
	applyResolvedTarget(conf, target)
	selected, err := selectBackend(opts.backend, classifyTarget(conf, target))
	if err != nil {
		return err
	}
	if selected == backendBrowser && (opts.remote != "" || opts.server != "") {
		return errors.New("llgo debug: the browser backend does not use -remote or -server")
	}
	if target == nil && opts.remote == "" && (conf.Goos != runtime.GOOS || conf.Goarch != runtime.GOARCH) {
		return fmt.Errorf("llgo debug: cannot launch a %s/%s program on %s/%s without -remote", conf.Goos, conf.Goarch, runtime.GOOS, runtime.GOARCH)
	}

	cleanup, artifact, err := prepareArtifact(conf)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err = build.Do(packageArgs, conf); err != nil {
		return err
	}
	if _, err = os.Stat(artifact); err != nil {
		return fmt.Errorf("llgo debug: built artifact %q is unavailable: %w", artifact, err)
	}

	return runSession(session{
		backend:      selected,
		artifact:     artifact,
		debuggerArgs: debuggerArgs,
		target:       target,
		options:      opts,
	}, stdin, stdout, stderr)
}

func applyResolvedTarget(conf *build.Config, target *targets.Config) {
	if conf == nil || target == nil {
		return
	}
	// The existing wasm/wasi target names intentionally use the GOOS/GOARCH
	// crosscompile path instead of the generic target-libc builder. Resolve
	// those values before build.Do so debug-artifact selection and the
	// crosscompiler agree on the target from the start.
	if target.GOARCH == "wasm" {
		conf.Goos = target.GOOS
		conf.Goarch = target.GOARCH
		conf.Target = ""
	}
}

func resolveTarget(name string) (*targets.Config, error) {
	if name == "" {
		return nil, nil
	}
	target, err := targets.NewDefaultResolver().Resolve(name)
	if err != nil {
		return nil, fmt.Errorf("llgo debug: %w", err)
	}
	return target, nil
}

func prepareArtifact(conf *build.Config) (cleanup func(), artifact string, err error) {
	cleanup = func() {}
	ext := debugArtifactExtension(conf)
	if conf.OutFile == "" {
		dir, err := os.MkdirTemp("", "llgo-debug-")
		if err != nil {
			return cleanup, "", fmt.Errorf("llgo debug: create artifact directory: %w", err)
		}
		cleanup = func() { os.RemoveAll(dir) }
		conf.OutFile = filepath.Join(dir, "program"+ext)
	} else if ext != "" && !strings.HasSuffix(conf.OutFile, ext) {
		conf.OutFile += ext
	}
	conf.AppExt = ext
	artifact, err = filepath.Abs(conf.OutFile)
	if err != nil {
		cleanup()
		return func() {}, "", fmt.Errorf("llgo debug: resolve artifact path: %w", err)
	}
	conf.OutFile = artifact
	return cleanup, artifact, nil
}

func debugArtifactExtension(conf *build.Config) string {
	if conf.Target != "" {
		if strings.HasPrefix(conf.Target, "wasi") || strings.HasPrefix(conf.Target, "wasm") {
			return ".wasm"
		}
		return ".elf"
	}
	switch conf.Goos {
	case "windows":
		return ".exe"
	case "js", "wasi", "wasip1":
		return ".wasm"
	default:
		return ""
	}
}
