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

package build

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/xgo-dev/llgo/internal/clang"
	gllvm "github.com/xgo-dev/llvm"
)

type wasmFuncInfoRelink struct {
	mapPath        string
	rootPath       string
	staticRootPath string
	inputs         []string
	userMap        bool
	stdoutMap      bool
	gcRoots        bool
	// An opaque response file may select an unknown map path. --print-map
	// always wins over -Map in wasm-ld, so capture only the probe's stdout
	// and always perform a final link with the original arguments afterward.
	stdoutProbe bool
}

func prepareWasmFuncInfoRelink(ctx *context, outputPath string, inputs, linkArgs []string) (*wasmFuncInfoRelink, error) {
	if ctx == nil || ctx.buildConf == nil || ctx.prog == nil ||
		ctx.buildConf.BuildMode != BuildModeExe ||
		runtimeSiteObjectFormat(ctx) != siteObjectWasm ||
		(!shouldEmitRuntimeEntrySites(ctx) && !ctx.prog.GCRootsEnabled()) {
		return nil, nil
	}
	gcRoots := ctx.prog.GCRootsEnabled()
	// A linker accepts only one map destination. Reuse an explicitly requested
	// map instead of silently overriding -extldflags with our private probe.
	args := ctx.linker().LinkArguments(linkArgs...)
	stdoutProbe := wasmLinkNeedsStdoutProbe(args)
	path := wasmLinkMapOutput(args)
	if !stdoutProbe && path != "" && path != "-" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(ctx.commands.dir, path)
		}
		return &wasmFuncInfoRelink{mapPath: path, inputs: slices.Clone(inputs), userMap: true, gcRoots: gcRoots}, nil
	}
	f, err := os.CreateTemp(filepath.Dir(outputPath), ".llgo-wasm-funcinfo-*.map")
	if err != nil {
		return nil, fmt.Errorf("create WebAssembly funcinfo link map: %w", err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return nil, fmt.Errorf("close WebAssembly funcinfo link map: %w", err)
	}
	return &wasmFuncInfoRelink{mapPath: name, inputs: slices.Clone(inputs), stdoutMap: path == "-", stdoutProbe: stdoutProbe, gcRoots: gcRoots}, nil
}

// A stdout map is captured privately during the probe. Publish it only when
// there is no second link; otherwise the final link prints the final map.
func (p *wasmFuncInfoRelink) publishProbeMap(out io.Writer) error {
	if p == nil || !p.stdoutMap {
		return nil
	}
	f, err := os.Open(p.mapPath)
	if err != nil {
		return fmt.Errorf("open WebAssembly stdout link map: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(out, f); err != nil {
		return fmt.Errorf("publish WebAssembly stdout link map: %w", err)
	}
	return nil
}

func (p *wasmFuncInfoRelink) cleanup() {
	if p != nil {
		if !p.userMap {
			_ = os.Remove(p.mapPath)
		}
		_ = os.Remove(p.rootPath)
		_ = os.Remove(p.staticRootPath)
	}
}

func (p *wasmFuncInfoRelink) probeArgs() []string {
	if p == nil || p.userMap {
		return nil
	}
	if p.stdoutProbe {
		return []string{"-Xlinker", "--print-map"}
	}
	return []string{"-Xlinker", "--Map=" + p.mapPath}
}

func (p *wasmFuncInfoRelink) linkProbe(cmd *clang.Cmd, args []string) error {
	args = append(slices.Clone(args), p.probeArgs()...)
	if p == nil || !p.stdoutProbe {
		return cmd.Link(args...)
	}
	f, err := os.Create(p.mapPath)
	if err != nil {
		return fmt.Errorf("open WebAssembly probe stdout map: %w", err)
	}
	return linkWasmMapProbe(cmd, f, args)
}

func linkWasmMapProbe(cmd *clang.Cmd, output io.WriteCloser, args []string) (err error) {
	stdout := cmd.Stdout
	cmd.Stdout = output
	defer func() {
		cmd.Stdout = stdout
		if closeErr := output.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close WebAssembly probe stdout map: %w", closeErr))
		}
	}()
	return cmd.Link(args...)
}

// Do not expand user response files: Clang, wasm-ld and emcc have different
// tokenizers, encodings and path rules. Passing the original arguments to the
// final driver preserves those rules, including nested files and spaced paths.
// Automatic driver response-file creation happens after this inspection and
// does not select this extra-link path for ordinary large command lines.
func wasmLinkNeedsStdoutProbe(args []string) bool {
	for _, arg := range args {
		options := []string{arg}
		if strings.HasPrefix(arg, "-Wl,") {
			options = strings.Split(strings.TrimPrefix(arg, "-Wl,"), ",")
		}
		for _, option := range options {
			if strings.HasPrefix(option, "@") || option == "--print-map" {
				return true
			}
		}
	}
	return false
}

