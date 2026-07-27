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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLinuxSyscallWorkerAuthorityRequiresExactTrapPolicy(t *testing.T) {
	goPath := "internal/lib/syscall/syscall_linux_coro.go"
	goSource, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`LLGoPackage = true`,
		`LLGoFiles   = "_wrap/syscall_linux.c"`,
		"//go:linkname llgoLinuxFuncPCABI0 llgo.funcPCABI0",
		"//go:linkname llgoLinuxSyscall4 llgo.syscall",
		"//go:linkname llgoLinuxSyscall7 llgo.syscall",
		"llgoLinuxFuncPCABI0(libc___llgo_linux_syscall3_v1_trampoline)",
		"llgoLinuxFuncPCABI0(libc___llgo_linux_syscall6_v1_trampoline)",
		"func Syscall(",
		"func Syscall6(",
		"func RawSyscall(",
		"func RawSyscall6(",
		"if r1 == ^uintptr(0)",
		"fixed C leaves supply the exact callable identity",
		"llgo.syscall sinks derive their word widths",
		"//go:linkname libc___llgo_linux_syscall3_v1_trampoline C.__llgo_linux_syscall3_v1",
		"//go:linkname libc___llgo_linux_syscall6_v1_trampoline C.__llgo_linux_syscall6_v1",
		"target-owned Linux trap policy on every active managed incoming edge",
		"active managed incoming edge",
		"Dynamic, fork, exec, exit, and other",
		"process-control trap numbers have no worker certificate",
	} {
		if !strings.Contains(string(goSource), required) {
			t.Errorf("%s lacks fail-closed dynamic-trap marker %q", goPath, required)
		}
	}
	for _, forbidden := range []string{
		"//llgo:coro workeraddr",
		"abi=word-call.v1/",
		"becomes a worker park",
		"uses the same worker handoff",
		"internal/runtime/syscall/linux",
		"runtime_entersyscall",
		"runtime_exitsyscall",
	} {
		if strings.Contains(string(goSource), forbidden) {
			t.Errorf("%s grants or describes forbidden dynamic-trap worker authority %q", goPath, forbidden)
		}
	}

	cPath := "internal/lib/syscall/_wrap/syscall_linux.c"
	cSource, err := os.ReadFile(cPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"uintptr_t __llgo_linux_syscall3_v1(",
		"uintptr_t __llgo_linux_syscall6_v1(",
		"long result = syscall(",
		"return result == -1L ? UINTPTR_MAX",
		"records the positive errno in the current",
		"invocation thread's TLS",
		"carry no",
		"standalone worker authority",
		"ProgramIR independently proves an exact",
		"target-safe constant trap",
	} {
		if !strings.Contains(string(cSource), required) {
			t.Errorf("%s lacks plain syscall adapter marker %q", cPath, required)
		}
	}
	for _, forbidden := range []string{"worker-local", "blanket worker"} {
		if strings.Contains(string(cSource), forbidden) {
			t.Errorf("%s retains stale dynamic-trap worker claim %q", cPath, forbidden)
		}
	}
}

func TestRuntimeWriteCarriesExactWorkerSafetyContract(t *testing.T) {
	commonPath := "internal/lib/runtime/runtime.go"
	commonSource, err := os.ReadFile(commonPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(commonSource), "return runtimeWrite(fd, p, n)") ||
		strings.Contains(string(commonSource), "C.write") {
		t.Fatalf("%s does not keep runtime.write behind its target-specific leaf", commonPath)
	}

	libcPath := "internal/lib/runtime/runtime_write_libc_llgo.go"
	libcSource, err := os.ReadFile(libcPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(libcSource), "//go:linkname c_write C.write") {
		t.Fatalf("%s does not bind c_write to its exact typed C declaration", libcPath)
	}
	if strings.Contains(string(libcSource), "//llgo:coro worker") {
		t.Fatalf("%s retains a redundant worker directive instead of the typed-C default", libcPath)
	}

	freestandingPath := "internal/lib/runtime/runtime_write_freestanding_webassembly_llgo.go"
	freestandingSource, err := os.ReadFile(freestandingPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"//go:build wasip2 || wasm_unknown",
		"func runtimeWrite(fd uintptr, p unsafe.Pointer, n int32) int32",
		"return n",
	} {
		if !strings.Contains(string(freestandingSource), marker) {
			t.Fatalf("%s lacks freestanding runtime.write marker %q", freestandingPath, marker)
		}
	}
	if strings.Contains(string(freestandingSource), "C.write") ||
		strings.Contains(string(freestandingSource), "//llgo:coro worker") {
		t.Fatalf("%s retains a libc worker dependency", freestandingPath)
	}

	osPath := "internal/clite/os/os.go"
	osSource, err := os.ReadFile(osPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(osSource), "func Write(fd c.Int, buf c.Pointer, count uintptr) c.SsizeT") {
		t.Fatalf("%s does not use the exact C ssize_t write result ABI", osPath)
	}
}

