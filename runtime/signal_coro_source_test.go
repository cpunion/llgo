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
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	runtimeSignalLegacySource = "internal/lib/runtime/signal_llgo.go"
	runtimeSignalCoroSource   = "internal/lib/runtime/signal_coro_llgo.go"
	runtimeSignalCSource      = "internal/lib/runtime/_wrap/signal.c"
	runtimeFaultLegacySource  = "internal/lib/runtime/fault_unwind_llgo.go"
	runtimeFaultCoroSource    = "internal/lib/runtime/fault_unwind_coro_llgo.go"
)

func TestRuntimeSignalCoroAdapterIsSignalSafeAndEventDriven(t *testing.T) {
	legacy := readRuntimePollFile(t, runtimeSignalLegacySource)
	if !strings.Contains(legacy, "coro_runtime_adapter_test") ||
		!strings.Contains(legacy, "!llgo_coro_native_timer") {
		t.Fatalf("%s does not exclude the complete native coroutine profile", runtimeSignalLegacySource)
	}
	faultLegacy := readRuntimePollFile(t, runtimeFaultLegacySource)
	if !strings.Contains(faultLegacy, "coro_runtime_adapter_test") ||
		!strings.Contains(faultLegacy, "!llgo_coro_native_timer") {
		t.Fatalf("%s does not exclude the complete native coroutine profile", runtimeFaultLegacySource)
	}
	faultCoro := readRuntimePollFile(t, runtimeFaultCoroSource)
	if !strings.Contains(faultCoro, "func faultTraceback(skip int) bool") ||
		strings.Contains(faultCoro, "c_installFaultHandler") || strings.Contains(faultCoro, "rtdebug.PanicSignal") {
		t.Fatalf("%s does not provide the signal-stack-free traceback fallback", runtimeFaultCoroSource)
	}

	coro := readRuntimePollFile(t, runtimeSignalCoroSource)
	requireRuntimeAnnotationFreeCDeclarations(
		t,
		runtimeSignalCoroSource,
		"coroSignalGenerationNativeV1",
		"coroSignalIdleNativeV1",
	)
	for _, required := range []string{
		"C.__llgo_runtime_signal_init_v1",
		"C.__llgo_runtime_signal_enable_v1",
		"C.__llgo_runtime_signal_disable_v1",
		"C.__llgo_runtime_signal_ignore_v1",
		"C.__llgo_runtime_signal_ignored_v1",
		"C.__llgo_runtime_signal_receive_v1",
		"C.__llgo_runtime_signal_generation_v1",
		"C.__llgo_runtime_signal_idle_v1",
		"poll_runtime_pollOpen(uintptr(fd))",
		"poll_runtime_pollWait(coroSignalPollContextV1, 'r')",
		"target := coroSignalGenerationNativeV1()",
		"coroSchedulerYield()",
	} {
		if !strings.Contains(coro, required) {
			t.Errorf("%s lacks event-driven signal marker %q", runtimeSignalCoroSource, required)
		}
	}
	for _, forbidden := range []string{
		"libuv",
		"pthread",
		"psync.",
		".Cond",
		"make(",
		"append(",
	} {
		if strings.Contains(coro, forbidden) {
			t.Errorf("%s retains forbidden blocking/dynamic signal mechanism %q", runtimeSignalCoroSource, forbidden)
		}
	}

	cSource := readRuntimePollFile(t, runtimeSignalCSource)
	for _, required := range []string{
		"sigaction((int)sig",
		"O_NONBLOCK",
		"FD_CLOEXEC",
		"write(llgo_signal_write_fd_v1, &sig, sizeof(sig))",
		"atomic_compare_exchange_strong_explicit",
		"llgo_signal_published_v1",
		"llgo_signal_acknowledged_v1",
		"llgo_signal_idle_generation_v1",
		"llgo_signal_delivering_v1",
		"llgo_signal_generation_reached_v1(acknowledged, published)",
		"Recover publications whose non-blocking handler write overflowed",
	} {
		if !strings.Contains(cSource, required) {
			t.Errorf("%s lacks signal-safe publication marker %q", runtimeSignalCSource, required)
		}
	}
	handlerStart := strings.Index(cSource, "static void llgo_signal_handler_v1(int signum)")
	if handlerStart < 0 {
		t.Fatal("cannot locate bounded native signal handler")
	}
	handlerEnd := strings.Index(cSource[handlerStart:], "static int llgo_signal_set_fd_flags_v1")
	if handlerEnd < 0 {
		t.Fatal("cannot locate end of bounded native signal handler")
	}
	handler := cSource[handlerStart : handlerStart+handlerEnd]
	for _, forbidden := range []string{"malloc", "pthread", "uv_", "sigaction", "printf"} {
		if strings.Contains(handler, forbidden) {
			t.Errorf("native POSIX signal handler contains forbidden operation %q", forbidden)
		}
	}

	manifest := readRuntimePollFile(t, "internal/lib/runtime/runtime_default.go")
	if !strings.Contains(manifest, "_wrap/signal.c") {
		t.Fatal("non-baremetal runtime C manifest omits the native signal adapter")
	}
}

