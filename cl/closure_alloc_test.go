package cl

import (
	"fmt"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestImmediateClosureStorage(t *testing.T) {
	const src = `package capturealloc
import "unsafe"
func pointer(p unsafe.Pointer) unsafe.Pointer { return func() unsafe.Pointer { return p }() }
func mutate(x int) int { func(){x++}(); return x }
func loop(x int, n int) int { for i:=0;i<n;i++ { func(){x+=n}() }; return x }
func zero(x struct{}) { func(){ _ = x }() }
func escape(x int) func() int { return func() int { return x } }
func address(x int) *int { return func() *int { return &x }() }
func deferred(x int) (r int) { defer func(){r=x}(); return }
func consume(int)
func async(x int) { go func(){consume(x)}() }
func unknown(func() int)
func argument(x int) { unknown(func() int { return x }) }
`
	for _, target := range []llssa.Target{
		{GOOS: "linux", GOARCH: "amd64"}, {GOOS: "darwin", GOARCH: "arm64"},
		{GOOS: "windows", GOARCH: "386"},
	} {
		t.Run(target.GOOS+"/"+target.GOARCH, func(t *testing.T) {
			word := 8
			if target.GOARCH == "386" {
				word = 4
			}
			for _, mode := range []ssa.BuilderMode{0, ssa.GlobalDebug} {
				ir := compileWithRewritesModeTarget(t, src, nil, mode, &target)
				for _, name := range []string{"pointer", "mutate", "loop", "zero"} {
					if body := llvmFunction(t, ir, "capturealloc."+name); strings.Contains(body, "runtime.Alloc") {
						t.Errorf("%s retains heap capture storage:\n%s", name, body)
					}
				}
				// A copied scalar shares one scanned allocation with the
				// descriptor's code/environment header. Address-observed cells
				// remain atomic; defer keeps two recover-visible scalar cells.
				for _, capture := range []struct {
					name                   string
					atomic, scanned, roots int
				}{
					{"escape", 0, 1, 0},
					{"address", 1, 0, 0},
					{"deferred", 2, 0, 0},
					{"async", 0, 1, 1},
					{"argument", 0, 1, 0},
				} {
					body := llvmFunction(t, ir, "capturealloc."+capture.name)
					checkClosureCaptureAllocations(t, body, word, capture.atomic, capture.scanned, capture.roots)
					if capture.scanned != 0 {
						layout := fmt.Sprintf("{ { ptr, ptr }, { i%d } }", 8*word)
						code := "store ptr @\"capturealloc." + capture.name + "$1\""
						if !strings.Contains(body, layout) || !strings.Contains(body, code) {
							t.Errorf("%s lost its combined descriptor and scalar capture:\n%s", capture.name, body)
						}
					}
				}
			}
		})
	}
}

func checkClosureCaptureAllocations(t *testing.T, body string, word, atomic, scanned, roots int) {
	t.Helper()
	for _, allocation := range []struct {
		name         string
		count, words int
	}{
		{"AllocAtomicU", atomic, 1},
		{"AllocU", scanned, 3},
		{"AllocRoot", roots, 1},
	} {
		call := "runtime." + allocation.name + "\"("
		exact := fmt.Sprintf("%si%d %d)", call, 8*word, allocation.words*word)
		if strings.Count(body, call) != allocation.count || strings.Count(body, exact) != allocation.count {
			t.Errorf("want %d %s allocations of %d bytes:\n%s", allocation.count, allocation.name, allocation.words*word, body)
		}
	}
	if strings.Count(body, "runtime.Alloc") != atomic+scanned+roots {
		t.Errorf("unexpected capture allocator:\n%s", body)
	}
}

func TestCgoWrapperElidesUnusedFrame(t *testing.T) {
	const src = `package cgoframe
import "unsafe"
var _cgo_scalar, _cgo_pair unsafe.Pointer
func _cgo_runtime_cgocall(unsafe.Pointer,uintptr) int32
type pair struct { a,b uint64 }
func _Cfunc_scalar(p0 uint64)(r1 uint64) {
	_cgo_runtime_cgocall(_cgo_scalar,uintptr(unsafe.Pointer(&p0)))
	return
}

func _Cfunc_pair(p0 uint64)(r1 pair) {
	_cgo_runtime_cgocall(_cgo_pair,uintptr(unsafe.Pointer(&p0)))
	return
}
func _Cfunc_noargs()(r1 uint64) {
	_cgo_runtime_cgocall(_cgo_scalar,uintptr(unsafe.Pointer(&r1)))
	return
}
`
	ir := compileWithRewrites(t, src, nil)
	for _, name := range []string{"scalar", "pair", "noargs"} {
		body := llvmFunction(t, ir, "cgoframe._Cfunc_"+name)
		if strings.Contains(body, "runtime.Alloc") {
			t.Fatalf("%s retains unused argument/result frame:\n%s", name, body)
		}
	}
}
