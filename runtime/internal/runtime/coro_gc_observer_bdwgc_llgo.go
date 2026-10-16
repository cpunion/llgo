//go:build llgo && llgo_coro && !nogc && !baremetal && !llgo_wasm_gc && !coro_runtime_adapter_test

package runtime

import (
	"github.com/xgo-dev/llgo/runtime/internal/clite/bdwgc"
	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

func coroConfigureGCObserverV1(epoch *uint32) {
	bdwgc.EnableGCNotification(epoch, coroGCNotificationFDV1())
}

func coroDetachGCNotificationV1(fd int32) {
	if coro.GCObservationEnabled() {
		bdwgc.DetachGCNotification(fd)
	}
}
