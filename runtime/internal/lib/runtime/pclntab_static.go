//go:build !llgo_pclntab_external && !llgo_pclntab_none

package runtime

// Embedded builds have no sidecar path, probe string, filesystem call or
// loader state machine linked into the program. The main module initializes
// the tables directly.
func ensureRuntimePCLN() bool { return true }

func runtimePCLNReady() bool { return true }
