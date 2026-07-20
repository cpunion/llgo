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
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const runtimeCoroPollInlinePatchSource = "_patch/internal/poll/fd_unix_coro_native_llgo.go"

func TestRuntimeCoroPollInlineAttemptContractAndLifetimeSource(t *testing.T) {
	runtimeSource := readRuntimePollFile(t, runtimeCoroPollGoSource)
	for _, marker := range []string{
		"inlineStream bool",
		"return true, pollCoroFDStreamLeafV1(fd), 0",
		"case uint32(csyscall.S_IFIFO), uint32(csyscall.S_IFCHR):",
		"return true, false, 0",
		"pd := &llgoPollDesc{fd: int32(fd), inlineStream: inlineAttempt}",
		"progress=executor-safe affinity=any-thread reentry=none memory=by-value",
		"progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return",
		"C.__llgo_runtime_poll_fd_stream_v1",
		"C.__llgo_runtime_poll_read_attempt_v1",
		"C.__llgo_runtime_poll_write_attempt_v1",
		"func pollCoroFDStreamLeafV1(",
		"func pollCoroReadAttemptV1(",
		"func pollCoroWriteAttemptV1(",
		"return result, errno, true",
		"!pd.inlineStream",
		"uintptr(fd) > uintptr(^uint32(0)>>1)",
		"return pollCoroReadAttemptV1(pd.fd, address, uintptr(size))",
		"return pollCoroWriteAttemptV1(pd.fd, address, uintptr(size))",
		"sequenced-packet, and raw sockets retain read/write on the worker path",
	} {
		if !strings.Contains(runtimeSource, marker) {
			t.Errorf("%s lacks inline-attempt marker %q", runtimeCoroPollGoSource, marker)
		}
	}
	for _, forbidden := range []string{
		"leaf func(",
		"pollFuncPCABI0(llgoCoroPollReadAttemptPackedV1)",
		"pollFuncPCABI0(llgoCoroPollWriteAttemptPackedV1)",
		"cliteos.Errno()",
		"NonblockingLease",
		"readAttempt",
		"writeAttempt",
		"RetireNonblocking",
		"F_GETFL",
		"O_NONBLOCK",
		"fd_nonblocking",
		"inline-progress=",
		"scope=wrapper",
	} {
		if strings.Contains(runtimeSource, forbidden) {
			t.Errorf("%s retains address/reverse/TLS inline-attempt path %q", runtimeCoroPollGoSource, forbidden)
		}
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), runtimeCoroPollGoSource, runtimeSource, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range parsed.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			functions[function.Name.Name] = function
		}
	}
	for _, test := range []struct {
		name   string
		target string
	}{
		{"pollCoroFDStreamLeafV1", "llgoCoroPollFDStreamV1"},
		{"pollCoroReadAttemptV1", "llgoCoroPollReadAttemptPackedV1"},
		{"pollCoroWriteAttemptV1", "llgoCoroPollWriteAttemptPackedV1"},
	} {
		function := functions[test.name]
		if function == nil || function.Body == nil {
			t.Fatalf("missing inline wrapper %s", test.name)
		}
		body := coroTimerOwnerNodeText(t, function.Body)
		if strings.Count(body, test.target) != 1 {
			t.Fatalf("%s C target count = %d, want 1:\n%s", test.name, strings.Count(body, test.target), body)
		}
		for _, forbidden := range []string{
			"pollRootGet", "throw(", "pollCoroWait", "runtime_poll", "go func", "defer ",
			"Acquire", "Release", "AllocZ",
		} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s executor-safe body contains forbidden %q:\n%s", test.name, forbidden, body)
			}
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch node.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				t.Errorf("%s executor-safe body contains a cycle", test.name)
			}
			return true
		})
	}

	patch := readRuntimePollFile(t, runtimeCoroPollInlinePatchSource)
	for _, marker := range []string{
		"func (fd *FD) Read(p []byte) (int, error)",
		"func (fd *FD) Write(p []byte) (int, error)",
		"return syscall.Read(fd.Sysfd, p)",
		"return syscall.Write(fd.Sysfd, p)",
		"if err == syscall.EINTR",
		"fd.pd.waitRead(fd.isFile)",
		"fd.pd.waitWrite(fd.isFile)",
		"if err := fd.readLock(); err != nil",
		"if err := fd.writeLock(); err != nil",
	} {
		if !strings.Contains(patch, marker) {
			t.Errorf("%s lacks standard-library inline-attempt marker %q", runtimeCoroPollInlinePatchSource, marker)
		}
	}
	for _, forbidden := range []string{
		"func (fd *FD) SetBlocking() error",
		"func (fd *FD) RawControl(f func(uintptr)) error",
		"func (fd *FD) RawRead(f func(uintptr) bool) error",
		"func (fd *FD) RawWrite(f func(uintptr) bool) error",
		"func (fd *FD) Dup() (int, string, error)",
		"RetireNonblocking", "NonblockingLease",
	} {
		if strings.Contains(patch, forbidden) {
			t.Errorf("%s retains obsolete descriptor-mode patch %q", runtimeCoroPollInlinePatchSource, forbidden)
		}
	}
}

