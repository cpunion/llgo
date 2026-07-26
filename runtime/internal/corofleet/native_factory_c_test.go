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

package corofleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeFactorySerializesConcurrentTaintedOwnerCreation(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is unavailable")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "native_factory_test.c")
	program := `
#include "owner.h"

#include <pthread.h>
#include <signal.h>
#include <stdatomic.h>
#include <stdint.h>

enum {
    requester_count = 16,
};

static _Atomic uint32_t seen[requester_count + 2];
static _Atomic uint32_t inherited_taint;

uint32_t __llgo_coro_native_fleet_owner_v2(uint32_t slot) {
    sigset_t current;
    if (slot == 0 || slot > requester_count + 1 ||
        pthread_sigmask(SIG_SETMASK, NULL, &current) != 0 ||
        __llgo_coro_fleet_owner_ready_v1(slot) != 0) {
        return 0;
    }
    if (sigismember(&current, SIGUSR1) == 1) {
        atomic_fetch_add_explicit(
            &inherited_taint, 1, memory_order_relaxed);
    }
    atomic_fetch_add_explicit(&seen[slot], 1, memory_order_relaxed);
    return 1;
}

static void *request_owner(void *word) {
    uint32_t slot = (uint32_t)(uintptr_t)word;
    sigset_t blocked;
    sigemptyset(&blocked);
    sigaddset(&blocked, SIGUSR1);
    if (pthread_sigmask(SIG_BLOCK, &blocked, NULL) != 0) {
        return (void *)(uintptr_t)1;
    }
    pthread_t owner = (pthread_t)0;
    if (__llgo_coro_fleet_owner_create_v2(&owner, slot) != 0 ||
        owner == (pthread_t)0) {
        return (void *)(uintptr_t)2;
    }
    void *result = (void *)(uintptr_t)1;
    if (pthread_join(owner, &result) != 0 || result != NULL) {
        return (void *)(uintptr_t)3;
    }
    return NULL;
}

int main(void) {
    pthread_t rejected = (pthread_t)0;
    if (__llgo_coro_fleet_owner_create_v2(&rejected, 1) == 0 ||
        __llgo_coro_fleet_factory_stop_v1() == 0) {
        return 10;
    }

    sigset_t unblocked;
    sigemptyset(&unblocked);
    sigaddset(&unblocked, SIGUSR1);
    if (pthread_sigmask(SIG_UNBLOCK, &unblocked, NULL) != 0 ||
        __llgo_coro_fleet_factory_start_v1() != 0 ||
        __llgo_coro_fleet_factory_start_v1() == 0) {
        return 11;
    }

    pthread_t requesters[requester_count];
    for (uint32_t index = 0; index < requester_count; ++index) {
        if (pthread_create(
                &requesters[index],
                NULL,
                request_owner,
                (void *)(uintptr_t)(index + 1)) != 0) {
            return 12;
        }
    }
    for (uint32_t index = 0; index < requester_count; ++index) {
        void *result = (void *)(uintptr_t)1;
        if (pthread_join(requesters[index], &result) != 0 || result != NULL) {
            return 13;
        }
    }
    if (atomic_load_explicit(&inherited_taint, memory_order_relaxed) != 0) {
        return 14;
    }
    for (uint32_t slot = 1; slot <= requester_count; ++slot) {
        if (atomic_load_explicit(&seen[slot], memory_order_relaxed) != 1) {
            return 15;
        }
    }
    if (__llgo_coro_fleet_factory_stop_v1() != 0 ||
        __llgo_coro_fleet_owner_create_v2(&rejected, 1) == 0) {
        return 16;
    }

    /* Restart proves stop destroyed every pthread primitive and tombstone. */
    if (__llgo_coro_fleet_factory_start_v1() != 0) {
        return 17;
    }
    pthread_t owner = (pthread_t)0;
    if (__llgo_coro_fleet_owner_create_v2(
            &owner, requester_count + 1) != 0 ||
        owner == (pthread_t)0) {
        return 18;
    }
    void *result = (void *)(uintptr_t)1;
    if (pthread_join(owner, &result) != 0 || result != NULL ||
        atomic_load_explicit(
            &seen[requester_count + 1], memory_order_relaxed) != 1 ||
        __llgo_coro_fleet_factory_stop_v1() != 0) {
        return 19;
    }
    return 0;
}
`
	if err := os.WriteFile(harness, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	owner, err := filepath.Abs(filepath.Join("_owner", "owner.c"))
	if err != nil {
		t.Fatal(err)
	}
	include := filepath.Dir(owner)
	executable := filepath.Join(dir, "native_factory_test")
	compile := exec.Command(cc,
		"-std=c11", "-Wall", "-Wextra", "-Werror", "-pthread",
		"-I", include, owner, harness, "-o", executable,
	)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile native clean M factory test: %v\n%s", err, output)
	}
	if output, err := exec.Command(executable).CombinedOutput(); err != nil {
		t.Fatalf("native clean M factory lifecycle failed: %v\n%s", err, output)
	}
}
