//go:build !linux || baremetal

package runtime

import _ "unsafe"

// Go exposes runtime.getAuxv on every platform, even when no auxiliary vector
// is populated. x/sys/cpu binds it unconditionally on Go 1.21 and later.
// Hosted Linux supplies its process vector in link_linux_llgo.go; targets
// without that startup contract, including bare metal, return a nil slice.
//
//go:linkname getAuxv runtime.getAuxv
func getAuxv() []uintptr { return nil }
