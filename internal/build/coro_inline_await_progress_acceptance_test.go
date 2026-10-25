//go:build !llgo && (darwin || linux)

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
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCoroInlineAwaitProgressAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_CALL_PROGRESS_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_CALL_PROGRESS_ACCEPTANCE=1 for linked inline-await progress acceptance")
	}
	compiler, err := filepath.Abs(os.Getenv("LLGO_TEST_LLGO"))
	if err != nil || os.Getenv("LLGO_TEST_LLGO") == "" {
		t.Fatal("LLGO_TEST_LLGO must identify the compiler being accepted")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "inline-await-progress")
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-Os", "-trimpath", "-ldflags=-s -w", "-o", image, "./internal/build/_testgo/coro_inline_await_progress")
	build.Dir = root
	build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("inline-await progress build: %v\n%s", err, output)
	}
	for _, procs := range []string{"1", "4"} {
		for _, mode := range []string{"direct", "nested"} {
			t.Run("P"+procs+"/"+mode, func(t *testing.T) {
				ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
				defer cancel()
				run := exec.CommandContext(ctx, image, mode)
				run.Env = append(os.Environ(), "GOMAXPROCS="+procs, "GOGC=100", "GOMEMLIMIT=off", "LLGO_TEST_BLOCK_PROGRESS_CALL=")
				output, err := run.CombinedOutput()
				if err != nil {
					t.Fatalf("inline-await progress run: %v\n%s", err, output)
				}
				fields := strings.Fields(string(output))
				if len(fields) != 7 || strings.Join(fields[:5], " ") != "inline await progress ok "+mode {
					t.Fatalf("unexpected progress receipt: %s", output)
				}
				calls, err := strconv.ParseUint(fields[5], 10, 64)
				if err != nil || procs == "1" && calls > 32768 {
					t.Fatalf("ready peer exceeded the single-owner service bound: calls=%d error=%v\n%s", calls, err, output)
				}
				t.Logf("%s", output)
			})
		}
	}
}
