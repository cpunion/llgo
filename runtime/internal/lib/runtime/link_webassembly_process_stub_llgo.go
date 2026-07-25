//go:build !baremetal && (wasm || tinygo.wasm) && go1.22

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import _ "unsafe"

// A core WebAssembly module has no fork/clone process capability. The Linux
// frontend still compiles syscall's capability probe, so provide its exact
// linkname body and report ENOSYS without entering a C syscall or worker.
//
//go:linkname syscall_rawVforkSyscall syscall.rawVforkSyscall
func syscall_rawVforkSyscall(trap, a1, a2, a3 uintptr) (r1 uintptr, err uintptr) {
	return ^uintptr(0), 38 // Linux ENOSYS, matching the selected frontend ABI.
}
