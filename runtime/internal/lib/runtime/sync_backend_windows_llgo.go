//go:build windows

package runtime

import psync "github.com/xgo-dev/llgo/runtime/internal/clite/sync"

type (
	nativeOnce  = psync.Once
	nativeMutex = psync.Mutex
	nativeCond  = psync.Cond
)
