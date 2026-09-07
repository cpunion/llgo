//go:build wasm && nogc && !llgo.wasm.gc.linear

package runtime

import _ "unsafe"

// With GC disabled, weak handles never expire, so unique has no dead entries
// to clean up. Keep its registration boundary available without starting a
// goroutine that cannot receive a collection notification.
//
//go:linkname unique_runtime_registerUniqueMapCleanup unique.runtime_registerUniqueMapCleanup
func unique_runtime_registerUniqueMapCleanup(f func()) {}
