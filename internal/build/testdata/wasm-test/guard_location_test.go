package wasmtest

import (
	"runtime"
	"strings"
	"testing"
)

type guardedLocationValue struct {
	padding [4096]byte
	value   int
	empty   [0]int
}

//go:noinline
func guardedLocationAccess(mode int, s []int, p *guardedLocationValue) {
	switch mode {
	case 0:
//line guard-index.go:101
		_ = s[3]
	case 1:
//line guard-read.go:101
		_ = p.value
	case 2:
//line guard-write.go:101
		p.value = 17
	case 3:
//line guard-empty.go:101
		_ = p.empty
	}
}

func TestGuardPanicLocations(t *testing.T) {
	for mode, file := range []string{"guard-index.go", "guard-read.go", "guard-write.go", "guard-empty.go"} {
		t.Run(file, func(t *testing.T) {
			defer func() {
				var pcs [32]uintptr
				n := runtime.Callers(0, pcs[:])
				frames := runtime.CallersFrames(pcs[:n])
				found := false
				for {
					frame, more := frames.Next()
					if strings.HasSuffix(frame.Function, ".guardedLocationAccess") {
						found = true
						if !strings.HasSuffix(frame.File, file) || frame.Line != 101 {
							t.Errorf("guard panic at %s:%d, want %s:101", frame.File, frame.Line, file)
						}
					}
					if !more {
						break
					}
				}
				if r := recover(); r == nil || !found {
					t.Errorf("guard panic=%v, source frame found=%v", r, found)
				}
			}()
			// A successful access at the same site must not affect the later
			// failure's file/line, including directives sharing a line number.
			guardedLocationAccess(mode, make([]int, 4), &guardedLocationValue{})
			guardedLocationAccess(mode, nil, nil)
		})
	}
}