func TestRuntimePanicPCSnapshotIsLazyAndSignalSafe(t *testing.T) {
	runtime2 := readRuntimePollFile(t, "internal/runtime/runtime2.go")
	if !strings.Contains(runtime2, "panicPCs *panicPCStore") ||
		strings.Contains(runtime2, "panicPCs panicPCStore") {
		t.Fatal("runtime G does not keep the bounded panic PC payload out of line")
	}

	context := readRuntimePollFile(t, "internal/runtime/runtime_context.go")
	for _, required := range []string{
		"var signalPanicPCStore panicPCStore",
		"func ensurePanicPCStore(gp *g) *panicPCStore",
		"raw := AllocRoot(size)",
		"func signalSafePanicPCStore(gp *g) *panicPCStore",
		"gp.panicPCs = &signalPanicPCStore",
		"if store == &signalPanicPCStore",
	} {
		if !strings.Contains(context, required) {
			t.Errorf("lazy panic PC ownership lacks %q", required)
		}
	}

	caller := readRuntimePollFile(t, "internal/runtime/caller.go")
	for _, required := range []string{
		"func StoreSignalFaultPCs(pcs []uintptr)",
		"p := signalSafePanicPCStore(getg())",
		"storePanicPCsInto(p, pcs, 1)",
		"p := ensurePanicPCStore(getg())",
	} {
		if !strings.Contains(caller, required) {
			t.Errorf("panic PC capture lacks %q", required)
		}
	}

	faultLegacy := readRuntimePollFile(t, runtimeFaultLegacySource)
	if !strings.Contains(faultLegacy, "rtdebug.StoreSignalFaultPCs(faultPCs[:n])") ||
		strings.Contains(faultLegacy, "rtdebug.StoreFaultPCs(faultPCs[:n])") {
		t.Fatal("legacy signal callback can reach the allocating panic PC path")
	}
}

func TestRuntimeSignalSourceSelectionUsesCompleteNativeCapability(t *testing.T) {
	native := []string{"llgo", "llgo_coro", "llgo_coro_native_pipe", "llgo_coro_native_timer"}
	tests := []struct {
		name      string
		goos      string
		buildTags []string
		legacy    bool
		coro      bool
	}{
		{name: "ordinary linux", goos: "linux", legacy: true},
		{name: "ordinary darwin", goos: "darwin", legacy: true},
		{name: "partial coroutine profile", goos: "linux", buildTags: []string{"llgo", "llgo_coro"}, legacy: true},
		{name: "missing pipe", goos: "linux", buildTags: []string{"llgo", "llgo_coro", "llgo_coro_native_timer"}, legacy: true},
		{name: "missing timer", goos: "linux", buildTags: []string{"llgo", "llgo_coro", "llgo_coro_native_pipe"}, legacy: true},
		{name: "complete linux", goos: "linux", buildTags: native, coro: true},
		{name: "complete darwin", goos: "darwin", buildTags: native, coro: true},
		{name: "adapter selects legacy", goos: "linux", buildTags: append(slices.Clone(native), "coro_runtime_adapter_test"), legacy: true},
		{name: "baremetal owns signals", goos: "linux", buildTags: append(slices.Clone(native), "baremetal")},
		{name: "unsupported target keeps legacy", goos: "windows", buildTags: native, legacy: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = test.goos
			ctx.GOARCH = "amd64"
			ctx.BuildTags = slices.Clone(test.buildTags)
			for file, want := range map[string]bool{
				filepath.Base(runtimeSignalLegacySource): test.legacy,
				filepath.Base(runtimeSignalCoroSource):   test.coro,
				filepath.Base(runtimeFaultLegacySource):  test.legacy,
				filepath.Base(runtimeFaultCoroSource):    test.coro,
			} {
				got, err := ctx.MatchFile(filepath.Dir(runtimeSignalCoroSource), file)
				if err != nil {
					t.Fatalf("MatchFile(%q): %v", file, err)
				}
				if got != want {
					t.Errorf("MatchFile(%q) = %t, want %t", file, got, want)
				}
			}
		})
	}
}

