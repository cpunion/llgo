//go:build coro_nil_fault_adapter_test

package runtime

import "github.com/xgo-dev/llgo/runtime/internal/coro"

func coroStageForeignIngressTerminal(
	*coro.G,
	coro.CompletionStatus,
) (bool, bool) {
	return false, true
}
