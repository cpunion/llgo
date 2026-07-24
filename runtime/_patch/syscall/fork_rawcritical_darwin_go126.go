//go:build darwin && go1.26

package syscall

// forkAndExecInChild must remain on the current native stack from fork through
// the parent/child recovery point. The source-patch builder attaches the
// compiler-owned marker to the exact GOROOT declaration without copying the Go
// standard library implementation.
//
//llgo:annotate forkAndExecInChild rawcritical