func (p *wasmFuncInfoRelink) liveEntryObject(ctx *context) (string, error) {
	if p == nil {
		return "", nil
	}
	data, err := os.ReadFile(p.mapPath)
	if err != nil {
		return "", fmt.Errorf("read WebAssembly funcinfo link map: %w", err)
	}
	linkMap := string(data)
	// Entry records only serve the exact function-value lookup performed by
	// runtime.FuncForPC. Avoid a second link (and all associated metadata) for
	// programs that do not use that capability.
	if !wasmLinkMapHasSymbol(linkMap, "runtime.FuncForPC") {
		return "", nil
	}
	candidates, err := wasmFuncInfoEntryCandidates(ctx, p.inputs)
	if err != nil {
		return "", err
	}
	roots := wasmFuncInfoLiveEntryRoots(linkMap, candidates)
	if len(roots) == 0 {
		return "", nil
	}
	f, err := os.CreateTemp("", "llgo-wasm-funcinfo-roots-*.o")
	if err != nil {
		return "", fmt.Errorf("create WebAssembly funcinfo root object: %w", err)
	}
	p.rootPath = f.Name()
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close WebAssembly funcinfo root object: %w", err)
	}
	if err := writeWasmFuncInfoRootObject(ctx, p.rootPath, roots); err != nil {
		return "", err
	}
	return p.rootPath, nil
}

func (p *wasmFuncInfoRelink) staticRootObject(ctx *context) (string, error) {
	if p == nil || !p.gcRoots {
		return "", nil
	}
	data, err := os.ReadFile(p.mapPath)
	if err != nil {
		return "", fmt.Errorf("read WebAssembly static-root link map: %w", err)
	}
	start, ok := wasmMutableDataStart(string(data))
	if !ok {
		return "", errors.New("WebAssembly link map has no mutable data segment")
	}
	f, err := os.CreateTemp("", "llgo-wasm-static-roots-*.o")
	if err != nil {
		return "", fmt.Errorf("create WebAssembly static-root object: %w", err)
	}
	p.staticRootPath = f.Name()
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close WebAssembly static-root object: %w", err)
	}
	if err := writeWasmStaticRootObject(ctx, p.staticRootPath, start); err != nil {
		return "", err
	}
	return p.staticRootPath, nil
}

// wasm-ld lays out .rodata before .data and .bss. The first mutable output
// segment is therefore the lower bound for conservative static roots. The
// final support object contains only code; the FuncInfo retention array is in
// the later llgo_gc_noscan segment, so the probe address remains stable across
// the final link.
func wasmMutableDataStart(linkMap string) (uint64, bool) {
	for _, line := range strings.Split(linkMap, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 || (fields[3] != ".data" && fields[3] != ".bss") {
			continue
		}
		address, err := strconv.ParseUint(fields[0], 16, 64)
		if err == nil {
			return address, true
		}
	}
	return 0, false
}

func writeWasmStaticRootObject(ctx *context, path string, start uint64) error {
	llvmCtx := gllvm.NewContext()
	defer llvmCtx.Dispose()
	mod := llvmCtx.NewModule("llgo.wasm.static.roots")
	defer mod.Dispose()
	mod.SetDataLayout(ctx.prog.DataLayout())
	mod.SetTarget(ctx.prog.Target().Spec().Triple)

	wordType := llvmCtx.Int32Type()
	if ctx.prog.PointerSize() == 8 {
		wordType = llvmCtx.Int64Type()
	}
	functionType := gllvm.FunctionType(wordType, nil, false)
	function := gllvm.AddFunction(mod, "llgo_gc_globals_start", functionType)
	entry := llvmCtx.AddBasicBlock(function, "entry")
	builder := llvmCtx.NewBuilder()
	builder.SetInsertPointAtEnd(entry)
	builder.CreateRet(gllvm.ConstInt(wordType, start, false))
	builder.Dispose()

	buf, err := ctx.prog.TargetMachine().EmitToMemoryBuffer(mod, gllvm.ObjectFile)
	if err != nil {
		return fmt.Errorf("emit WebAssembly static-root object: %w", err)
	}
	defer buf.Dispose()
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write WebAssembly static-root object: %w", err)
	}
	return nil
}

