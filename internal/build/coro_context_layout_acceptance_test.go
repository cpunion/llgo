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
		started := time.Now()
		t.Logf("context ownership command starts (limit %s): %v", timeout, args)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("context ownership command %v after %s: %v (context: %v)\n%s", args, time.Since(started), err, ctx.Err(), output)
		}
		t.Logf("context ownership command completed in %s", time.Since(started))
		return string(output)
	}
	dir := t.TempDir()
	layout := filepath.Join(dir, "context-layout")
	native := filepath.Join(dir, "native-start")
	buildEnv := []string{"LLGO_ROOT=" + root, "LLGO_BUILD_CACHE=off", "GOMAXPROCS=2", "GOMEMLIMIT=2GiB"}
	run(t, filepath.Join(root, "runtime"), 5*time.Minute, buildEnv, compiler,
		"build", "-p=1", "-Os", "-trimpath", "-tags=coro_context_layout_test", "-o", layout, "./_test/corocontextlayout")
	run(t, filepath.Join(root, "runtime"), 5*time.Minute, buildEnv, compiler,
		"build", "-p=1", "-Os", "-trimpath", "-o", native, "./_test/coronativestart")
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
			for _, name := range []string{"lifetime", "nil-entry"} {
				for repeat := 0; repeat < 3; repeat++ {
					output := run(t, root, 90*time.Second, env, native, name)
					if strings.TrimSpace(output) != "native startup ok "+name {
						t.Fatalf("missing isolated native pass for %s: %s", name, output)
					}
					t.Log(output)
				}
			}
			output = run(t, root, 90*time.Second, env, native, "gmp")
			if strings.TrimSpace(output) != "native startup ok gmp" {
				t.Fatalf("missing native G/M/P passes: %s", output)
			}
			t.Log(output)
		})
	}
}
