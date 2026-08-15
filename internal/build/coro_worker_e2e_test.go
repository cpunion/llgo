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

// buildCoroNativeAllocationCacheObject materializes the LLGoFiles leaf owned
// by runtime/internal/coroalloc. Source-island E2E tests emit package LLVM
// modules directly, so their ordinary package link never sees this C object.
func buildCoroNativeAllocationCacheObject(t *testing.T, temp string) string {
	t.Helper()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	source := filepath.Join("..", "..", "runtime", "internal", "coroalloc", "_cache", "cache.c")
	object := filepath.Join(temp, "coro-allocation-cache.o")
	if output, err := exec.Command(clang, "-std=c11", "-O2", "-c", source, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile native coroutine allocation cache leaf: %v\n%s", err, output)
	}
	return object
}

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

// buildCoroNativePollObject materializes the scalar poll-descriptor and
// bounded socket-attempt leaf normally owned by runtime's source package.
func buildCoroNativePollObject(t *testing.T, temp string) string {
	t.Helper()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	source := filepath.Join("..", "..", "runtime", "internal", "lib", "runtime", "_wrap", "poll.c")
	object := filepath.Join(temp, "coro-poll.o")
	if output, err := exec.Command(clang, "-std=c11", "-O2", "-c", source, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile native coroutine poll leaf: %v\n%s", err, output)
	}
	return object
}

// buildCoroNativeFleetOwnerObject materializes the bounded pthread routine
// owned by runtime/internal/corofleet. It has one static C-to-Go edge and
// accepts only a scalar route, never a function pointer or coroutine identity
// from managed code.
func buildCoroNativeFleetOwnerObject(t *testing.T, temp string) string {
	t.Helper()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	source := filepath.Join("..", "..", "runtime", "internal", "corofleet", "_owner", "owner.c")
	object := filepath.Join(temp, "coro-fleet-owner.o")
	if output, err := exec.Command(clang, "-std=c11", "-O2", "-c", source, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile native coroutine fleet owner leaf: %v\n%s", err, output)
	}
	return object
}
