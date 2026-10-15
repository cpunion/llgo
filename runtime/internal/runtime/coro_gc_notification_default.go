//go:build !llgo_coro_native_timer || !(darwin || linux) || baremetal || coro_runtime_adapter_test

package runtime

func coroGCNotificationFDV1() int32 { return -1 }