func TestDarwinSyscallFailureConventionsAreExplicit(t *testing.T) {
	path := "internal/lib/syscall/syscall_darwin_go126.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`const LLGoPackage = true`,
		"//go:linkname llgoDarwinFuncPCABI0 llgo.funcPCABI0",
		"//go:linkname libc_getrlimit_trampoline C.getrlimit",
		"//go:linkname libc_setrlimit_trampoline C.setrlimit",
		"//go:linkname libc_sysctl_trampoline C.sysctl",
		"//go:linkname libc_open_trampoline C.llgo_open",
		"//go:linkname libc_close_trampoline C.close",
		"//go:linkname libc_read_trampoline C.read",
		"//go:linkname libc_lseek_trampoline C.lseek",
		"//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/6+foreign-pointer-result=r1\n//go:linkname libc_mmap_trampoline C.mmap",
		"//go:linkname libc_munmap_trampoline C.munmap",
		"//go:linkname libc_fdopendir_trampoline C.fdopendir",
		"//go:linkname libc_writev_trampoline C.writev",
		"//go:linkname libc_fcntl_trampoline C.llgo_fcntl",
		"//go:linkname libc_fsync_trampoline C.fsync",
		"//go:linkname libc_fchdir_trampoline C.fchdir",
		"//go:linkname libc_pread_trampoline C.pread",
		"//go:linkname libc_pwrite_trampoline C.pwrite",
		"//go:linkname libc_openat_trampoline C.llgo_openat",
		"//go:linkname libc_getsockopt_trampoline C.getsockopt",
		"//go:linkname libc_setsockopt_trampoline C.setsockopt",
		"//go:linkname libc_utimensat_trampoline C.utimensat",
		"//go:linkname libc_sendto_trampoline C.sendto",
		"//go:linkname libc_recvfrom_trampoline C.recvfrom",
		"//go:linkname libc_wait4_trampoline C.wait4",
		"//go:linkname llgoSyscall3Int32 llgo.syscall32",
		"//go:linkname llgoSyscall6Int32 llgo.syscall32",
		"//go:linkname llgoSyscall9Int32 llgo.syscall32",
		"//go:linkname llgoSyscall3Word llgo.syscall",
		"//go:linkname llgoSyscall6Word llgo.syscall",
		"//go:linkname llgoSyscall3Pointer llgo.syscallPtr",
		"llgoSyscall3Int32(fn, a1, a2, a3)",
		"llgoSyscall3Word(fn, a1, a2, a3)",
		"llgoSyscall3Pointer(fn, a1, a2, a3)",
		"func syscall6X(",
	} {
		if !strings.Contains(string(source), required) {
			t.Errorf("%s lacks explicit failure-convention marker %q", path, required)
		}
	}
	if strings.Contains(string(source), "//llgo:coro workerresult") {
		t.Errorf("%s retains a worker result directive instead of SSA-derived result flow", path)
	}
	if strings.Contains(string(source), "//llgo:coro workeraddr") {
		t.Errorf("%s retains producer arity directives instead of sink-derived ABI", path)
	}

	publicPath := "internal/lib/syscall/syscall_darwin.go"
	publicSource, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"c_syscall(c.Long(trap), a1, a2, a3)",
		"c_syscall(c.Long(trap), a1, a2, a3, a4, a5, a6)",
		"c_syscall(c.Long(trap), a1, a2, a3, a4, a5, a6, a7, a8, a9)",
		"public trap API on its original direct/plain path",
		"exact constant-trap capability proof",
	} {
		if !strings.Contains(string(publicSource), required) {
			t.Errorf("%s lacks fail-closed public trap marker %q", publicPath, required)
		}
	}
	for _, forbidden := range []string{
		"llgoDarwinSyscall4Word",
		"llgoDarwinSyscall7Word",
		"libc___llgo_darwin_syscall3_v1_trampoline",
		"libc___llgo_darwin_syscall6_v1_trampoline",
	} {
		if strings.Contains(string(publicSource), forbidden) || strings.Contains(string(source), forbidden) {
			t.Errorf("public dynamic trap path retains unconditional worker authority %q", forbidden)
		}
	}

	cPath := "internal/coroworker/_worker/worker.c"
	cSource, err := os.ReadFile(cPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"__llgo_darwin_syscall3_v1",
		"__llgo_darwin_syscall6_v1",
		"syscall((int)number",
		"public syscall-number API",
	} {
		if strings.Contains(string(cSource), forbidden) {
			t.Errorf("%s retains unsafe dynamic-trap worker shim %q", cPath, forbidden)
		}
	}
}

