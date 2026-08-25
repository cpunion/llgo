//go:build darwin

package syscall

func Syscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	return RawSyscall(trap, a1, a2, a3)
}

func Syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	return RawSyscall6(trap, a1, a2, a3, a4, a5, a6)
}

func Syscall9(trap, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2 uintptr, err Errno) {
	r1, errno := runtime_syscall9(trap, a1, a2, a3, a4, a5, a6, a7, a8, a9)
	return r1, 0, Errno(errno)
}

// RawSyscall accepts an arbitrary runtime trap word. Until the compiler owns
// an exact constant-trap capability proof, it must remain on the calling
// native thread: the target may be fork, exec, exit, or thread-affine.
//
//llgo:rawcritical
func RawSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	r1, errno := runtime_syscall3(trap, a1, a2, a3)
	return r1, 0, Errno(errno)
}

//llgo:rawcritical
func RawSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	r1, errno := runtime_syscall6(trap, a1, a2, a3, a4, a5, a6)
	return r1, 0, Errno(errno)
}