func TestRuntimeCoroPollPackedInlineAttemptCWrapper(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("poll inline-attempt wrapper is POSIX-only on %s", runtime.GOOS)
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("host C compiler is unavailable")
	}
	cSource := readRuntimePollFile(t, runtimePollCSource)
	for _, marker := range []string{
		"#include <sys/socket.h>",
		"__llgo_runtime_poll_fd_stream_v1",
		"getsockopt(",
		"SOL_SOCKET",
		"SO_TYPE",
		"socket_type == SOCK_STREAM",
		"LLGO_RUNTIME_POLL_MAX_INLINE_ATTEMPT_V1",
		"llgo_runtime_poll_bounded_size_v1(size)",
		"MSG_DONTWAIT",
		"recv(",
		"send(",
	} {
		if !strings.Contains(cSource, marker) {
			t.Errorf("%s lacks bounded stream-attempt marker %q", runtimePollCSource, marker)
		}
	}
	for _, forbidden := range []string{
		"MSG_NOSIGNAL", "F_GETFL", "O_NONBLOCK", "fd_nonblocking", "fcntl(",
		"ssize_t result = read(", "ssize_t result = write(",
	} {
		if strings.Contains(cSource, forbidden) {
			t.Errorf("%s retains obsolete or incompatible inline-attempt marker %q", runtimePollCSource, forbidden)
		}
	}
	dir := t.TempDir()
	testSource := filepath.Join(dir, "poll_inline_attempt_test.c")
	program := `
	#include <errno.h>
	#include <fcntl.h>
	#include <stdint.h>
	#include <stdlib.h>
	#include <string.h>
	#include <sys/socket.h>
	#include <unistd.h>

	uint64_t __llgo_runtime_poll_read_attempt_v1(int32_t, void *, uintptr_t);
	uint64_t __llgo_runtime_poll_write_attempt_v1(int32_t, void *, uintptr_t);
	uint32_t __llgo_runtime_poll_fd_stream_v1(int32_t);

	static int32_t result_word(uint64_t packed) { return (int32_t)(uint32_t)packed; }
	static uint32_t errno_word(uint64_t packed) { return (uint32_t)(packed >> 32); }
	static int would_block(uint32_t error) { return error == EAGAIN || error == EWOULDBLOCK; }

	int main(void) {
	    int stream[2];
	    int datagram[2];
	    int pipefd[2];
	    char byte = 0;
	    uint64_t packed;
	    alarm(5);
	    if (socketpair(AF_UNIX, SOCK_STREAM, 0, stream) != 0) return 10;
	    if ((fcntl(stream[0], F_GETFL, 0) & O_NONBLOCK) != 0 ||
	        (fcntl(stream[1], F_GETFL, 0) & O_NONBLOCK) != 0) return 11;
	    errno = EDOM;
	    if (__llgo_runtime_poll_fd_stream_v1((int32_t)stream[0]) != 1 || errno != EDOM) return 12;
	    errno = EDOM;
	    packed = __llgo_runtime_poll_read_attempt_v1((int32_t)stream[0], &byte, 1);
	    if (result_word(packed) != -1 || !would_block(errno_word(packed)) || errno != EDOM) return 13;
	    byte = 'x';
	    errno = ERANGE;
	    packed = __llgo_runtime_poll_write_attempt_v1((int32_t)stream[1], &byte, 1);
	    if (result_word(packed) != 1 || errno_word(packed) != 0 || errno != ERANGE) return 14;
	    byte = 0;
	    errno = EDOM;
	    packed = __llgo_runtime_poll_read_attempt_v1((int32_t)stream[0], &byte, 1);
	    if (result_word(packed) != 1 || errno_word(packed) != 0 || byte != 'x' || errno != EDOM) return 15;
	    errno = ERANGE;
	    packed = __llgo_runtime_poll_read_attempt_v1(-1, &byte, 1);
	    if (result_word(packed) != -1 || errno_word(packed) != EBADF || errno != ERANGE) return 16;

	    char *large = (char *)malloc(64u << 10);
	    if (large == NULL) return 17;
	    memset(large, 'z', 64u << 10);
	    int flags = fcntl(stream[1], F_GETFL, 0);
	    if (flags < 0 || fcntl(stream[1], F_SETFL, flags | O_NONBLOCK) != 0) return 18;
	    packed = __llgo_runtime_poll_write_attempt_v1(
	        (int32_t)stream[1], large, (uintptr_t)((64u << 10) + 4096u));
	    if (result_word(packed) <= 0 || result_word(packed) > (int32_t)(64u << 10) || errno_word(packed) != 0) return 19;
	    free(large);

	    if (socketpair(AF_UNIX, SOCK_DGRAM, 0, datagram) != 0) return 20;
	    if (__llgo_runtime_poll_fd_stream_v1((int32_t)datagram[0]) != 0) return 21;
	    if (pipe(pipefd) != 0 || __llgo_runtime_poll_fd_stream_v1((int32_t)pipefd[0]) != 0) return 22;
	    int nullfd = open("/dev/null", O_RDONLY);
	    if (nullfd < 0 || __llgo_runtime_poll_fd_stream_v1((int32_t)nullfd) != 0) return 23;
	    int regular = open("poll_regular_probe.tmp", O_CREAT | O_RDWR | O_TRUNC, 0600);
	    if (regular < 0 || __llgo_runtime_poll_fd_stream_v1((int32_t)regular) != 0) return 24;
	    unlink("poll_regular_probe.tmp");
	#ifdef SOCK_SEQPACKET
	    int packet[2];
	    if (socketpair(AF_UNIX, SOCK_SEQPACKET, 0, packet) == 0) {
	        if (__llgo_runtime_poll_fd_stream_v1((int32_t)packet[0]) != 0) return 25;
	        close(packet[0]);
	        close(packet[1]);
	    }
	#endif

	#ifdef __linux__
	    /* Linux read(2) leaves a pending zero-length datagram queued, whereas
	       recv(2) consumes it. Datagram sockets therefore must stay on the exact
	       syscall.Read worker path instead of this recv-based stream fast path. */
	    if (send(datagram[1], "", 0, 0) != 0) return 26;
	    if (read(datagram[0], &byte, 1) != 0) return 27;
	    if (recv(datagram[0], &byte, 1, MSG_DONTWAIT) != 0) return 28;
	    if (recv(datagram[0], &byte, 1, MSG_DONTWAIT) != -1 ||
	        (errno != EAGAIN && errno != EWOULDBLOCK)) return 29;
	#endif

	    if (close(stream[0]) != 0 || close(stream[1]) != 0 ||
	        close(datagram[0]) != 0 || close(datagram[1]) != 0 ||
	        close(pipefd[0]) != 0 || close(pipefd[1]) != 0 ||
	        close(nullfd) != 0 || close(regular) != 0) return 30;
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
	executable := filepath.Join(dir, "poll_inline_attempt_test")
	compile := exec.Command(cc, "-std=c11", "-Wall", "-Wextra", "-Werror", wrapper, testSource, "-o", executable)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile poll inline-attempt wrapper: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil {
		t.Fatalf("run poll inline-attempt wrapper: %v\n%s", err, output)
	}
}
