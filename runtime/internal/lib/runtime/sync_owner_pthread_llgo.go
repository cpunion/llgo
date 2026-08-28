//go:build !llgo || !llgo_coro

package runtime

import psync "github.com/xgo-dev/llgo/runtime/internal/sync"

// Non-coroutine builds retain the original native mutex implementation.
type runtimeMutex = psync.Mutex
