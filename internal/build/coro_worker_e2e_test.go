//go:build darwin || linux

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
	"os/exec"
	"path/filepath"
	"testing"
)

// buildCoroNativeWorkerCallObject materializes the LLGoFiles leaf normally
// owned by runtime/internal/coroworker. Source-island E2E tests emit package
// LLVM modules themselves, so the ordinary package linker never gets a chance
// to add this C object for them.
func buildCoroNativeWorkerCallObject(t *testing.T, temp string) string {
	t.Helper()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	source := filepath.Join("..", "..", "runtime", "internal", "coroworker", "_worker", "worker.c")
	object := filepath.Join(temp, "coro-worker-call.o")
	if output, err := exec.Command(clang, "-std=c11", "-O2", "-c", source, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile native coroutine worker leaf: %v\n%s", err, output)
	}
	return object
}

// buildCoroNativeDoorbellObject materializes the LLGoFiles leaf normally
// owned by runtime/internal/corodoorbell. Source-island E2E tests emit package
// LLVM modules themselves, so the ordinary package linker never gets a chance
// to add this C object for them.
func buildCoroNativeDoorbellObject(t *testing.T, temp string) string {
	t.Helper()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	source := filepath.Join("..", "..", "runtime", "internal", "corodoorbell", "_wrap", "doorbell.c")
	object := filepath.Join(temp, "coro-doorbell.o")
	if output, err := exec.Command(clang, "-std=c11", "-O2", "-c", source, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile native coroutine doorbell leaf: %v\n%s", err, output)
	}
	return object
}