func TestRuntimeSignalFixedCAdapter(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("native signal adapter is POSIX-only on %s", runtime.GOOS)
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("host C compiler is unavailable")
	}

	dir := t.TempDir()
	testSource := filepath.Join(dir, "signal_adapter_test.c")
	program := `
#define _POSIX_C_SOURCE 200809L
#include <fcntl.h>
#include <signal.h>
#include <stdint.h>

#define SIGNAL_NONE UINT32_MAX

int32_t __llgo_runtime_signal_init_v1(void);
void __llgo_runtime_signal_enable_v1(uint32_t);
void __llgo_runtime_signal_disable_v1(uint32_t);
void __llgo_runtime_signal_ignore_v1(uint32_t);
uint32_t __llgo_runtime_signal_ignored_v1(uint32_t);
uint32_t __llgo_runtime_signal_receive_v1(void);
uint32_t __llgo_runtime_signal_generation_v1(void);
uint32_t __llgo_runtime_signal_idle_v1(uint32_t);

static volatile sig_atomic_t previous_seen;

static void previous_handler(int sig) {
    previous_seen = sig;
}

int main(void) {
    struct sigaction previous = {0};
    struct sigaction fault_before = {0};
    struct sigaction fault_after = {0};
    int32_t fd;
    uint32_t target;

    previous.sa_handler = previous_handler;
    sigemptyset(&previous.sa_mask);
    if (sigaction(SIGUSR1, &previous, 0) != 0 ||
        signal(SIGUSR2, SIG_IGN) == SIG_ERR ||
        sigaction(SIGSEGV, 0, &fault_before) != 0) {
        return 10;
    }

    fd = __llgo_runtime_signal_init_v1();
    if (fd < 0 || (fcntl(fd, F_GETFL, 0) & O_NONBLOCK) == 0 ||
        (fcntl(fd, F_GETFD, 0) & FD_CLOEXEC) == 0) {
        return 11;
    }
    if (sigaction(SIGSEGV, 0, &fault_after) != 0 ||
        fault_before.sa_handler != fault_after.sa_handler ||
        fault_before.sa_flags != fault_after.sa_flags) {
        return 12; /* init must not steal llgo fault-handler ownership */
    }
    if (__llgo_runtime_signal_ignored_v1(SIGUSR2) == 0) {
        return 21; /* inherited ignored dispositions remain observable */
    }

    __llgo_runtime_signal_enable_v1(SIGUSR1);
    if (raise(SIGUSR1) != 0 || raise(SIGUSR1) != 0 ||
        __llgo_runtime_signal_generation_v1() != 1) {
        return 13;
    }
    target = __llgo_runtime_signal_generation_v1();
    if (__llgo_runtime_signal_receive_v1() != SIGUSR1) {
        return 14;
    }
    if (__llgo_runtime_signal_idle_v1(target) != 0) {
        return 15; /* acknowledgement alone is not receiver idle */
    }
    if (__llgo_runtime_signal_receive_v1() != SIGNAL_NONE ||
        __llgo_runtime_signal_idle_v1(target) == 0) {
        return 16;
    }

    __llgo_runtime_signal_ignore_v1(SIGUSR1);
    if (__llgo_runtime_signal_ignored_v1(SIGUSR1) == 0 ||
        raise(SIGUSR1) != 0 ||
        __llgo_runtime_signal_receive_v1() != SIGNAL_NONE) {
        return 17;
    }

    __llgo_runtime_signal_enable_v1(SIGUSR1);
    if (__llgo_runtime_signal_ignored_v1(SIGUSR1) != 0 || raise(SIGUSR1) != 0) {
        return 18;
    }
    target = __llgo_runtime_signal_generation_v1();
    if (__llgo_runtime_signal_receive_v1() != SIGUSR1 ||
        __llgo_runtime_signal_receive_v1() != SIGNAL_NONE ||
        __llgo_runtime_signal_idle_v1(target) == 0) {
        return 19;
    }

    __llgo_runtime_signal_disable_v1(SIGUSR1);
    previous_seen = 0;
    if (raise(SIGUSR1) != 0 || previous_seen != SIGUSR1) {
        return 20; /* disable restores the pre-existing disposition */
    }
    return 0;
}
`
	if err := os.WriteFile(testSource, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs(runtimeSignalCSource)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "signal_adapter_test")
	compile := exec.Command(cc, "-std=c11", "-Wall", "-Wextra", "-Werror", adapter, testSource, "-o", executable)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile native signal adapter: %v\n%s", err, output)
	}
	if output, err := exec.Command(executable).CombinedOutput(); err != nil {
		t.Fatalf("run native signal adapter: %v\n%s", err, output)
	}
}