func TestDarwinRuntimeSyscallLinknameWrappersStayManaged(t *testing.T) {
	files := map[string][]string{
		"internal/lib/runtime/syscall_darwin_go126_llgo.go": {
			"syscall_syscalln syscall.syscalln",
			"syscall_rawsyscalln syscall.rawsyscalln",
		},
		"internal/lib/runtime/syscall_darwin_llgo.go": {
			"syscall_syscall syscall.syscall",
			"syscall_syscall6 syscall.syscall6",
			"syscall_syscall6X syscall.syscall6X",
			"syscall_syscallPtr syscall.syscallPtr",
			"syscall_syscallX syscall.syscallX",
			"syscall_syscall9 syscall.syscall9",
			"syscall_rawSyscall syscall.rawSyscall",
			"syscall_rawSyscall6 syscall.rawSyscall6",
		},
	}
	for path, wrappers := range files {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, wrapper := range wrappers {
			marker := "//llgo:managedlink\n//go:linkname " + wrapper
			if !strings.Contains(string(source), marker) {
				t.Errorf("%s lacks managed Go-facing syscall boundary %q", path, marker)
			}
		}
		for _, forbidden := range []string{
			"//llgo:managedlink\n//go:linkname llgo_rawSyscall ",
			"//llgo:managedlink\n//go:linkname llgo_rawSyscall6 ",
			"//llgo:managedlink\n//go:linkname llgo_rawSyscall9 ",
		} {
			if strings.Contains(string(source), forbidden) {
				t.Errorf("%s falsely certifies the dynamic foreign syscall leaf %q", path, forbidden)
			}
		}
	}
}

