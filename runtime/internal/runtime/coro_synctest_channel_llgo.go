//go:build llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

// The optional tail follows the channel's existing public runtime prefix.
// Its ordinary allocation has no association pointer or global weak registry;
// the GC naturally retains a bubble precisely as long as its channels survive.
type coroSynctestChannel struct {
	channel Chan
	bubble  *coroSynctestBubble
}

func newSynctestChan() *Chan {
	b := currentSynctestBubble()
	if b == nil {
		return nil
	}
	ch := &coroSynctestChannel{bubble: b}
	ch.channel.synctest = true
	return &ch.channel
}

func coroSynctestChanDurable(ch *Chan, task *coro.G, foreignMessage string) bool {
	if ch == nil {
		return true
	}
	if !ch.synctest {
		return false
	}
	owner := (*coroSynctestChannel)(unsafe.Pointer(ch)).bubble
	var current *coroSynctestBubble
	if task == nil {
		current = currentSynctestBubble()
	} else if entry := coro.LookupExecutionDomainTask(task); entry != nil {
		current = (*coroSynctestBubble)(entry.Context)
	}
	if owner == nil || owner != current {
		coroRuntimeAbort(foreignMessage)
		return false
	}
	return true
}

func coroCheckSynctestChan(ch *Chan, context *coroChanExternalCommitContextV1, foreignMessage string) {
	var task *coro.G
	if context != nil {
		task = context.current
	}
	coroSynctestChanDurable(ch, task, foreignMessage)
}
