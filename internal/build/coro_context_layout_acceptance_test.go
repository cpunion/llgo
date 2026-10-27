//go:build !llgo && (darwin || linux)

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoroContextLayoutAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_CONTEXT_LAYOUT_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_CONTEXT_LAYOUT_ACCEPTANCE=1 for linked context ownership acceptance")
	}
	compiler := os.Getenv("LLGO_TEST_LLGO")
	if compiler == "" {
		t.Fatal("LLGO_TEST_LLGO must identify the compiler being accepted")
	}
	compiler, err := filepath.Abs(compiler)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, cwd string, timeout time.Duration, env []string, args ...string) string {
		t.Helper()
		ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), env...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("context ownership command %v: %v\n%s", args, err, output)
		}
		return string(output)
	}
	dir := t.TempDir()
	layout := filepath.Join(dir, "context-layout")
	native := filepath.Join(dir, "native-start.test")
	buildEnv := []string{"LLGO_ROOT=" + root, "LLGO_BUILD_CACHE=off", "GOMAXPROCS=2", "GOMEMLIMIT=2GiB"}
	run(t, filepath.Join(root, "runtime"), 5*time.Minute, buildEnv, compiler,
		"build", "-p=1", "-Os", "-trimpath", "-tags=coro_context_layout_test", "-o", layout, "./_test/corocontextlayout")
	run(t, root, 5*time.Minute, buildEnv, compiler, "test", "-c", "-p=1", "-Os", "-trimpath", "-o", native,
		"test/llgoext/runtime_g_test.go", "test/llgoext/runtime_native_start_test.go")
	for _, procs := range []string{"1", "4"} {
		t.Run("P"+procs, func(t *testing.T) {
			env := []string{"GOMAXPROCS=" + procs, "GOGC=100", "GOMEMLIMIT=off"}
			output := run(t, root, 30*time.Second, env, layout)
			if !strings.Contains(output, "context layout and ownership ok") {
				t.Fatalf("missing context layout receipt: %s", output)
			}
			t.Log(strings.TrimSpace(output))
			// Global registered-G assertions need a fresh process, including
			// between repeats. Other tests may have returned before their last
			// child or testing.tRunner has physically retired.
			for _, name := range []string{"NativeStartStorageAndLifetime", "NativeStartRejectsNilEntry"} {
				for repeat := 0; repeat < 3; repeat++ {
					output := run(t, root, 90*time.Second, append(env, "LLGO_NATIVE_START_ACCEPTANCE_CHILD=1"),
						native, "-test.v", "-test.count=1", "-test.timeout=60s", "-test.run=^TestRuntime"+name+"$")
					if strings.Count(output, "--- PASS: TestRuntime"+name+" ") != 1 || strings.Contains(output, "--- SKIP:") {
						t.Fatalf("missing isolated native pass for %s: %s", name, output)
					}
					t.Log(output)
				}
			}
			output = run(t, root, 90*time.Second, env, native, "-test.v", "-test.count=3", "-test.timeout=60s",
				"-test.run=^TestRuntime(GetGIsolation|GMP.*)$")
			for _, name := range []string{"GMPLinks", "GMPIDBatchAllocation", "GetGIsolation"} {
				if strings.Count(output, "--- PASS: TestRuntime"+name+" ") != 3 {
					t.Fatalf("missing three native passes for %s: %s", name, output)
				}
			}
			t.Log(output)
		})
	}
}