func TestDarwinGeneratedWorkerCatalogCoversFileAndTCPWithoutUnsafeTransitions(t *testing.T) {
	path := "internal/lib/syscall/syscall_darwin_worker_catalog_go126.go"
	sourceBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	commonTargets := []struct {
		name     string
		physical string
	}{
		{"libc_accept_trampoline", "accept"},
		{"libc_bind_trampoline", "bind"},
		{"libc_chdir_trampoline", "chdir"},
		{"libc_chmod_trampoline", "chmod"},
		{"libc_chown_trampoline", "chown"},
		{"libc_closedir_trampoline", "closedir"},
		{"libc_connect_trampoline", "connect"},
		{"libc_dup_trampoline", "dup"},
		{"libc_fchmod_trampoline", "fchmod"},
		{"libc_fchown_trampoline", "fchown"},
		{"libc_ftruncate_trampoline", "ftruncate"},
		{"libc_getcwd_trampoline", "getcwd"},
		{"libc_getpid_trampoline", "getpid"},
		{"libc_getrusage_trampoline", "getrusage"},
		{"libc_getpeername_trampoline", "getpeername"},
		{"libc_getsockname_trampoline", "getsockname"},
		{"libc_kill_trampoline", "kill"},
		{"libc_lchown_trampoline", "lchown"},
		{"libc_link_trampoline", "link"},
		{"libc_listen_trampoline", "listen"},
		{"libc_mkdir_trampoline", "mkdir"},
		{"libc_mprotect_trampoline", "mprotect"},
		{"libc_pipe_trampoline", "pipe"},
		{"libc_readlink_trampoline", "readlink"},
		{"libc_recvmsg_trampoline", "recvmsg"},
		{"libc_rename_trampoline", "rename"},
		{"libc_rmdir_trampoline", "rmdir"},
		{"libc_sendmsg_trampoline", "sendmsg"},
		{"libc_shutdown_trampoline", "shutdown"},
		{"libc_socket_trampoline", "socket"},
		{"libc_symlink_trampoline", "symlink"},
		{"libc_truncate_trampoline", "truncate"},
		{"libc_unlink_trampoline", "unlink"},
		{"libc_unlinkat_trampoline", "unlinkat"},
		{"libc_write_trampoline", "write"},
		{"libc_sendfile_trampoline", "sendfile"},
		{"libc_socketpair_trampoline", "socketpair"},
	}
	for _, target := range commonTargets {
		marker := "//go:linkname " + target.name + " C." + target.physical +
			"\nfunc " + target.name + "()"
		if !strings.Contains(source, marker) {
			t.Errorf("%s lacks exact producer catalog entry %q", path, marker)
		}
	}
	if got := strings.Count(source, "//go:linkname "); got != len(commonTargets) {
		t.Errorf("%s linkname entry count = %d, want exact common P0 manifest size %d", path, got, len(commonTargets))
	}

	archCatalogs := []struct {
		path    string
		goarch  string
		targets []struct {
			name     string
			physical string
		}
	}{
		{
			path:   "internal/lib/syscall/syscall_darwin_worker_catalog_arm64_go126.go",
			goarch: "arm64",
			targets: []struct {
				name     string
				physical string
			}{
				{"libc_fstat_trampoline", "fstat"},
				{"libc_lstat_trampoline", "lstat"},
				{"libc_stat_trampoline", "stat"},
				{"libc_fstatat_trampoline", "fstatat"},
			},
		},
		{
			path:   "internal/lib/syscall/syscall_darwin_worker_catalog_amd64_go126.go",
			goarch: "amd64",
			targets: []struct {
				name     string
				physical string
			}{
				{"libc_fstat64_trampoline", "fstat64"},
				{"libc_lstat64_trampoline", "lstat64"},
				{"libc_stat64_trampoline", "stat64"},
				{"libc_fstatat64_trampoline", "fstatat64"},
			},
		},
	}
	for _, catalog := range archCatalogs {
		archBytes, err := os.ReadFile(catalog.path)
		if err != nil {
			t.Fatal(err)
		}
		archSource := string(archBytes)
		if !strings.Contains(archSource, "//go:build darwin && "+catalog.goarch+" && go1.26") {
			t.Errorf("%s lacks exact architecture build constraint", catalog.path)
		}
		for _, target := range catalog.targets {
			marker := "//go:linkname " + target.name + " C." + target.physical +
				"\nfunc " + target.name + "()"
			if !strings.Contains(archSource, marker) {
				t.Errorf("%s lacks exact architecture producer entry %q", catalog.path, marker)
			}
		}
		if got := strings.Count(archSource, "//go:linkname "); got != len(catalog.targets) {
			t.Errorf("%s linkname entry count = %d, want %d", catalog.path, got, len(catalog.targets))
		}
		if strings.Contains(archSource, "//llgo:coro") {
			t.Errorf("%s retains derivable coroutine directives", catalog.path)
		}
	}
	for _, forbidden := range []string{
		"//llgo:coro workeraddr",
		"//llgo:coro contract",
		"func libc_fstat_trampoline()",
		"func libc_lstat_trampoline()",
		"func libc_stat_trampoline()",
		"func libc_fstat64_trampoline()",
		"func libc_lstat64_trampoline()",
		"func libc_stat64_trampoline()",
		"func libc_fstatat_trampoline()",
		"func libc_fstatat64_trampoline()",
		"libc_fork_trampoline",
		"libc_execve_trampoline",
		"libc_exit_trampoline",
		"libc_pthread_",
		"llgo.syscall",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s grants or consumes forbidden worker authority %q", path, forbidden)
		}
	}
	for _, required := range []string{
		"never recovers a target or policy from uintptr",
		"There are intentionally no entries for fork, execve, exit",
		"ioctl, kevent, and ptrace",
		"complete Darwin syscall catalog",
		"requires an exact physical symbol",
		"Every active sink must derive the same",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("%s lacks catalog invariant %q", path, required)
		}
	}
	legacyBytes, err := os.ReadFile("internal/lib/syscall/syscall_darwin_go126.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"libc_fstatat_trampoline", "libc_fstatat64_trampoline"} {
		if strings.Contains(string(legacyBytes), forbidden) {
			t.Errorf("common Darwin syscall declarations retain architecture-specific target %q", forbidden)
		}
	}
}

