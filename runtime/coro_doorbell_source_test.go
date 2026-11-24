//go:build !llgo

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

package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	coroDoorbellGoSource = "internal/corodoorbell/pipe_llgo.go"
	coroDoorbellCSource  = "internal/corodoorbell/_wrap/doorbell.c"
)

func TestCoroDoorbellUsesExactBoundedForeignLeaves(t *testing.T) {
	goSource, err := os.ReadFile(coroDoorbellGoSource)
	if err != nil {
		t.Fatal(err)
	}
	goText := string(goSource)
	requireRuntimeAnnotationFreeCDeclarations(
		t,
		coroDoorbellGoSource,
		"nativeCDoorbellOpen",
		"nativeCDoorbellRead",
		"nativeCDoorbellClose",
	)
	for _, required := range []string{
		`LLGoFiles   = "_wrap/doorbell.c"`,
		"//go:linkname nativeCDoorbellOpen C.__llgo_coro_doorbell_open_v1",
		"//go:linkname nativeCDoorbellRead C.__llgo_coro_doorbell_read_v1",
		"//llgo:coro contract foreign.v1 scope=declaration progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return\n//go:linkname nativeCDoorbellWrite C.__llgo_coro_doorbell_write_v1",
		"//go:linkname nativeCDoorbellWrite C.__llgo_coro_doorbell_write_v1",
		"//go:linkname nativeCDoorbellClose C.__llgo_coro_doorbell_close_v1",
		"unpackNativeDoorbellResult",
	} {
		if !strings.Contains(goText, required) {
			t.Errorf("%s lacks exact doorbell boundary marker %q", coroDoorbellGoSource, required)
		}
	}
	for _, forbidden := range []string{
		"runtime/internal/clite/os",
		"cliteos.Pipe(",
		"cliteos.Read(",
		"cliteos.Write(",
		"cliteos.Close(",
		"nativeErrno(",
	} {
		if strings.Contains(goText, forbidden) {
			t.Errorf("%s still exposes generic foreign edge %q", coroDoorbellGoSource, forbidden)
		}
	}

	for _, path := range []string{
		"internal/corodoorbell/pipe_poll_darwin_llgo.go",
		"internal/corodoorbell/pipe_poll_linux_llgo.go",
	} {
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		text := string(source)
		if !strings.Contains(text, "//go:linkname nativeCPoll C.__llgo_coro_doorbell_poll_one_v1") ||
			!strings.Contains(text, "compiler derives that") {
			t.Errorf("%s does not expose the exact inferred raw-host one-fd poll operation", path)
		}
		for _, capability := range []string{"noblock", "sync", "schedulerwait", "worker"} {
			if strings.Contains(text, "//llgo:coro "+capability+"\n//go:linkname nativeCPoll ") {
				t.Errorf("%s gives inferred blocking one-fd poll the obsolete or incorrect %s declaration capability", path, capability)
			}
		}
		if strings.Contains(text, "C.poll") || strings.Contains(text, "nativeErrno()") {
			t.Errorf("%s still reaches generic poll/errno directly", path)
		}
	}
	for _, path := range []string{
		"internal/corodoorbell/poll_set_darwin_llgo.go",
		"internal/corodoorbell/poll_set_linux_llgo.go",
	} {
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		text := string(source)
		if !strings.Contains(text, "//go:linkname nativeCPollSet C.__llgo_coro_doorbell_poll_set_v1") ||
			!strings.Contains(text, "compiler admits it only for the scheduler-owner") {
			t.Errorf("%s does not expose the exact inferred bounded owner poll operation", path)
		}
		for _, capability := range []string{"noblock", "sync", "schedulerwait", "worker"} {
			if strings.Contains(text, "//llgo:coro "+capability+"\n//go:linkname nativeCPollSet ") {
				t.Errorf("%s gives inferred blocking poll-set the obsolete or incorrect %s declaration capability", path, capability)
			}
		}
		if strings.Contains(text, "C.poll") || strings.Contains(text, "nativeErrno()") {
			t.Errorf("%s still reaches generic poll/errno directly", path)
		}
	}

	cSource, err := os.ReadFile(coroDoorbellCSource)
	if err != nil {
		t.Fatal(err)
	}
	cText := string(cSource)
	for _, required := range []string{
		"LLGO_DOORBELL_OPEN_RETRIES_V1 16",
		"LLGO_DOORBELL_READ_MAX_V1 64u",
		"LLGO_DOORBELL_WRITE_SIZE_V1 1u",
		"LLGO_DOORBELL_POLL_CAPACITY_V1 1025u",
		"LLGO_DOORBELL_POLL_MAX_MS_V1 1000",
		"O_NONBLOCK",
		"FD_CLOEXEC",
		"count != 1",
		"count > LLGO_DOORBELL_POLL_CAPACITY_V1",
		"timeout_ms > LLGO_DOORBELL_POLL_MAX_MS_V1",
		"Never retry close",
	} {
		if !strings.Contains(cText, required) {
			t.Errorf("%s lacks bounded capability marker %q", coroDoorbellCSource, required)
		}
	}
}

