//go:build darwin

package runtime

import (
	c "github.com/goplus/llgo/runtime/internal/clite"
	cliteos "github.com/goplus/llgo/runtime/internal/clite/os"
	"unsafe"
)

// These fixed scalar declarations keep libc environment mutations on the
// common llgo.syscall worker boundary when coroutine worker lowering is
// enabled. The same calls retain their direct synchronous lowering in a plain
// build. C string storage lives in the suspended coroutine frame until the
// irreversible worker completion is consumed, including command shutdown.

//go:linkname runtimeDarwinFuncPCABI0 llgo.funcPCABI0
func runtimeDarwinFuncPCABI0(fn any) uintptr

//go:linkname runtimeDarwinSyscall1Int32 llgo.syscall32
func runtimeDarwinSyscall1Int32(fn, a1 uintptr) (r1, r2, errno uintptr)

//go:linkname runtimeDarwinSyscall3Int32 llgo.syscall32
func runtimeDarwinSyscall3Int32(fn, a1, a2, a3 uintptr) (r1, r2, errno uintptr)

// These address-only declarations are the exact producer-side identities for
// the worker calls below. Their word-call ABI is intentionally independent of
// the typed clite/os declarations: llgo.funcPCABI0 publishes the frozen target
// identity, and the exact llgo.syscall32 sinks derive their 3-word/1-word ABI
// without recovering policy from the emitted address.
// Darwin setenv(const char*, const char*, int) therefore has three worker
// words, while unsetenv(const char*) has one; both return C int, which is why
// these sites use the syscall32 result convention.
// setenv and unsetenv copy/use their C strings before returning, so the frame
// borrows only need to survive through worker completion.

//go:linkname libc_setenv_trampoline C.setenv
func libc_setenv_trampoline()

//go:linkname libc_unsetenv_trampoline C.unsetenv
func libc_unsetenv_trampoline()

// runtimeDarwinFcntl is used only for the runtime's F_GETFL/F_SETFL and
// F_GETFD/F_SETFD descriptor metadata operations. Those commands do not wait
// for descriptor readiness or application I/O, so they must stay on the owner
// thread with the immediately following TLS errno read. General syscall.fcntl
// calls retain their independent worker capability.
//
//llgo:coro sync
//go:linkname runtimeDarwinFcntl C.llgo_fcntl
func runtimeDarwinFcntl(fd, cmd, arg uintptr) uintptr

// os.Executable (darwin) expects runtime to populate os.executablePath.
// Upstream Go runtime sets this during startup; llgo sets it from argv[0],
// which is sufficient for stdlib os tests.
//
//go:linkname executablePath os.executablePath
var executablePath string

//go:linkname os_runtime_args os.runtime_args
func os_runtime_args() []string {
	argc := int(c.Argc)
	if argc <= 0 {
		return nil
	}
	if c.Argv == nil {
		return nil
	}
	args := make([]string, 0, argc)
	for i := 0; i < argc; i++ {
		p := c.Index(c.Argv, i)
		if p == nil {
			break
		}
		args = append(args, c.GoString(p))
	}
	if len(args) > 0 && executablePath == "" {
		executablePath = args[0]
	}
	return args
}

//go:linkname c_environ environ
var c_environ **c.Char

//go:linkname syscall_runtime_envs syscall.runtime_envs
func syscall_runtime_envs() []string {
	var out []string
	for p := c_environ; p != nil && *p != nil; p = c.Advance(p, 1) {
		out = append(out, c.GoString(*p))
	}
	return out
}

//llgo:managedlink
//go:linkname syscall_runtimeSetenv syscall.runtimeSetenv
func syscall_runtimeSetenv(key, value string) {
	name := c.AllocaCStr(key)
	text := c.AllocaCStr(value)
	runtimeDarwinSyscall3Int32(
		runtimeDarwinFuncPCABI0(libc_setenv_trampoline),
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(text)),
		1,
	)
	if key == "GODEBUG" {
		godebugEnvChanged(value)
	}
}

//llgo:managedlink
//go:linkname syscall_runtimeUnsetenv syscall.runtimeUnsetenv
func syscall_runtimeUnsetenv(key string) {
	name := c.AllocaCStr(key)
	runtimeDarwinSyscall1Int32(
		runtimeDarwinFuncPCABI0(libc_unsetenv_trampoline),
		uintptr(unsafe.Pointer(name)),
	)
	if key == "GODEBUG" {
		godebugEnvChanged("")
	}
}

//go:linkname os_beforeExit os.runtime_beforeExit
func os_beforeExit(exitCode int) {}

//go:linkname os_sigpipe os.sigpipe
func os_sigpipe() {}

//go:linkname c_getpagesize C.getpagesize
func c_getpagesize() c.Int

//go:linkname syscall_Getpagesize syscall.Getpagesize
func syscall_Getpagesize() int {
	return int(c_getpagesize())
}

//go:linkname syscall_Exit syscall.Exit
//go:nosplit
func syscall_Exit(code int) {
	c.Exit(c.Int(code))
}

//go:linkname syscall_runtime_BeforeFork syscall.runtime_BeforeFork
func syscall_runtime_BeforeFork() {}

//go:linkname syscall_runtime_AfterFork syscall.runtime_AfterFork
func syscall_runtime_AfterFork() {}

//go:linkname syscall_runtime_AfterForkInChild syscall.runtime_AfterForkInChild
func syscall_runtime_AfterForkInChild() {}

//go:linkname syscall_runtime_BeforeExec syscall.runtime_BeforeExec
func syscall_runtime_BeforeExec() {}

//go:linkname syscall_runtime_AfterExec syscall.runtime_AfterExec
func syscall_runtime_AfterExec() {}

func fcntl(fd int32, cmd int32, arg int32) (int32, int32) {
	result := runtimeDarwinFcntl(
		uintptr(int64(fd)),
		uintptr(int64(cmd)),
		uintptr(int64(arg)),
	)
	if uint32(result) == ^uint32(0) {
		return -1, int32(cliteos.Errno())
	}
	return int32(result), 0
}
