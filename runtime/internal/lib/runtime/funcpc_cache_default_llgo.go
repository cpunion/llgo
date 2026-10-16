//go:build !baremetal && (darwin || linux || wasm || windows)

package runtime

const funcForPCCacheSets = 1024
