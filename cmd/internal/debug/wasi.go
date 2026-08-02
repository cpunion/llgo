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
	"os"
	"path/filepath"
	"strings"

	"github.com/goplus/llgo/internal/debugabi"
	"github.com/goplus/llgo/internal/wasmdebug"
)

func validateWASIDebugArtifact(path string) (*wasmdebug.MemoryImport, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("llgo debug: read WASI artifact %q: %w", path, err)
	}
	hasDWARF, err := wasmdebug.HasDWARF(raw)
	if err != nil {
		return nil, fmt.Errorf("llgo debug: validate WASI artifact %q: %w", path, err)
	}
	if !hasDWARF {
		if externalURL, ok, urlErr := wasmdebug.ExternalURL(raw); urlErr != nil {
			return nil, fmt.Errorf("llgo debug: validate external WASI DWARF reference: %w", urlErr)
		} else if ok {
			return nil, fmt.Errorf("llgo debug: Wasmtime guest debugging currently requires embedded DWARF; artifact references external debug file %q", externalURL)
		}
		return nil, errors.New("llgo debug: WASI artifact contains no DWARF sections")
	}

	record, ok, err := wasmdebug.DebuggerRecord(raw)
	if err != nil {
		return nil, fmt.Errorf("llgo debug: validate WASI debugger ABI: %w", err)
	}
	if !ok {
		return nil, errors.New("llgo debug: WASI artifact has no LLGo debugger ABI record")
	}
	if record.PointerSize != 4 || record.ByteOrder != debugabi.ByteOrderLittle {
		return nil, fmt.Errorf("llgo debug: unsupported WASI debugger ABI target: pointer size %d, byte order %d", record.PointerSize, record.ByteOrder)
	}

	memories, err := wasmdebug.ImportedMemories(raw)
	if err != nil {
		return nil, fmt.Errorf("llgo debug: inspect WASI memory imports: %w", err)
	}
	var environment *wasmdebug.MemoryImport
	for index := range memories {
		memory := &memories[index]
		if memory.Module != "env" || memory.Name != "memory" {
			continue
		}
		if environment != nil {
			return nil, errors.New("llgo debug: WASI artifact imports env.memory more than once")
		}
		environment = memory
	}
	if environment != nil && environment.Memory64 {
		return nil, errors.New("llgo debug: memory64 WASI guest debugging is not supported yet")
	}
	return environment, nil
}

func writeWASIEnvironment(memory *wasmdebug.MemoryImport) (path string, cleanup func(), err error) {
	cleanup = func() {}
	dir, err := os.MkdirTemp("", "llgo-wasmtime-env-")
	if err != nil {
		return "", cleanup, fmt.Errorf("llgo debug: create Wasmtime environment directory: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	var declaration strings.Builder
	if memory != nil {
		fmt.Fprintf(&declaration, "  (memory (export \"memory\") %d", memory.Minimum)
		if memory.HasMax {
			fmt.Fprintf(&declaration, " %d", memory.Maximum)
		}
		if memory.Shared {
			declaration.WriteString(" shared")
		}
		declaration.WriteString(")\n")
	}
	wat := "(module\n" + declaration.String() +
		"  (func (export \"longjmp\") (param i32 i32) unreachable)\n" +
		"  (func (export \"pthread_exit\") (param i32) unreachable))\n"
	path = filepath.Join(dir, "env.wat")
	if err := os.WriteFile(path, []byte(wat), 0600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("llgo debug: write Wasmtime environment module: %w", err)
	}
	return path, cleanup, nil
}
