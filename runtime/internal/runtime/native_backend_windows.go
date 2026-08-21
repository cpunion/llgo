//go:build windows

package runtime

import (
	psync "github.com/xgo-dev/llgo/runtime/internal/clite/sync"
	"github.com/xgo-dev/llgo/runtime/internal/clite/thread"
)

type (
	nativeOnce          = psync.Once
	nativeMutex         = psync.Mutex
	nativeCond          = psync.Cond
	nativeThreadKey     = thread.Key
	nativeKeyDestructor = thread.KeyDestructor
)
