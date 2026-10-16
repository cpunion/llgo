//go:build baremetal && (darwin || linux || wasm || windows)

package runtime

// Cache misses still resolve through the same metadata and symbol tables.
// Keep the allocation-free four-way cache, but do not reserve a desktop-sized
// lookup cache on a small board. This does not limit functions or goroutines.
const funcForPCCacheSets = 32
