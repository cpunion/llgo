package runtime

import (
	"unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
	cos "github.com/goplus/llgo/runtime/internal/clite/os"
)

func sysctlbynameInt32(name []byte) (int32, int32) {
	out := int32(0)
	nout := unsafe.Sizeof(out)
	ret := sysctlbyname(&name[0], (*byte)(unsafe.Pointer(&out)), &nout, nil, 0)
	return ret, out
}

func sysctlbynameBytes(name, out []byte) int32 {
	nout := uintptr(len(out))
	return sysctlbyname(&name[0], &out[0], &nout, nil, 0)
}

//go:linkname internal_cpu_getsysctlbyname internal/cpu.getsysctlbyname
func internal_cpu_getsysctlbyname(name []byte) (int32, int32) {
	return sysctlbynameInt32(name)
}

//go:linkname internal_cpu_sysctlbynameInt32 internal/cpu.sysctlbynameInt32
func internal_cpu_sysctlbynameInt32(name []byte) (int32, int32) {
	return sysctlbynameInt32(name)
}

//go:linkname internal_cpu_sysctlbynameBytes internal/cpu.sysctlbynameBytes
func internal_cpu_sysctlbynameBytes(name, out []byte) int32 {
	return sysctlbynameBytes(name, out)
}

// sysctlbyname is an exact synchronous Darwin metadata query used during
// runtime startup. Reuse the compiler-audited typed C declaration instead of
// transporting an unclassified function address through llgo.rawSyscall6.
func sysctlbyname(name *byte, old *byte, oldlen *uintptr, new *byte, newlen uintptr) int32 {
	return int32(cos.Sysctlbyname(
		(*c.Char)(unsafe.Pointer(name)),
		unsafe.Pointer(old),
		oldlen,
		unsafe.Pointer(new),
		newlen,
	))
}
