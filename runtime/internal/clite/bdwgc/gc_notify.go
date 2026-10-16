package bdwgc

import _ "unsafe"

// InitGCNotification chains the process collector hook during single-threaded
// allocator bootstrap. Nothing is observed until EnableGCNotification.
//
//go:linkname InitGCNotification C.__llgo_coro_gc_notification_init_v1
func InitGCNotification()

// These configuration calls synchronize with the collector but never retain
// a caller frame, invoke Go, or wait for application I/O. epoch is static
// process-lifetime storage; fd is the existing nonblocking executor doorbell.
//
//llgo:coro sync
//go:linkname EnableGCNotification C.__llgo_coro_gc_notification_enable_v1
func EnableGCNotification(epoch *uint32, fd int32)

//llgo:coro sync
//go:linkname DetachGCNotification C.__llgo_coro_gc_notification_detach_v1
func DetachGCNotification(fd int32)
