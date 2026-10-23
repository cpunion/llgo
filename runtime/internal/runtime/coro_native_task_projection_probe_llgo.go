//go:build llgo && llgo_coro && llgo_coro_task_projection_probe

package runtime

import "github.com/xgo-dev/llgo/runtime/internal/coro"

// Test-only observation. It changes no scheduler, G or physical-owner state.
func CoroNativeTaskProjectionProbeV1() bool {
	task, driver, route := coroCurrentTaskV1()
	if task == nil || driver == nil || route == 0 {
		return false
	}
	ctx := (*coroRuntimeContext)(coro.TaskLocal(task))
	return validCoroRuntimeTaskContext(task, ctx) && ctx.g.m == nil && ctx.g.atomicstatus == _Grunnable
}
