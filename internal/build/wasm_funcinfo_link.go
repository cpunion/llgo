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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	gllvm "github.com/xgo-dev/llvm"
)

type wasmFuncInfoRelink struct {
	mapPath  string
	rootPath string
	inputs   []string
}

func prepareWasmFuncInfoRelink(ctx *context, outputPath string, inputs []string) (*wasmFuncInfoRelink, error) {
	if ctx == nil || ctx.buildConf == nil || ctx.prog == nil ||
		ctx.buildConf.BuildMode != BuildModeExe ||
		runtimeSiteObjectFormat(ctx) != siteObjectWasm ||
		!shouldEmitRuntimeEntrySites(ctx) {
		return nil, nil
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
	return &wasmFuncInfoRelink{mapPath: name, inputs: slices.Clone(inputs)}, nil
}

func (p *wasmFuncInfoRelink) cleanup() {
	if p != nil {
		_ = os.Remove(p.mapPath)
		_ = os.Remove(p.rootPath)
	}
}

func (p *wasmFuncInfoRelink) probeArgs() []string {
	if p == nil {
		return nil
	}
	return []string{"-Xlinker", "--Map=" + p.mapPath}
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
	p.rootPath = strings.TrimSuffix(p.mapPath, filepath.Ext(p.mapPath)) + ".o"
	if err := writeWasmFuncInfoRootObject(ctx, p.rootPath, roots); err != nil {
		return "", err
	}
	return p.rootPath, nil
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
