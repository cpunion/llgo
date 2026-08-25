//go:build !windows && !amd64 && !wasm && !tinygo.wasm

package ffi

const (
	DefaultAbi = 1
)
