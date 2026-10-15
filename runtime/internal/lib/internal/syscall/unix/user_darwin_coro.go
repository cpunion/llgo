//go:build darwin && go1.26

package unix

import _ "unsafe"

// The reentrant account queries may wait for Directory Services. Keep their
// exact C identities on the ordinary worker path; their caller-owned result
// structs, buffers and output pointers remain rooted until physical completion.
// The original Go wrappers interpret the *_r return value as errno, rather than
// the worker's thread-local errno word. No non-reentrant static-result API is
// substituted, and no contract is inferred from a function-pointer address.

//go:linkname libc_getpwnam_r_trampoline C.getpwnam_r
func libc_getpwnam_r_trampoline()

//go:linkname libc_getpwuid_r_trampoline C.getpwuid_r
func libc_getpwuid_r_trampoline()

//go:linkname libc_getgrnam_r_trampoline C.getgrnam_r
func libc_getgrnam_r_trampoline()

//go:linkname libc_getgrgid_r_trampoline C.getgrgid_r
func libc_getgrgid_r_trampoline()

//go:linkname libc_getgrouplist_trampoline C.getgrouplist
func libc_getgrouplist_trampoline()

//go:linkname libc_sysconf_trampoline C.sysconf
func libc_sysconf_trampoline()
