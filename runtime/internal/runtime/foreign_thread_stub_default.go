//go:build !windows && (!llgo || nogc || baremetal || wasm || tinygo.wasm || !(darwin || linux))

package runtime

import "github.com/xgo-dev/llgo/runtime/internal/thread"

type gLifecycleDestructor = thread.KeyDestructor

func EnterForeignThread() bool {
	return false
}

func ExitForeignThread(registered bool) {}
