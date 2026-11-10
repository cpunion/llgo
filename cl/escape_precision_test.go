package cl

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestEscapePrecisionLowering(t *testing.T) {
	const src = `package precision
func read(p *int) int {return *p}
func apply(fn func(*int)int, p *int) int {return fn(p)}
func borrow(x int)int{return apply(read,&x)}
func immutable(x int)func()int{return func()int{return x}}
func mutable(x int)func()int{return func()int{x++;return x}}
func repeated()[]func()int{var x int;var fs []func()int;for i:=0;i<3;i++{x=i;fs=append(fs,func()int{return x})};return fs}
func equal(a,b string)bool{return a==b}
func compare(b []byte,a,c string)bool{s:=string(b);return equal(s,a)||s==c}
func snapshot(b []byte,a string)bool{s:=string(b);b[0]++;return s==a}
`
	for _, target := range []llssa.Target{
		{GOOS: "darwin", GOARCH: "arm64"}, {GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "windows", GOARCH: "386"}, {GOOS: "wasip1", GOARCH: "wasm"},
	} {
		t.Run(target.GOOS+"/"+target.GOARCH, func(t *testing.T) {
			t.Setenv("GOOS", target.GOOS)
			t.Setenv("GOARCH", target.GOARCH)
			word := 8
			if target.GOARCH == "386" || target.GOARCH == "wasm" {
				word = 4
			}
			ir := compileInterfaceBoxIR(t, src, ssa.InstantiateGenerics, &target, target.GOARCH == "wasm")
			if body := llvmFunction(t, ir, "precision.borrow"); strings.Contains(body, "runtime.Alloc") {
				t.Errorf("callback argument still allocates:\n%s", body)
			}
			body := llvmFunction(t, ir, "precision.immutable")
			checkClosureCaptureAllocations(t, body, word, 0, 1, 0)
			// The captured scalar is inline after the descriptor header, not
			// a separately allocated cell. The header contains an environment
			// pointer even when the captured value itself is pointer-free.
			if !strings.Contains(body, fmt.Sprintf("{ { ptr, ptr }, { i%d } }", 8*word)) ||
				!strings.Contains(body, `store ptr @"precision.immutable$1"`) {
				t.Errorf("immutable capture lost its combined descriptor layout:\n%s", body)
			}
			for _, name := range []string{"mutable", "repeated"} {
				body := llvmFunction(t, ir, "precision."+name)
				checkClosureCaptureAllocations(t, body, word, 1, 1, 0)
				cell := regexp.MustCompile(`(%[0-9]+) = call ptr @"[^"]*runtime.AllocAtomicU"\(`).FindStringSubmatch(body)
				if len(cell) != 2 {
					t.Errorf("%s capture lost its shared scalar cell:\n%s", name, body)
					continue
				}
				// Every closure stores the same scalar cell address, rather
				// than copying the mutable value into its private environment.
				shared := regexp.MustCompile(`(?m)(%[0-9]+) = getelementptr inbounds(?: nuw)? \{ ptr \}, ptr %[0-9]+, i32 0, i32 0\n\s*store ptr ` + regexp.QuoteMeta(cell[1]) + `, ptr (%[0-9]+)`)
				capture := shared.FindStringSubmatch(body)
				if len(capture) != 3 || capture[1] != capture[2] {
					t.Errorf("%s capture lost shared storage:\n%s", name, body)
				}
			}
			if body := llvmFunction(t, ir, "precision.compare"); !strings.Contains(body, "StringFromBytesReadOnly") {
				t.Errorf("read region still copies:\n%s", body)
			}
			if body := llvmFunction(t, ir, "precision.snapshot"); strings.Contains(body, "StringFromBytesReadOnly") {
				t.Errorf("mutable source lost snapshot:\n%s", body)
			}
		})
	}
}
