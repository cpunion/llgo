//go:build !windows

package tls

import "github.com/xgo-dev/llgo/runtime/internal/clite/pthread"

type (
	nativeKey           = pthread.Key
	nativeKeyDestructor = pthread.KeyDestructor
)
