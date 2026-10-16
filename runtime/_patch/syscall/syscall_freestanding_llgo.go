//go:build wasm || tinygo.wasm || baremetal

package syscall

// Freestanding targets have no Linux/Unix kernel trap ABI. Keep the public
// signatures but reject raw traps; supported I/O uses explicit host operations.
func rawSyscallNoError(trap, a1, a2, a3 uintptr) (r1, r2 uintptr) {
	return ^uintptr(0), 0
}

func Syscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	return ^uintptr(0), 0, ENOSYS
}

func Syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	return ^uintptr(0), 0, ENOSYS
}

func RawSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	return ^uintptr(0), 0, ENOSYS
}

func RawSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	return ^uintptr(0), 0, ENOSYS
}