func TestDarwinInternalSyscallAtWorkerTargetsAreExact(t *testing.T) {
	path := "internal/lib/internal/syscall/unix/at_darwin_coro.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"//go:linkname libc_readlinkat_trampoline C.readlinkat",
		"//go:linkname libc_mkdirat_trampoline C.mkdirat",
		"//go:linkname libc_fchmodat_trampoline C.fchmodat",
		"//go:linkname libc_fchownat_trampoline C.fchownat",
		"//go:linkname libc_renameat_trampoline C.renameat",
		"//go:linkname libc_linkat_trampoline C.linkat",
		"//go:linkname libc_symlinkat_trampoline C.symlinkat",
		"//go:linkname libc_faccessat_trampoline C.faccessat",
	} {
		if !strings.Contains(string(source), required) {
			t.Errorf("%s lacks fixed at-family worker target %q", path, required)
		}
	}
	if strings.Contains(string(source), "//llgo:coro") {
		t.Errorf("%s retains derivable address-carrier directives", path)
	}
}

func TestDarwinInternalSyscallNetResolverWorkerTargetsAreExact(t *testing.T) {
	path := "internal/lib/internal/syscall/unix/net_darwin_coro.go"
	sourceBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	if !strings.Contains(source, "//go:build darwin && go1.26") {
		t.Errorf("%s lacks exact Darwin Go 1.26 build constraint", path)
	}
	targets := []struct {
		name                 string
		physical             string
		arity                int
		foreignPointerResult bool
	}{
		{name: "libc_getaddrinfo_trampoline", physical: "getaddrinfo", arity: 6},
		{name: "libc_freeaddrinfo_trampoline", physical: "freeaddrinfo", arity: 6},
		{name: "libc_getnameinfo_trampoline", physical: "getnameinfo", arity: 9},
		{name: "libc_gai_strerror_trampoline", physical: "gai_strerror", arity: 3, foreignPointerResult: true},
		{name: "libresolv_res_9_ninit_trampoline", physical: "libresolv_res_9_ninit", arity: 3},
		{name: "libresolv_res_9_nclose_trampoline", physical: "libresolv_res_9_nclose", arity: 3},
		{name: "libresolv_res_9_nsearch_trampoline", physical: "libresolv_res_9_nsearch", arity: 6},
	}
	for _, target := range targets {
		directive := ""
		if target.foreignPointerResult {
			directive = "//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/" +
				fmt.Sprint(target.arity) + "+foreign-pointer-result=r1\n"
		}
		marker := directive +
			"//go:linkname " + target.name + " C." + target.physical +
			"\nfunc " + target.name + "()"
		if !strings.Contains(source, marker) {
			t.Errorf("%s lacks exact resolver worker target %q", path, marker)
		}
	}
	if got := strings.Count(source, "//llgo:coro workeraddr "); got != 0 {
		t.Errorf("%s legacy worker target count = %d, want zero", path, got)
	}
	if got := strings.Count(source, "+foreign-pointer-result=r1"); got != 1 {
		t.Errorf("%s foreign-pointer result declarations = %d, want exactly gai_strerror", path, got)
	}
	if got := strings.Count(source, "//go:linkname "); got != len(targets) {
		t.Errorf("%s linkname count = %d, want exact resolver manifest size %d", path, got, len(targets))
	}
	for _, forbidden := range []string{"fork", "execve", "pthread", "llgo.syscall"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s grants or consumes unrelated worker authority %q", path, forbidden)
		}
	}
}

func TestDarwinReaddirWorkerTargetUsesArchitectureSymbol(t *testing.T) {
	for path, symbol := range map[string]string{
		"internal/lib/syscall/syscall_darwin_readdir_arm64_go126.go": "C.readdir_r",
		"internal/lib/syscall/syscall_darwin_readdir_amd64_go126.go": "C.readdir_r$INODE64",
	} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		marker := "//go:linkname libc_readdir_r_trampoline " + symbol
		if !strings.Contains(string(source), marker) {
			t.Errorf("%s lacks architecture-specific readdir worker target %q", path, marker)
		}
		if strings.Contains(string(source), "//llgo:coro") {
			t.Errorf("%s retains a derivable worker-address directive", path)
		}
	}
}

