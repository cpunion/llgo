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
	"strings"
	"testing"
	"time"
)

func TestCoroWorkerMappingRetentionAcceptance(t *testing.T) {
	if os.Getenv("LLGO_CORO_WORKER_MAPPING_ACCEPTANCE") != "1" {
		t.Skip("set LLGO_CORO_WORKER_MAPPING_ACCEPTANCE=1 for the linked worker GC gate")
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
	image := filepath.Join(t.TempDir(), "worker-mapping-retention")
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-p=1", "-Os", "-trimpath", "-ldflags=-s -w", "-o", image,
		"./_testgo/coro_worker_mapping_retention")
	build.Dir = filepath.Join(root, "runtime")
	build.Env = append(os.Environ(), "LLGO_ROOT="+root, "LLGO_BUILD_CACHE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("worker mapping GC build: %v\n%s", err, output)
	}
	for _, procs := range []string{"1", "4"} {
		t.Run("P"+procs, func(t *testing.T) {
			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 60*time.Second)
			defer cancel()
			run := exec.CommandContext(ctx, image)
			run.Env = append(os.Environ(), "GOMAXPROCS="+procs, "GOGC=100", "GOMEMLIMIT=off")
			if output, err := run.CombinedOutput(); err != nil || !strings.Contains(string(output), "worker mapping retention ok") {
				t.Fatalf("worker mapping GC run: %v\n%s", err, output)
			}
		})
	}
}
