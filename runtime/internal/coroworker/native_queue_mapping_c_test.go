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

package coroworker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNativeQueueMappingFailureAndJoinedRelease(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is unavailable")
	}
	worker, err := filepath.Abs(filepath.Join("_worker", "worker.c"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "mapping_test.c")
	program := `
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif
#include <sys/mman.h>
#include <sys/types.h>
#include <errno.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

static bool fail_map;
static bool fail_unmap;
static unsigned mappings;
static unsigned releases;
static void *test_map(void *, size_t, int, int, int, off_t);
static int test_unmap(void *, size_t);
#define mmap test_map
#define munmap test_unmap
#include ` + strconv.Quote(worker) + `
#undef mmap
#undef munmap

static void *test_map(void *hint, size_t bytes, int protection, int flags, int fd, off_t offset) {
    if (fail_map) {
        errno = ENOMEM;
        return MAP_FAILED;
    }
    void *result = mmap(hint, bytes, protection, flags, fd, offset);
    if (result != MAP_FAILED) ++mappings;
    return result;
}

static int test_unmap(void *pointer, size_t bytes) {
    if (fail_unmap) {
        errno = EINVAL;
        return -1;
    }
    int result = munmap(pointer, bytes);
    if (result == 0) ++releases;
    return result;
}

uint32_t __llgo_coro_native_worker_complete_v1(
    uint32_t source_slot, uint32_t generation,
    uintptr_t r1, uintptr_t r2, uintptr_t error, uintptr_t fault,
    uintptr_t fault_pc, uintptr_t fault_target) {
    (void)source_slot; (void)generation; (void)r1; (void)r2;
    (void)error; (void)fault; (void)fault_pc; (void)fault_target;
    return 0;
}

int main(int argc, char **argv) {
    if (argc == 2 && strcmp(argv[1], "release-failure") == 0) {
        if (!__llgo_coro_worker_queue_init_v1() ||
            !__llgo_coro_worker_queue_stop_v1(0)) return 20;
        fail_unmap = true;
        if (__llgo_coro_worker_queue_destroy_after_join_v1() ||
            __llgo_coro_worker_queue_can_release_v1() ||
            llgo_coro_worker_queue_v1.jobs == NULL || releases != 0) return 21;
        /* A failed OS release must keep the owner sealed. This test alone
           releases its injected failure's mapping before process exit. */
        fail_unmap = false;
        size_t bytes = sizeof(*llgo_coro_worker_queue_v1.jobs) * LLGO_CORO_WORKER_QUEUE_CAPACITY_V1;
        if (test_unmap(llgo_coro_worker_queue_v1.jobs, bytes) != 0) return 22;
        llgo_coro_worker_queue_v1.jobs = NULL;
        return mappings == releases ? 0 : 23;
    }
    fail_map = true;
    if (__llgo_coro_worker_queue_init_v1() ||
        !__llgo_coro_worker_queue_can_release_v1() || mappings != 0) return 1;
    fail_map = false;
    for (unsigned cycle = 0; cycle < 8; ++cycle) {
        if (!__llgo_coro_worker_queue_init_v1() ||
            llgo_coro_worker_queue_v1.jobs == NULL ||
            __llgo_coro_worker_queue_init_v1() ||
            !__llgo_coro_worker_queue_stop_v1(0) ||
            !__llgo_coro_worker_queue_destroy_after_join_v1() ||
            !__llgo_coro_worker_queue_can_release_v1() ||
            llgo_coro_worker_queue_v1.jobs != NULL ||
            mappings != cycle + 1 || releases != mappings) return 2;
    }
    return 0;
}
`
	if err := os.WriteFile(harness, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "mapping_test")
	compile := exec.Command(cc, "-std=c11", "-Wall", "-Wextra", "-Werror", "-pthread", harness, "-o", executable)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile native queue mapping test: %v\n%s", err, output)
	}
	for _, args := range [][]string{nil, {"release-failure"}} {
		if output, err := exec.Command(executable, args...).CombinedOutput(); err != nil {
			t.Fatalf("native queue mapping lifecycle %v: %v\n%s", args, err, output)
		}
	}
}
