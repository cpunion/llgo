//go:build linux && go1.26

package syscall

// The bodies below cross a clone/vfork boundary. Their child-side execution
// must remain on the current native stack and must use the synchronous
// RawSyscall body: an arbitrary worker would belong to the parent process and
// cannot preserve the shared-address-space child protocol.
//
// Keep the standard-library implementations intact and attach capabilities to
// the exact GOROOT declarations through the source-patch annotation pass.
//
//llgo:annotate forkAndExecInChild1 rawcritical
//llgo:annotate doCheckClonePidfd rawcritical
