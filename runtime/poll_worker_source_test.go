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
	runtimePollGoSource = "internal/lib/runtime/poll_linkname_llgo.go"
	runtimePollCSource  = "internal/lib/runtime/_wrap/poll.c"
)

func TestRuntimePollWaitUsesFixedSyscallWorkerABI(t *testing.T) {
	goSource := readRuntimePollFile(t, runtimePollGoSource)
	for _, required := range []string{
		"//go:linkname pollFuncPCABI0 llgo.funcPCABI0",
		"//go:linkname pollSyscall llgo.syscall",
		"//go:linkname pollWaitFixedV1 C.__llgo_runtime_poll_wait_v1",
		"pollFuncPCABI0(pollWaitFixedV1)",
		"uintptr(uint32(timeout))",
		"n, errno := runtimePollWaitFixedV1(&fds[0], 2, timeout)",
		"if int(errno) == int(csyscall.EINTR)",
		"With the worker capability disabled, llgo.syscall keeps its legacy direct",
	} {
		if !strings.Contains(goSource, required) {
			t.Errorf("%s lacks fixed worker ABI marker %q", runtimePollGoSource, required)
		}
	}
	for _, forbidden := range []string{
		"//go:linkname c_poll C.poll",
		"cliteos.Errno()",
	} {
		if strings.Contains(goSource, forbidden) {
			t.Errorf("%s retains executor-thread poll/TLS errno path %q", runtimePollGoSource, forbidden)
		}
	}

	cSource := readRuntimePollFile(t, runtimePollCSource)
	for _, required := range []string{
		"uintptr_t __llgo_runtime_poll_wait_v1(",
		"nfds_t nfds = (nfds_t)nfds_word;",
		"int timeout = (int)(int32_t)(uint32_t)timeout_word;",
		"int result = poll(fds, nfds, timeout);",
		"return UINTPTR_MAX;",
	} {
		if !strings.Contains(cSource, required) {
			t.Errorf("%s lacks fixed poll wrapper marker %q", runtimePollCSource, required)
		}
	}
	// These two exact expressions freeze the -1 contract without executing an
	// unbounded wait: Go publishes 0xffffffff and C restores int32(-1) before
	// widening to the platform's int.
	if !strings.Contains(goSource, "uintptr(uint32(timeout))") ||
		!strings.Contains(cSource, "(int)(int32_t)(uint32_t)timeout_word") {
		t.Fatal("poll timeout -1 low-32-bit round trip is not explicit at both ABI ends")
	}

	manifest := readRuntimePollFile(t, "internal/lib/runtime/runtime_default.go")
	if !strings.Contains(manifest, "_wrap/poll.c") {
		t.Fatal("non-baremetal runtime C manifest does not include the fixed poll wrapper")
	}
}

func TestRuntimePollFixedCWrapper(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("poll wrapper is POSIX-only on %s", runtime.GOOS)
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("host C compiler is unavailable")
	}
	dir := t.TempDir()
	testSource := filepath.Join(dir, "poll_wrapper_test.c")
	program := `
#include <errno.h>
#include <poll.h>
#include <stdint.h>
#include <unistd.h>

uintptr_t __llgo_runtime_poll_wait_v1(uintptr_t, uintptr_t, uintptr_t);

int main(void) {
    int pipefd[2];
    if (pipe(pipefd) != 0) {
        return 10;
    }
    struct pollfd fd = { .fd = pipefd[0], .events = POLLIN, .revents = 0 };
    if (__llgo_runtime_poll_wait_v1((uintptr_t)&fd, 1, 0) != 0) {
        return 11;
    }
    const char byte = 'x';
    if (write(pipefd[1], &byte, 1) != 1) {
        return 12;
    }
    fd.revents = 0;
    if (__llgo_runtime_poll_wait_v1((uintptr_t)&fd, 1, UINT32_MAX) != 1 ||
        (fd.revents & POLLIN) == 0) {
        return 13;
    }
    errno = 0;
    if (__llgo_runtime_poll_wait_v1(0, 1, 0) != UINTPTR_MAX || errno == 0) {
        return 14;
    }
    if (close(pipefd[0]) != 0 || close(pipefd[1]) != 0) {
        return 15;
    }
    return 0;
}
`
	if err := os.WriteFile(testSource, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper, err := filepath.Abs(runtimePollCSource)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "poll_wrapper_test")
	compile := exec.Command(cc, "-std=c11", "-Wall", "-Wextra", "-Werror", wrapper, testSource, "-o", executable)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile fixed poll wrapper: %v\n%s", err, output)
	}
	run := exec.Command(executable)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run fixed poll wrapper: %v\n%s", err, output)
	}
}

func readRuntimePollFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
