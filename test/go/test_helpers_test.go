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

package gotest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const llgoTestCompilerEnv = "LLGO_TEST_COMPILER"

func configuredLLGoTestCompiler(t *testing.T) string {
	t.Helper()
	compiler := os.Getenv(llgoTestCompilerEnv)
	if compiler == "" {
		compiler = os.Getenv("LLGO_TEST_LLGO")
	}
	if compiler == "" {
		return ""
	}
	abs, err := filepath.Abs(compiler)
	if err != nil {
		t.Fatalf("resolve %s: %v", llgoTestCompilerEnv, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat %s: %v", llgoTestCompilerEnv, err)
	}
	if info.IsDir() {
		t.Fatalf("%s points to a directory: %s", llgoTestCompilerEnv, abs)
	}
	return abs
}

func configuredLLGoSiblingTool(t *testing.T, name string) string {
	t.Helper()
	compiler := configuredLLGoTestCompiler(t)
	if compiler == "" {
		return ""
	}
	tool := filepath.Join(filepath.Dir(compiler), name+filepath.Ext(compiler))
	info, err := os.Stat(tool)
	if err != nil {
		t.Fatalf("stat configured LLGo tool %s: %v", name, err)
	}
	if info.IsDir() {
		t.Fatalf("configured LLGo tool %s points to a directory: %s", name, tool)
	}
	return tool
}

func runLLGoTestCommand(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(acceptanceLLGoBinary(t), args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("llgo %v failed: %v\n%s", args, err, output)
	}
	return output
}

func TestConfiguredLLGoTestCompiler(t *testing.T) {
	t.Setenv("LLGO_TEST_LLGO", "")
	t.Setenv("LLGO", "")
	t.Setenv(llgoTestCompilerEnv, "")
	if got := configuredLLGoTestCompiler(t); got != "" {
		t.Fatalf("empty %s resolved to %q", llgoTestCompilerEnv, got)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(llgoTestCompilerEnv, executable)
	want, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	if got := configuredLLGoTestCompiler(t); got != want {
		t.Fatalf("configured compiler = %q, want %q", got, want)
	}
	if got := configuredLLGo(t); got != want {
		t.Fatalf("configured test llgo fallback = %q, want %q", got, want)
	}

	t.Setenv(llgoTestCompilerEnv, "")
	t.Setenv("LLGO_TEST_LLGO", executable)
	if got := configuredLLGoTestCompiler(t); got != want {
		t.Fatalf("configured LLGO_TEST_LLGO compiler = %q, want %q", got, want)
	}

	dir := t.TempDir()
	ext := filepath.Ext(executable)
	compiler := filepath.Join(dir, "llgo"+ext)
	tool := filepath.Join(dir, "llgen"+ext)
	for _, path := range []string{compiler, tool} {
		if err := os.WriteFile(path, []byte("test"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LLGO_TEST_LLGO", "")
	t.Setenv(llgoTestCompilerEnv, compiler)
	if got := configuredLLGoSiblingTool(t, "llgen"); got != tool {
		t.Fatalf("configured sibling tool = %q, want %q", got, tool)
	}
}

func testExecutablePath(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}
