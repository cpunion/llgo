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
	for _, required := range []string{
		`LLGoFiles   = "_wrap/doorbell.c"`,
		"//llgo:coro noblock\n//go:linkname nativeCDoorbellOpen C.__llgo_coro_doorbell_open_v1",
		"//llgo:coro noblock\n//go:linkname nativeCDoorbellRead C.__llgo_coro_doorbell_read_v1",
		"//llgo:coro noblock\n//go:linkname nativeCDoorbellWrite C.__llgo_coro_doorbell_write_v1",
		"//llgo:coro noblock\n//go:linkname nativeCDoorbellClose C.__llgo_coro_doorbell_close_v1",
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
		if !strings.Contains(text, "//llgo:coro schedulerwait\n//go:linkname nativeCPoll C.__llgo_coro_doorbell_poll_one_v1") {
			t.Errorf("%s does not restrict the exact one-fd poll leaf to the scheduler stack", path)
		}
		if strings.Contains(text, "//llgo:coro noblock\n//go:linkname nativeCPoll ") ||
			strings.Contains(text, "//llgo:coro sync\n//go:linkname nativeCPoll ") {
			t.Errorf("%s overstates the blocking one-fd poll capability", path)
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
		if !strings.Contains(text, "//llgo:coro schedulerwait\n//go:linkname nativeCPollSet C.__llgo_coro_doorbell_poll_set_v1") {
			t.Errorf("%s does not restrict the bounded owner poll leaf to the scheduler stack", path)
		}
		if strings.Contains(text, "//llgo:coro noblock\n//go:linkname nativeCPollSet ") ||
			strings.Contains(text, "//llgo:coro sync\n//go:linkname nativeCPollSet ") {
			t.Errorf("%s overstates the blocking poll-set capability", path)
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
