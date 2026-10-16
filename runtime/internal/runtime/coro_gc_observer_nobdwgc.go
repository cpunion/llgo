//go:build nogc || baremetal || llgo_wasm_gc || coro_runtime_adapter_test || coro_native_fleet_test

package runtime

func coroConfigureGCObserverV1(*uint32) {}
func coroDetachGCNotificationV1(int32)  {}