func TestCoroDoorbellFixedCLeaves(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("native coroutine doorbell is unavailable on %s", runtime.GOOS)
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("host C compiler is unavailable")
	}
	dir := t.TempDir()
	testSource := filepath.Join(dir, "doorbell_test.c")
	program := `
#define _POSIX_C_SOURCE 200809L
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <stdint.h>

#if defined(__APPLE__)
typedef uint32_t llgo_doorbell_nfds_v1;
#else
typedef uintptr_t llgo_doorbell_nfds_v1;
#endif

int32_t __llgo_coro_doorbell_open_v1(int32_t output[2]);
uint64_t __llgo_coro_doorbell_read_v1(int32_t, void *, uintptr_t);
uint64_t __llgo_coro_doorbell_write_v1(int32_t, const void *, uintptr_t);
int32_t __llgo_coro_doorbell_close_v1(int32_t);
uint64_t __llgo_coro_doorbell_poll_one_v1(
    struct pollfd *, llgo_doorbell_nfds_v1, int32_t);
uint64_t __llgo_coro_doorbell_poll_set_v1(
    struct pollfd *, llgo_doorbell_nfds_v1, int32_t);
#if defined(__APPLE__)
void __llgo_coro_doorbell_poll_close_epoch_bump_v1(void);
int32_t __llgo_coro_doorbell_owns_poll_fd_v1(int32_t);
#endif

static int32_t result(uint64_t packed) { return (int32_t)(uint32_t)packed; }
static int32_t error(uint64_t packed) { return (int32_t)(uint32_t)(packed >> 32); }

int main(void) {
    int32_t fds[2] = {-1, -1};
    struct pollfd entry = {0};
    uint8_t byte = 7;
    uint64_t packed;

    if (!__llgo_coro_doorbell_open_v1(fds) ||
        (fcntl(fds[0], F_GETFL, 0) & O_NONBLOCK) == 0 ||
        (fcntl(fds[1], F_GETFL, 0) & O_NONBLOCK) == 0 ||
        (fcntl(fds[0], F_GETFD, 0) & FD_CLOEXEC) == 0 ||
        (fcntl(fds[1], F_GETFD, 0) & FD_CLOEXEC) == 0) {
        return 10;
    }
    packed = __llgo_coro_doorbell_read_v1(fds[0], &byte, 1);
    if (result(packed) != -1 ||
        (error(packed) != EAGAIN && error(packed) != EWOULDBLOCK)) {
        return 11;
    }
    packed = __llgo_coro_doorbell_write_v1(fds[1], &byte, 2);
    if (result(packed) != -1 || error(packed) != EINVAL) {
        return 12;
    }
    packed = __llgo_coro_doorbell_write_v1(fds[1], &byte, 1);
    if (result(packed) != 1 || error(packed) != 0) {
        return 13;
    }
    entry.fd = fds[0];
    entry.events = POLLIN;
    packed = __llgo_coro_doorbell_poll_one_v1(&entry, 1, 0);
    if (result(packed) != 1 || error(packed) != 0 || (entry.revents & POLLIN) == 0) {
        return 14;
    }
    packed = __llgo_coro_doorbell_read_v1(fds[0], &byte, 1);
    if (result(packed) != 1 || error(packed) != 0 || byte != 7) {
        return 15;
    }
    packed = __llgo_coro_doorbell_poll_set_v1(&entry, 1, -1);
    if (result(packed) != -1 || error(packed) != EINVAL) {
        return 16;
    }
#if defined(__APPLE__)
    /* Exercise persistent Darwin kqueue: level-triggered re-fire, swapped order,
     * filter removal, close-epoch resync, and >64 fallback to poll(2). */
    {
        int32_t p1[2] = {-1, -1};
        int32_t p2[2] = {-1, -1};
        struct pollfd set[3];
        struct pollfd wide[65];
        uint32_t i;
        if (!__llgo_coro_doorbell_open_v1(p1) || !__llgo_coro_doorbell_open_v1(p2)) {
            return 20;
        }
        if (result(__llgo_coro_doorbell_write_v1(p1[1], &byte, 1)) != 1) {
            return 21;
        }
        set[0].fd = fds[0]; set[0].events = POLLIN; set[0].revents = 0;
        set[1].fd = p1[0];  set[1].events = POLLIN; set[1].revents = 0;
        set[2].fd = p2[1];  set[2].events = POLLOUT; set[2].revents = 0;
        packed = __llgo_coro_doorbell_poll_set_v1(set, 3, 0);
        if (result(packed) != 2 || error(packed) != 0 ||
            set[0].revents != 0 || (set[1].revents & POLLIN) == 0 || (set[2].revents & POLLOUT) == 0) {
            return 22;
        }
        /* Swap order of p1/p2 (nchanges==0 steady state) and verify level-triggered re-fire. */
        set[1].fd = p2[1]; set[1].events = POLLOUT; set[1].revents = 0;
        set[2].fd = p1[0]; set[2].events = POLLIN;  set[2].revents = 0;
        packed = __llgo_coro_doorbell_poll_set_v1(set, 3, 0);
        if (result(packed) != 2 || error(packed) != 0 ||
            (set[1].revents & POLLOUT) == 0 || (set[2].revents & POLLIN) == 0) {
            return 23;
        }
        /* Drain p1 and remove p2 (EV_DELETE); neither doorbell nor p1 should fire. */
        if (result(__llgo_coro_doorbell_read_v1(p1[0], &byte, 1)) != 1) {
            return 24;
        }
        set[1].fd = p1[0]; set[1].events = POLLIN; set[1].revents = 0;
        packed = __llgo_coro_doorbell_poll_set_v1(set, 2, 0);
        if (result(packed) != 0 || error(packed) != 0) {
            return 25;
        }
        /* Close-epoch bump must resync without losing readiness on newly written pipe. */
        __llgo_coro_doorbell_poll_close_epoch_bump_v1();
        if (result(__llgo_coro_doorbell_write_v1(p1[1], &byte, 1)) != 1) {
            return 26;
        }
        packed = __llgo_coro_doorbell_poll_set_v1(set, 2, 0);
        if (result(packed) != 1 || error(packed) != 0 || (set[1].revents & POLLIN) == 0) {
            return 27;
        }
        if (result(__llgo_coro_doorbell_read_v1(p1[0], &byte, 1)) != 1) {
            return 28;
        }
        /* >64 watches must transparently fall back to poll(2). */
        wide[0].fd = fds[0]; wide[0].events = POLLIN; wide[0].revents = 0;
        for (i = 1; i < 65; i++) {
            wide[i].fd = p1[0]; wide[i].events = POLLIN; wide[i].revents = 0;
        }
        wide[64].fd = p2[1]; wide[64].events = POLLOUT; wide[64].revents = 0;
        packed = __llgo_coro_doorbell_poll_set_v1(wide, 65, 0);
        if (result(packed) != 1 || error(packed) != 0 || (wide[64].revents & POLLOUT) == 0) {
            return 29;
        }
        if (__llgo_coro_doorbell_owns_poll_fd_v1(fds[0]) != 0) {
            return 30;
        }
        if (!__llgo_coro_doorbell_close_v1(p1[0]) || !__llgo_coro_doorbell_close_v1(p1[1]) ||
            !__llgo_coro_doorbell_close_v1(p2[0]) || !__llgo_coro_doorbell_close_v1(p2[1])) {
            return 31;
        }
    }
#endif
    if (!__llgo_coro_doorbell_close_v1(fds[0]) ||
        !__llgo_coro_doorbell_close_v1(fds[1])) {
        return 17;
    }
    return 0;
}
`
	if err := os.WriteFile(testSource, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper, err := filepath.Abs(coroDoorbellCSource)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "doorbell_test")
	compile := exec.Command(cc, "-std=c11", "-Wall", "-Wextra", "-Werror", wrapper, testSource, "-o", executable)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile fixed doorbell adapter: %v\n%s", err, output)
	}
	if output, err := exec.Command(executable).CombinedOutput(); err != nil {
		t.Fatalf("run fixed doorbell adapter: %v\n%s", err, output)
	}
}
