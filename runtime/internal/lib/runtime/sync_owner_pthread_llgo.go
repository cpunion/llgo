//go:build !llgo || !llgo_coro

package runtime

import psync "github.com/goplus/llgo/runtime/internal/clite/pthread/sync"

// Non-coroutine builds retain the original native mutex implementation.
type runtimeMutex = psync.Mutex
