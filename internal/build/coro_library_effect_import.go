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
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	gllvm "github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

var regularArchiveMagic = []byte("!<arch>\n")

func readImportCfgPackageFiles(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open importcfg %q: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var packageFiles []string
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		separator := strings.IndexAny(text, " \t")
		if separator < 0 {
			if text == "packagefile" {
				return nil, fmt.Errorf(
					"importcfg %q line %d has malformed packagefile directive",
					path, line,
				)
			}
			continue
		}
		directive := text[:separator]
		if directive != "packagefile" {
			continue
		}
		value := strings.TrimSpace(text[separator:])
		importPath, archive, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(importPath) == "" || strings.TrimSpace(archive) == "" {
			return nil, fmt.Errorf("importcfg %q line %d has malformed packagefile directive", path, line)
		}
		packageFiles = append(packageFiles, strings.TrimSpace(archive))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read importcfg %q: %w", path, err)
	}
	return packageFiles, nil
}

func regularArchive(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	header := make([]byte, len(regularArchiveMagic))
	if _, err := io.ReadFull(file, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(header, regularArchiveMagic), nil
}

func buildCoroLibraryEffectMetadata(ctx *context) (coro.LibraryEffectMetadata, error) {
	if ctx == nil || ctx.buildConf == nil || ctx.prog == nil {
		return coro.LibraryEffectMetadata{}, fmt.Errorf("missing build context for coroutine library metadata")
	}
	target := ctx.prog.TargetSpec()
	endianness := ""
	switch ctx.prog.TargetData().ByteOrder() {
	case gllvm.LittleEndian:
		endianness = "little"
	case gllvm.BigEndian:
		endianness = "big"
	default:
		return coro.LibraryEffectMetadata{}, fmt.Errorf("unsupported LLVM byte order")
	}
	return coro.LibraryEffectMetadata{
		FunctionIDSchema:   coro.FunctionIDSchema,
		CoroABI:            activeCoroABIVersion(ctx.buildConf),
		SchedulerABI:       activeCoroSchedulerABIVersion(ctx.buildConf),
		PanicABI:           activeCoroPanicABIVersion(ctx.buildConf),
		FuncRepABI:         activeCoroFuncRepABIVersion(ctx.buildConf),
		TargetTriple:       target.Triple,
		TargetCPU:          target.CPU,
		TargetFeatures:     target.Features,
		TargetABI:          target.TargetABI,
		PointerBits:        ctx.prog.PointerSize() * 8,
		Endianness:         endianness,
		DataLayout:         ctx.prog.DataLayout(),
		TargetCapabilities: ctx.buildConf.coroTargetCapabilities(),
	}, nil
}

func loadCoroLibraryEffectIndex(
	ctx *context,
) (*coro.LibraryEffectIndex, coro.LibraryEffectMetadata, error) {
	metadata, err := buildCoroLibraryEffectMetadata(ctx)
	if err != nil {
		return nil, coro.LibraryEffectMetadata{}, err
	}
	packageFiles, err := readImportCfgPackageFiles(ctx.buildConf.ImportCfg)
	if err != nil {
		return nil, coro.LibraryEffectMetadata{}, err
	}
	if len(packageFiles) == 0 {
		return nil, metadata, nil
	}
	seen := make(map[string]struct{}, len(packageFiles))
	var summaries []coro.LibraryEffectSummary
	for _, archive := range packageFiles {
		if _, duplicate := seen[archive]; duplicate {
			continue
		}
		seen[archive] = struct{}{}
		regular, err := regularArchive(archive)
		if err != nil {
			return nil, coro.LibraryEffectMetadata{}, fmt.Errorf(
				"inspect importcfg package archive %q: %w", archive, err,
			)
		}
		if !regular {
			// The Go importer remains authoritative for non-ar export data.
			// Absence of an LLGo record keeps bodyless Go calls conservative.
			continue
		}
		imported, found, err := ReadCoroLibraryEffectArchive(archive)
		if err != nil {
			return nil, coro.LibraryEffectMetadata{}, err
		}
		if found {
			summaries = append(summaries, imported...)
		}
	}
	if len(summaries) == 0 {
		return nil, metadata, nil
	}
	index, err := coro.NewLibraryEffectIndex(summaries, metadata)
	if err != nil {
		return nil, coro.LibraryEffectMetadata{}, fmt.Errorf("index importcfg coroutine library metadata: %w", err)
	}
	return index, metadata, nil
}

func prepareCoroImportedLibraryEffects(
	ctx *context,
	index *coro.LibraryEffectIndex,
	metadata coro.LibraryEffectMetadata,
) (map[*ssa.Function]coro.LibraryEffectFunction, error) {
	if index == nil {
		return nil, nil
	}
	if ctx == nil || ctx.coroEmission == nil {
		return nil, fmt.Errorf("imported coroutine library metadata requires a prepared emission universe")
	}
	functionIDs := ctx.coroEmission.FunctionIDConfig()
	functionIDs.CoroABI = metadata.CoroABI
	functionIDs.SchedulerABI = metadata.SchedulerABI
	functionIDs.ArchiveReady = true
	imported := make(map[*ssa.Function]coro.LibraryEffectFunction)
	for _, unresolved := range ctx.coroEmission.Functions() {
		function, ok := ctx.coroEmission.Resolve(unresolved)
		if !ok || function == nil || function != unresolved {
			continue
		}
		id, err := coro.StableFunctionID(function, functionIDs)
		if err != nil {
			return nil, fmt.Errorf("identify possible imported library function %q: %w", function.Name(), err)
		}
		fact, found := index.Lookup(id)
		if !found {
			continue
		}
		background, classified, err := ctx.coroEmission.FunctionBackground(function)
		if err != nil {
			return nil, fmt.Errorf("classify imported library function %q: %w", function.Name(), err)
		}
		if !classified || background != llssa.InGo {
			return nil, fmt.Errorf(
				"imported library fact %q does not match one bodyless managed-Go declaration",
				id,
			)
		}
		if len(function.Blocks) != 0 {
			// A source-loaded definition is authoritative in this compilation.
			// importcfg may still name a cache/archive for the same dependency;
			// its producer fact is needed only when the frontend has no body.
			continue
		}
		if err := ctx.coroEmission.CoroLibraryEffects().ValidateFunction(function, metadata, fact); err != nil {
			return nil, err
		}
		imported[function] = fact
	}
	if len(imported) == 0 {
		return nil, nil
	}
	return imported, nil
}

// prepareCoroImportedLibraryForeignCallables joins producer-owned C callable
// facts to exact frontend declarations. A locally generated conservative
// default is replaceable; an explicit local generic/legacy contract is source
// authority and may only agree exactly with the archive.
func prepareCoroImportedLibraryForeignCallables(
	ctx *context,
	index *coro.LibraryEffectIndex,
	metadata coro.LibraryEffectMetadata,
) (map[*ssa.Function]coro.LibraryEffectForeignCallable, error) {
	if index == nil {
		return nil, nil
	}
	if ctx == nil || ctx.coroEmission == nil {
		return nil, fmt.Errorf("imported coroutine library foreign metadata requires a prepared emission universe")
	}
	functionIDs := ctx.coroEmission.FunctionIDConfig()
	functionIDs.CoroABI = metadata.CoroABI
	functionIDs.SchedulerABI = metadata.SchedulerABI
	functionIDs.ArchiveReady = true
	imported := make(map[*ssa.Function]coro.LibraryEffectForeignCallable)
	for _, unresolved := range ctx.coroEmission.Functions() {
		function, ok := ctx.coroEmission.Resolve(unresolved)
		if !ok || function == nil || function != unresolved {
			continue
		}
		id, err := coro.StableFunctionID(function, functionIDs)
		if err != nil {
			return nil, fmt.Errorf("identify possible imported library foreign callable %q: %w", function.Name(), err)
		}
		fact, found := index.LookupForeignFunction(id)
		if !found {
			continue
		}
		if err := ctx.coroEmission.CoroLibraryEffects().ValidateForeignCallable(
			function, metadata, fact,
		); err != nil {
			return nil, err
		}

		noBlock, noBlockOK, err := ctx.coroEmission.CoroForeignNoBlockCertificate(function)
		if err != nil {
			return nil, fmt.Errorf("classify local foreign noblock metadata for %q: %w", id, err)
		}
		synchronous, syncOK, err := ctx.coroEmission.CoroForeignSyncCertificate(function)
		if err != nil {
			return nil, fmt.Errorf("classify local foreign sync metadata for %q: %w", id, err)
		}
		worker, workerOK, err := ctx.coroEmission.CoroForeignWorkerCertificate(function)
		if err != nil {
			return nil, fmt.Errorf("classify local foreign worker metadata for %q: %w", id, err)
		}
		legacy := noBlockOK || syncOK || workerOK ||
			noBlock.ID != "" || synchronous.ID != "" || worker.ID != ""
		if legacy {
			if fact.HasContract {
				return nil, fmt.Errorf(
					"imported library foreign callable %q conflicts with explicit local legacy metadata",
					id,
				)
			}
			// A v2 identity-only record cannot reproduce a legacy execution
			// certificate. Keep the exact source-loaded legacy fact authoritative.
			continue
		}

		local, localOK, err := ctx.coroEmission.CoroCallableContractCertificate(function)
		if err != nil {
			return nil, fmt.Errorf("classify local callable contract for %q: %w", id, err)
		}
		defaulted, err := ctx.coroEmission.CoroLibraryEffects().
			CallableContractDefault(function)
		if err != nil {
			return nil, fmt.Errorf("classify local callable contract provenance for %q: %w", id, err)
		}
		if localOK && !defaulted {
			if !fact.HasContract || local != fact.Contract {
				return nil, fmt.Errorf(
					"imported library foreign callable %q conflicts with the explicit local callable contract",
					id,
				)
			}
		}
		imported[function] = fact
	}
	if len(imported) == 0 {
		return nil, nil
	}
	return imported, nil
}
