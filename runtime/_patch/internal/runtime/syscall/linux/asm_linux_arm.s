//go:build tinygo.wasm || baremetal

// Syscall6 is supplied by syscall_freestanding_webassembly_llgo.go. A
// freestanding target must never emit the Linux kernel trap selected by its
// layout-only frontend architecture.
