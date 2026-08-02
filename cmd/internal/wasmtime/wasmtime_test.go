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

package wasmtime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFindPrecedenceAndVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	newWasmtime := writeFakeWasmtime(t, "wasmtime 44.0.0 (test)")
	oldWasmtime := writeFakeWasmtime(t, "wasmtime 43.0.1 (test)")

	if got, err := findFrom(newWasmtime, oldWasmtime, nil); err != nil || got != newWasmtime {
		t.Fatalf("configured Wasmtime = (%q, %v), want (%q, nil)", got, err, newWasmtime)
	}
	if got, err := findFrom("", newWasmtime, nil); err != nil || got != newWasmtime {
		t.Fatalf("environment Wasmtime = (%q, %v), want (%q, nil)", got, err, newWasmtime)
	}
	if got, err := findFrom("", "", []string{oldWasmtime, newWasmtime}); err != nil || got != newWasmtime {
		t.Fatalf("fallback Wasmtime = (%q, %v), want (%q, nil)", got, err, newWasmtime)
	}
	if _, err := findFrom(oldWasmtime, "", nil); err == nil || !strings.Contains(err.Error(), "version 44 or newer") {
		t.Fatalf("old Wasmtime error = %v", err)
	}
	if _, err := findFrom(filepath.Join(t.TempDir(), "missing"), "", nil); err == nil || !strings.Contains(err.Error(), "find Wasmtime") {
		t.Fatalf("missing Wasmtime error = %v", err)
	}
}

func writeFakeWasmtime(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wasmtime")
	script := "#!/bin/sh\nprintf '%s\\n' '" + version + "'\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
