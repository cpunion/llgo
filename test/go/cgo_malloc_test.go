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
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCgoMallocWrapperSymbols(t *testing.T) {
	if strings.TrimSpace(runHostGoCmd(t, "", "env", "CGO_ENABLED")) != "1" {
		t.Skip("cgo is disabled")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang is unavailable")
	}

	dir := t.TempDir()
	src := `package main

/*
#include <stdlib.h>
*/
import "C"
import "unsafe"

func main() {
	p := C.malloc(8)
	if p == nil {
		panic("C.malloc returned nil")
	}
	C.free(unsafe.Pointer(p))
}
`
	mainFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	runHostGoCmd(t, dir, "run", mainFile)

	root := findLLGoRoot(t)
	llgo := acceptanceLLGoBinary(t)
	t.Setenv("LLGO_ROOT", root)
	runLLGoWithoutHostCgoFlags(t, dir, llgo, "run", mainFile)
}

func runHostGoCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = hostCgoEnv(t)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("host go %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// hostCgoEnv keeps helper programs on the Go toolchain's host architecture.
// Cross-target LLGo jobs intentionally set GOARCH, CC, and cgo flags for the
// program under test; inheriting them makes an ordinary host `go run` try to
// link Go's host objects with the target linker.
func hostCgoEnv(t *testing.T) []string {
	cc, cxx := "clang", "clang++"
	if hostOS, _ := goHostTarget(t); hostOS == "windows" {
		// The Go Windows host linker expects a GNU-compatible C toolchain;
		// plain clang selects link.exe and cannot consume Go's linker script.
		cc, cxx = "gcc", "g++"
		if hostRoot := os.Getenv("LLGO_MINGW_HOST_ROOT"); hostRoot != "" {
			cc = filepath.Join(hostRoot, "bin", "clang.exe")
			cxx = filepath.Join(hostRoot, "bin", "clang++.exe")
		}
	}
	return hostGoEnvWithCompiler(t, cc, cxx)
}

// hostLLGoToolEnv configures an already-built LLGo helper for the Go host
// architecture. Unlike hostCgoEnv, its compiler must be Clang because LLGo
// inspects the driver when selecting the native Windows ABI.
func hostLLGoToolEnv(t *testing.T) []string {
	cc, cxx := "clang", "clang++"
	if hostOS, _ := goHostTarget(t); hostOS == "windows" {
		if hostRoot := os.Getenv("LLGO_MINGW_HOST_ROOT"); hostRoot != "" {
			cc = filepath.Join(hostRoot, "bin", "clang.exe")
			cxx = filepath.Join(hostRoot, "bin", "clang++.exe")
		}
	}
	return hostGoEnvWithCompiler(t, cc, cxx)
}

func hostGoEnvWithCompiler(t *testing.T, cc, cxx string) []string {
	t.Helper()
	hostOS, hostArch := goHostTarget(t)

	env := make([]string, 0, len(os.Environ())+4)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		upper := strings.ToUpper(name)
		if upper == "GOOS" || upper == "GOARCH" || upper == "CC" || upper == "CXX" ||
			(strings.HasPrefix(upper, "CGO_") && strings.HasSuffix(upper, "FLAGS")) {
			continue
		}
		env = append(env, value)
	}
	return append(env, "GOOS="+hostOS, "GOARCH="+hostArch, "CC="+cc, "CXX="+cxx)
}

func goHostTarget(t *testing.T) (goos, goarch string) {
	t.Helper()
	query := exec.Command("go", "env", "GOHOSTOS", "GOHOSTARCH")
	query.Env = os.Environ()
	out, err := query.CombinedOutput()
	if err != nil {
		t.Fatalf("query Go host target: %v\n%s", err, out)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		t.Fatalf("go env GOHOSTOS GOHOSTARCH returned %q", out)
	}
	return fields[0], fields[1]
}

func runLLGoWithoutHostCgoFlags(t *testing.T, dir, llgo string, args ...string) {
	t.Helper()
	cmd := exec.Command(llgo, args...)
	cmd.Dir = dir
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(name, "CGO_") && strings.HasSuffix(name, "FLAGS") {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s failed: %v\nstdout:\n%s\nstderr:\n%s", llgo, strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
}

func runGoCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := commandForTest(t, dir, "go", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func findLLGoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/xgo-dev/llgo") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("cannot find llgo repository root")
		}
		dir = parent
	}
}
