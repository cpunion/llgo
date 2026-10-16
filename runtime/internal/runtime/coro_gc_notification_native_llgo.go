//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

package runtime

func coroGCNotificationFDV1() int32 {
	fd, ok := coroNativeFleetV1State.domains[0].doorbell.NotificationFD()
	if !ok {
		return -1
	}
	return fd
}
