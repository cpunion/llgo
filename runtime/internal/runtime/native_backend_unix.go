//go:build !windows

package runtime

import (
	"github.com/xgo-dev/llgo/runtime/internal/clite/pthread"
	psync "github.com/xgo-dev/llgo/runtime/internal/clite/pthread/sync"
)

type (
	nativeOnce          = psync.Once
	nativeMutex         = psync.Mutex
	nativeCond          = psync.Cond
	nativeThreadKey     = pthread.Key
	nativeKeyDestructor = pthread.KeyDestructor
)
