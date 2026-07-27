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
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	gllvm "github.com/xgo-dev/llvm"
)

const coroLibraryEffectObjectSymbol = "__llgo_coro_library_effect_archive_v1"

// writeCoroLibraryEffectObject wraps one format-neutral producer record in a
// minimal native object. A raw ar data member is rejected by Darwin ld even
// when it has no symbols; a native object remains linker-compatible and still
// avoids decoding any package LTO bitcode.
func (c *context) writeCoroLibraryEffectObject(path string, records []byte) error {
	if c == nil || c.prog == nil {
		return fmt.Errorf("missing LLGo program for coroutine library metadata object")
	}
	llvmContext := gllvm.NewContext()
	defer llvmContext.Dispose()
	module := llvmContext.NewModule("llgo.coro.library-effect.archive.v1")
	defer module.Dispose()
	if err := populateCoroLibraryEffectObjectModule(c.prog, module, records); err != nil {
		return err
	}
	if err := gllvm.VerifyModule(module, gllvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("verify coroutine library metadata object: %w", err)
	}
	if useInMemoryNativeCodegen(c) {
		object, err := c.prog.TargetMachine().EmitToMemoryBuffer(module, gllvm.ObjectFile)
		if err != nil {
			return fmt.Errorf("emit coroutine library metadata object: %w", err)
		}
		defer object.Dispose()
		if err := os.WriteFile(path, object.Bytes(), 0o644); err != nil {
			return fmt.Errorf("write coroutine library metadata object: %w", err)
		}
		return nil
	}

	irFile, err := os.CreateTemp("", filepath.Base(path)+"-*.ll")
	if err != nil {
		return fmt.Errorf("create coroutine library metadata IR: %w", err)
	}
	irPath := irFile.Name()
	defer os.Remove(irPath)
	if _, err := io.WriteString(irFile, module.String()); err != nil {
		irFile.Close()
		return fmt.Errorf("write coroutine library metadata IR: %w", err)
	}
	if err := irFile.Close(); err != nil {
		return fmt.Errorf("close coroutine library metadata IR: %w", err)
	}
	// This sidecar must remain a native object even when the package itself uses
	// Full/Thin LTO; the trailing flag overrides any configured -flto mode.
	if err := c.compiler().Compile(
		"-o", path, "-c", irPath, "-Wno-override-module", "-fno-lto",
	); err != nil {
		return fmt.Errorf("compile coroutine library metadata object: %w", err)
	}
	return nil
}

func populateCoroLibraryEffectObjectModule(
	prog llssa.Program,
	module gllvm.Module,
	records []byte,
) error {
	if prog == nil || module.IsNil() {
		return fmt.Errorf("missing LLVM program or module for coroutine library metadata object")
	}
	if len(records) == 0 {
		return fmt.Errorf("empty coroutine library metadata record")
	}
	module.SetDataLayout(prog.DataLayout())
	module.SetTarget(prog.TargetSpec().Triple)
	llvmContext := module.Context()
	if strings.HasPrefix(strings.ToLower(prog.TargetSpec().Triple), "wasm") {
		// LLVM's ordinary global section attribute creates a Wasm data segment,
		// which would consume runtime linear memory. The backend's documented
		// wasm.custom_sections metadata emits the producer record directly as
		// an object custom section instead.
		module.AddNamedMetadataOperand("wasm.custom_sections", llvmContext.MDNode([]gllvm.Metadata{
			llvmContext.MDString(coro.LibraryEffectSummarySection),
			llvmContext.MDString(string(records)),
		}))
		return nil
	}
	initial := llvmContext.ConstString(string(records), false)
	global := gllvm.AddGlobal(module, initial.Type(), coroLibraryEffectObjectSymbol)
	global.SetInitializer(initial)
	global.SetGlobalConstant(true)
	global.SetLinkage(gllvm.InternalLinkage)
	global.SetUnnamedAddr(true)
	global.SetAlignment(1)
	global.SetSection(coroLibraryEffectObjectSection(prog.TargetSpec().Triple))

	usedInitial := gllvm.ConstArray(global.Type(), []gllvm.Value{global})
	used := gllvm.AddGlobal(module, usedInitial.Type(), "llvm.compiler.used")
	used.SetInitializer(usedInitial)
	used.SetLinkage(gllvm.AppendingLinkage)
	used.SetSection("llvm.metadata")
	return nil
}

