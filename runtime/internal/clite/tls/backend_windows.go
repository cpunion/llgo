//go:build windows

package tls

import "github.com/xgo-dev/llgo/runtime/internal/clite/thread"

type (
	nativeKey           = thread.Key
	nativeKeyDestructor = thread.KeyDestructor
)
