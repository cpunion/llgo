//go:build windows

package libuv

const (
	LLGoPackage = "link: $(pkg-config --libs libuv); -luv"
	LLGoFiles   = "_wrap/libuv.c"
)