// wasmLinkMapOutput follows the linker's last-option-wins rule after unwrapping
// the two Clang driver spellings. Keep paths (including spaces) intact.
func wasmLinkMapOutput(args []string) string {
	var linkerArgs []string
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "-Xlinker" && i+1 < len(args):
			i++
			linkerArgs = append(linkerArgs, args[i])
		case strings.HasPrefix(arg, "-Wl,"):
			linkerArgs = append(linkerArgs, strings.Split(strings.TrimPrefix(arg, "-Wl,"), ",")...)
		}
	}
	var path string
	for i := 0; i < len(linkerArgs); i++ {
		switch arg := linkerArgs[i]; {
		case (arg == "-Map" || arg == "--Map") && i+1 < len(linkerArgs):
			i++
			path = linkerArgs[i]
		case strings.HasPrefix(arg, "-Map="):
			path = strings.TrimPrefix(arg, "-Map=")
		case strings.HasPrefix(arg, "--Map="):
			path = strings.TrimPrefix(arg, "--Map=")
		}
	}
	return path
}

func writeWasmFuncInfoRootObject(ctx *context, path string, roots []string) error {
	llvmCtx := gllvm.NewContext()
	defer llvmCtx.Dispose()
	mod := llvmCtx.NewModule("llgo.wasm.funcinfo.roots")
	defer mod.Dispose()
	mod.SetDataLayout(ctx.prog.DataLayout())
	mod.SetTarget(ctx.prog.Target().Spec().Triple)

	i8Type := llvmCtx.Int8Type()
	pointerType := gllvm.PointerType(i8Type, 0)
	values := make([]gllvm.Value, 0, len(roots))
	for _, name := range roots {
		row := gllvm.AddGlobal(mod, i8Type, name)
		values = append(values, gllvm.ConstBitCast(row, pointerType))
	}
	init := gllvm.ConstArray(pointerType, values)
	registry := gllvm.AddGlobal(mod, init.Type(), "__llgo_wasm_funcinfo_roots")
	registry.SetInitializer(init)
	registry.SetLinkage(gllvm.InternalLinkage)
	registry.SetGlobalConstant(true)
	registry.SetSection("llgo_gc_noscan")
	appendLLVMUsed(mod, []gllvm.Value{registry})

	buf, err := ctx.prog.TargetMachine().EmitToMemoryBuffer(mod, gllvm.ObjectFile)
	if err != nil {
		return fmt.Errorf("emit WebAssembly funcinfo root object: %w", err)
	}
	defer buf.Dispose()
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write WebAssembly funcinfo root object: %w", err)
	}
	return nil
}

func wasmFuncInfoEntryCandidates(ctx *context, inputs []string) (map[string]string, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	nm, err := exec.LookPath("llvm-nm")
	if err != nil {
		return nil, fmt.Errorf("WebAssembly funcinfo requires llvm-nm: %w", err)
	}
	args := make([]string, 0, len(inputs)+2)
	args = append(args, "--defined-only", "--format=just-symbols")
	args = append(args, inputs...)
	cmd := ctx.commands.configure(exec.Command(nm, args...))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list WebAssembly funcinfo entries: %w\n%s", err, output)
	}
	return parseWasmFuncInfoEntryCandidates(string(output)), nil
}

func parseWasmFuncInfoEntryCandidates(output string) map[string]string {
	candidates := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		if index := strings.Index(line, wasmFuncInfoEntryPrefix); index >= 0 {
			row := strings.TrimSpace(line[index:])
			if row == "" {
				continue
			}
			function := strings.TrimPrefix(row, wasmFuncInfoEntryPrefix)
			if function != "" && function != "sentinel" {
				candidates[function] = row
			}
		}
	}
	return candidates
}

func wasmFuncInfoLiveEntryRoots(linkMap string, candidates map[string]string) []string {
	if len(candidates) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var roots []string
	for _, line := range strings.Split(linkMap, "\n") {
		rest := strings.TrimSpace(line)
		for range 3 {
			_, tail, ok := cutMapField(rest)
			if !ok {
				rest = ""
				break
			}
			rest = tail
		}
		row := candidates[rest]
		if row == "" || seen[row] {
			continue
		}
		seen[row] = true
		roots = append(roots, row)
	}
	slices.Sort(roots)
	return roots
}

func wasmLinkMapHasSymbol(linkMap, symbol string) bool {
	for _, line := range strings.Split(linkMap, "\n") {
		rest := strings.TrimSpace(line)
		for range 3 {
			_, tail, ok := cutMapField(rest)
			if !ok {
				rest = ""
				break
			}
			rest = tail
		}
		if rest == symbol {
			return true
		}
	}
	return false
}

func cutMapField(line string) (field, rest string, ok bool) {
	line = strings.TrimLeft(line, " \t")
	if line == "" {
		return "", "", false
	}
	end := strings.IndexAny(line, " \t")
	if end < 0 {
		return line, "", true
	}
	return line[:end], strings.TrimLeft(line[end:], " \t"), true
}
