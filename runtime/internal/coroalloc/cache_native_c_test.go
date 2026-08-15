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

package coroalloc

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeCacheBoundsZeroesAndSerializes(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is unavailable")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "cache_test.c")
	program := `
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

void *__llgo_coro_alloc_cache_take_v1(uintptr_t size);
bool __llgo_coro_alloc_cache_put_v1(void *pointer, uintptr_t size);

enum {
    bounded_size = 256,
    bounded_capacity = 512,
	compact_size = 384,
    thread_count = 8,
    thread_iterations = 10000,
    concurrent_size = 512,
};

static void *exercise_cache(void *unused) {
    (void)unused;
    for (int iteration = 0; iteration < thread_iterations; ++iteration) {
        unsigned char *pointer =
            __llgo_coro_alloc_cache_take_v1(concurrent_size);
        if (pointer == NULL) {
            pointer = calloc(1, concurrent_size);
        }
        if (pointer == NULL) {
            return (void *)(uintptr_t)1;
        }
        for (int index = 0; index < concurrent_size; ++index) {
            if (pointer[index] != 0) {
                return (void *)(uintptr_t)2;
            }
        }
        memset(pointer, 0xa5, concurrent_size);
        if (!__llgo_coro_alloc_cache_put_v1(pointer, concurrent_size)) {
            free(pointer);
        }
    }
    return NULL;
}

int main(void) {
    if (__llgo_coro_alloc_cache_take_v1(255) != NULL ||
        __llgo_coro_alloc_cache_put_v1(NULL, bounded_size)) {
        return 10;
    }
    unsigned char *unsupported = malloc(255);
    if (unsupported == NULL ||
        __llgo_coro_alloc_cache_put_v1(unsupported, 255)) {
        return 11;
    }
    free(unsupported);

    unsigned char *stored[bounded_capacity];
    for (int index = 0; index < bounded_capacity; ++index) {
        stored[index] = malloc(bounded_size);
        if (stored[index] == NULL) {
            return 12;
        }
        memset(stored[index], 0xa5, bounded_size);
        if (!__llgo_coro_alloc_cache_put_v1(stored[index], bounded_size)) {
            return 13;
        }
    }
    unsigned char *overflow = malloc(bounded_size);
    if (overflow == NULL) {
        return 14;
    }
    memset(overflow, 0xa5, bounded_size);
    if (__llgo_coro_alloc_cache_put_v1(overflow, bounded_size)) {
        return 15;
    }
    free(overflow);

    for (int index = 0; index < bounded_capacity; ++index) {
        unsigned char *pointer =
            __llgo_coro_alloc_cache_take_v1(bounded_size);
        if (pointer == NULL) {
            return 16;
        }
        for (int byte = 0; byte < bounded_size; ++byte) {
            if (pointer[byte] != 0) {
                return 17;
            }
        }
        free(pointer);
    }
    if (__llgo_coro_alloc_cache_take_v1(bounded_size) != NULL) {
        return 18;
    }

    unsigned char *compact = malloc(compact_size);
    if (compact == NULL) {
        return 21;
    }
    memset(compact, 0xa5, compact_size);
    if (!__llgo_coro_alloc_cache_put_v1(compact, compact_size)) {
        return 22;
    }
    compact = __llgo_coro_alloc_cache_take_v1(compact_size);
    if (compact == NULL) {
        return 23;
    }
    for (int byte = 0; byte < compact_size; ++byte) {
        if (compact[byte] != 0) {
            return 24;
        }
    }
    free(compact);

    pthread_t threads[thread_count];
    for (int index = 0; index < thread_count; ++index) {
        if (pthread_create(&threads[index], NULL, exercise_cache, NULL) != 0) {
            return 19;
        }
    }
    for (int index = 0; index < thread_count; ++index) {
        void *result = (void *)(uintptr_t)1;
        if (pthread_join(threads[index], &result) != 0 || result != NULL) {
            return 20;
        }
    }
    for (;;) {
        void *pointer =
            __llgo_coro_alloc_cache_take_v1(concurrent_size);
        if (pointer == NULL) {
            break;
        }
        free(pointer);
    }
    return 0;
}
`
	if err := os.WriteFile(harness, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	cache, err := filepath.Abs(filepath.Join("_cache", "cache.c"))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "cache_test")
	compile := exec.Command(
		cc, "-std=c11", "-Wall", "-Wextra", "-Werror", "-pthread",
		cache, harness, "-o", executable,
	)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile native coroutine cache test: %v\n%s", err, output)
	}
	if output, err := exec.Command(executable).CombinedOutput(); err != nil {
		t.Fatalf("native coroutine cache test failed: %v\n%s", err, output)
	}
}
