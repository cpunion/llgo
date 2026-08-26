//go:build llgo_pclntab_none

package runtime

// Metadata-free builds fail at the public symbolization boundary. Keeping
// this a compile-time capability avoids retaining a mode word, a branch, the
// shadow-stack interning path, or any sidecar loader code in the binary.
func ensureRuntimePCLN() bool { return false }

func runtimePCLNReady() bool { return false }
