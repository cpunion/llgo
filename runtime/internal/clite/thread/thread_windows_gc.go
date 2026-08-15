//go:build windows && !nogc

package thread

const (
	LLGoFiles   = "-DLLGO_USE_BDWGC: _wrap/thread_windows.c"
	LLGoPackage = "link: $(pkg-config --libs bdw-gc); -lgc"
)
