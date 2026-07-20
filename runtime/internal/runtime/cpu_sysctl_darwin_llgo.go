//go:build llgo && darwin && !baremetal

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
	"unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
	cos "github.com/goplus/llgo/runtime/internal/clite/os"
)

// The upstream Go runtime supplies these two internal/cpu definitions from
// runtime/os_darwin.go. LLGo replaces that runtime package, so preserve the
// same exact physical Go ABI here. The bodyless declarations in internal/cpu
// are joined to these definitions by the emission universe's structural
// go:linkname alias, rather than by a package or display-name exception.

//go:linkname internalCPUSysctlbynameInt32 internal/cpu.sysctlbynameInt32
func internalCPUSysctlbynameInt32(name []byte) (int32, int32) {
	out := int32(0)
	nout := unsafe.Sizeof(out)
	ret := cos.Sysctlbyname(
		(*c.Char)(unsafe.Pointer(&name[0])), unsafe.Pointer(&out), &nout, nil, 0,
	)
	return int32(ret), out
}

//go:linkname internalCPUSysctlbynameBytes internal/cpu.sysctlbynameBytes
func internalCPUSysctlbynameBytes(name, out []byte) int32 {
	nout := uintptr(len(out))
	ret := cos.Sysctlbyname(
		(*c.Char)(unsafe.Pointer(&name[0])), unsafe.Pointer(&out[0]), &nout, nil, 0,
	)
	return int32(ret)
}