func coroLibraryEffectObjectSection(triple string) string {
	triple = strings.ToLower(triple)
	if strings.Contains(triple, "darwin") || strings.Contains(triple, "apple") {
		return "__LLVM,__llgo_coro"
	}
	return coro.LibraryEffectSummarySection
}

// readCoroLibraryEffectObject extracts only the dedicated compiler section
// from a metadata sidecar. Native formats use the standard-library object
// readers; WebAssembly custom sections are small enough to decode directly.
func readCoroLibraryEffectObject(data []byte) ([]byte, error) {
	if len(data) >= 4 && bytes.Equal(data[:4], []byte{0, 'a', 's', 'm'}) {
		return readCoroLibraryEffectWasmObject(data)
	}
	if file, err := elf.NewFile(bytes.NewReader(data)); err == nil {
		defer file.Close()
		section := file.Section(coro.LibraryEffectSummarySection)
		if section == nil {
			return nil, fmt.Errorf("ELF metadata object omits section %q", coro.LibraryEffectSummarySection)
		}
		return section.Data()
	}
	if file, err := macho.NewFile(bytes.NewReader(data)); err == nil {
		defer file.Close()
		section := file.Section("__llgo_coro")
		if section == nil {
			return nil, fmt.Errorf("Mach-O metadata object omits section %q", "__llgo_coro")
		}
		return section.Data()
	}
	if file, err := pe.NewFile(bytes.NewReader(data)); err == nil {
		defer file.Close()
		section := file.Section(coro.LibraryEffectSummarySection)
		if section == nil {
			return nil, fmt.Errorf("COFF metadata object omits section %q", coro.LibraryEffectSummarySection)
		}
		return section.Data()
	}
	return nil, fmt.Errorf("unsupported or corrupt coroutine library metadata object")
}

func readCoroLibraryEffectWasmObject(data []byte) ([]byte, error) {
	if len(data) < 8 || !bytes.Equal(data[:8], []byte{0, 'a', 's', 'm', 1, 0, 0, 0}) {
		return nil, fmt.Errorf("invalid WebAssembly coroutine library metadata object")
	}
	var found []byte
	var customSections []string
	for offset := 8; offset < len(data); {
		sectionID := data[offset]
		offset++
		size, err := readCoroLibraryEffectULEB(data, &offset)
		if err != nil {
			return nil, fmt.Errorf("read WebAssembly section size: %w", err)
		}
		if size > uint64(len(data)-offset) {
			return nil, fmt.Errorf("truncated WebAssembly section payload")
		}
		end := offset + int(size)
		if sectionID == 0 {
			nameOffset := offset
			nameSize, err := readCoroLibraryEffectULEB(data[:end], &nameOffset)
			if err != nil {
				return nil, fmt.Errorf("read WebAssembly custom-section name: %w", err)
			}
			if nameSize > uint64(end-nameOffset) {
				return nil, fmt.Errorf("truncated WebAssembly custom-section name")
			}
			nameEnd := nameOffset + int(nameSize)
			name := string(data[nameOffset:nameEnd])
			customSections = append(customSections, name)
			if name == coro.LibraryEffectSummarySection {
				if found != nil {
					return nil, fmt.Errorf("duplicate WebAssembly coroutine library metadata section")
				}
				found = append([]byte(nil), data[nameEnd:end]...)
			}
		}
		offset = end
	}
	if found == nil {
		return nil, fmt.Errorf(
			"WebAssembly metadata object omits custom section %q (has %q)",
			coro.LibraryEffectSummarySection, customSections,
		)
	}
	return found, nil
}

func readCoroLibraryEffectULEB(data []byte, offset *int) (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if *offset >= len(data) {
			return 0, io.ErrUnexpectedEOF
		}
		next := data[*offset]
		*offset++
		if shift == 63 && next > 1 {
			return 0, fmt.Errorf("ULEB128 value overflows uint64")
		}
		value |= uint64(next&0x7f) << shift
		if next&0x80 == 0 {
			return value, nil
		}
	}
	return 0, fmt.Errorf("ULEB128 value overflows uint64")
}
