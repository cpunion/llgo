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
	"net/url"
	"os"
	"path/filepath"

	"github.com/goplus/llgo/internal/debugabi"
	"github.com/goplus/llgo/internal/wasmdebug"
)

func finalizeDebugArtifact(conf *Config, out *OutFmtDetails, verbose bool) error {
	if conf == nil || out == nil {
		return nil
	}
	if conf.DebugArtifactMode != DebugArtifactExternal {
		if out.Out != "" {
			// A successful non-external rebuild owns this conventional sibling
			// path and must not leave a stale optional artifact behind.
			_ = os.Remove(dwarfSidecarPath(out.Out))
		}
		if conf.DebugArtifactMode == DebugArtifactEmbedded && conf.Goarch == "wasm" {
			return finalizeEmbeddedWasmDebuggerRecord(conf, out)
		}
		return nil
	}
	if out.Out == "" {
		return fmt.Errorf("external DWARF executable path is empty")
	}
	if out.DWARF == "" {
		return fmt.Errorf("external DWARF output path is empty")
	}
	raw, err := os.ReadFile(out.Out)
	if err != nil {
		return err
	}
	if conf.Goarch == "wasm" {
		raw, err = wasmdebug.SetDebuggerRecord(raw, wasmDebuggerRecord(conf))
		if err != nil {
			return fmt.Errorf("add WebAssembly debugger ABI record: %w", err)
		}
	}
	// external_debug_info stores a URL, not a filesystem path. Keep the
	// sidecar adjacent to the module and escape its filename for URL lookup.
	main, err := wasmdebug.Externalize(raw, url.PathEscape(filepath.Base(out.DWARF)))
	if err != nil {
		return fmt.Errorf("externalize WebAssembly DWARF: %w", err)
	}
	info, err := os.Stat(out.Out)
	if err != nil {
		return err
	}
	if err := writeDebugArtifactFile(out.DWARF, raw, info.Mode()); err != nil {
		return err
	}
	if err := writeDebugArtifactFile(out.Out, main, info.Mode()); err != nil {
		_ = os.Remove(out.DWARF)
		return err
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "llgo: external DWARF: %d bytes -> %s\n", len(raw), out.DWARF)
	}
	return nil
}

func finalizeEmbeddedWasmDebuggerRecord(conf *Config, out *OutFmtDetails) error {
	if out.Out == "" {
		return fmt.Errorf("embedded WebAssembly debugger artifact path is empty")
	}
	raw, err := os.ReadFile(out.Out)
	if err != nil {
		return err
	}
	raw, err = wasmdebug.SetDebuggerRecord(raw, wasmDebuggerRecord(conf))
	if err != nil {
		return fmt.Errorf("add WebAssembly debugger ABI record: %w", err)
	}
	info, err := os.Stat(out.Out)
	if err != nil {
		return err
	}
	return writeDebugArtifactFile(out.Out, raw, info.Mode())
}

func wasmDebuggerRecord(conf *Config) debugabi.Record {
	return debugabi.NewRecord(uint8(conf.AbiMode), 4, debugabi.ByteOrderLittle)
}

func writeDebugArtifactFile(path string, data []byte, mode os.FileMode) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(mode.Perm()); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