func TestDarwinRuntimeEnvironmentUsesFixedWorkerABI(t *testing.T) {
	path := "internal/lib/runtime/link_darwin_llgo.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"//go:linkname runtimeDarwinFuncPCABI0 llgo.funcPCABI0",
		"//go:linkname runtimeDarwinSyscall1Int32 llgo.syscall32",
		"//go:linkname runtimeDarwinSyscall3Int32 llgo.syscall32",
		"//go:linkname libc_setenv_trampoline C.setenv",
		"//go:linkname libc_unsetenv_trampoline C.unsetenv",
		"runtimeDarwinFuncPCABI0(libc_setenv_trampoline)",
		"runtimeDarwinFuncPCABI0(libc_unsetenv_trampoline)",
		"uintptr(unsafe.Pointer(name))",
		"without recovering policy from the emitted address",
		"irreversible worker completion",
		"//llgo:coro sync\n//go:linkname runtimeDarwinFcntl C.llgo_fcntl",
		"cliteos.Errno()",
	} {
		if !strings.Contains(string(source), required) {
			t.Errorf("%s lacks fixed environment worker marker %q", path, required)
		}
	}
	for _, forbidden := range []string{
		"runtimeDarwinFuncPCABI0(cliteos.Setenv)",
		"runtimeDarwinFuncPCABI0(cliteos.Unsetenv)",
		"syscall.llgoRuntimeFcntl",
		"abi=word-call.v1/",
		"//llgo:coro workeraddr",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("%s still derives worker authority from typed C declaration %q", path, forbidden)
		}
	}
	errnoSource, err := os.ReadFile("internal/clite/os/os.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(errnoSource), "//llgo:coro noblock\n//go:linkname Errno C.cliteErrno") {
		t.Error("the runtime-owned Darwin fcntl bridge cannot read TLS errno without a no-suspend certificate")
	}
}

func TestCoroWorkerCapturesRawErrno(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("native worker is unavailable on %s", runtime.GOOS)
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("host C compiler is unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "worker_errno_test.c")
	program := `
#include <errno.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

struct llgo_coro_worker_result_v1 {
    uintptr_t r1;
    uintptr_t r2;
    uintptr_t error;
};

bool __llgo_coro_worker_call_v1(
    uintptr_t function,
    uint32_t argc,
    const uintptr_t args[9],
    struct llgo_coro_worker_result_v1 *result);

uint32_t __llgo_coro_native_worker_complete_v1(
    uint32_t source_slot,
    uint32_t generation,
    uintptr_t r1,
    uintptr_t r2,
    uintptr_t error) {
    (void)source_slot;
    (void)generation;
    (void)r1;
    (void)r2;
    (void)error;
    return 0;
}

static int fail_int(uintptr_t ignored) {
    (void)ignored;
    errno = EAGAIN;
    return -1;
}

static uintptr_t fail_word(uintptr_t ignored) {
    (void)ignored;
    errno = EIO;
    return UINTPTR_MAX;
}

static void *fail_pointer(uintptr_t ignored) {
    (void)ignored;
    errno = ENOMEM;
    return NULL;
}

static uintptr_t success_with_errno(uintptr_t ignored) {
    (void)ignored;
    errno = EBUSY;
    return 7;
}

static int call_and_check(
    uintptr_t function,
    uintptr_t want_r1,
    uintptr_t want_errno,
    int base) {
    const uintptr_t args[9] = {0};
    struct llgo_coro_worker_result_v1 result = {0};
    if (!__llgo_coro_worker_call_v1(function, 1, args, &result)) {
        return base;
    }
    if (result.r1 != want_r1) {
        return base + 1;
    }
    if (result.error != want_errno) {
        return base + 2;
    }
    return 0;
}

int main(void) {
    int status = call_and_check(
        (uintptr_t)(void *)&fail_int,
        (uintptr_t)UINT32_MAX,
        (uintptr_t)EAGAIN,
        10);
    if (status != 0) {
        return status;
    }
    status = call_and_check(
        (uintptr_t)(void *)&fail_word,
        UINTPTR_MAX,
        (uintptr_t)EIO,
        20);
    if (status != 0) {
        return status;
    }
    status = call_and_check(
        (uintptr_t)(void *)&fail_pointer,
        0,
        (uintptr_t)ENOMEM,
        30);
    if (status != 0) {
        return status;
    }
    status = call_and_check(
        (uintptr_t)(void *)&success_with_errno,
        7,
        (uintptr_t)EBUSY,
        40);
    if (status != 0) {
        return status;
    }
    return 0;
}
`
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	worker, err := filepath.Abs("internal/coroworker/_worker/worker.c")
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "worker_errno_test")
	compile := exec.Command(cc, "-std=c11", "-Wall", "-Wextra", "-Werror", "-pthread", worker, source, "-o", executable)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile worker errno test: %v\n%s", err, output)
	}
	if output, err := exec.Command(executable).CombinedOutput(); err != nil {
		t.Fatalf("worker did not preserve raw worker-local errno: %v\n%s", err, output)
	}
}
