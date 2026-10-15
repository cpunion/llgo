//go:build !llgo

package cl

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroJSFilesystemOperationKeepsTypedOwners(t *testing.T) {
	source, err := os.ReadFile("../runtime/internal/lib/syscall/js/fs_coro_llgo.go")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(source), "package js", "package hostop", 1) + `
type Value struct { ref uintptr }
func ValueOf(any) Value { return Value{} }
func (v Value) IsNull() bool { return v.ref == 4 }
`
	pkg, _, _ := buildGoSSAPkg(t, code)
	for _, test := range []struct {
		name, target string
		opcode       uint32
		arguments    int
		pointers     uint32
	}{
		{"llgoFSCall", "coroJSFSOperation", 4<<16 | 1, 4, 0x5},
		{"llgoFSOpenInto", "coroJSFSOpenOperation", 4<<16 | 2, 5, 0x15},
	} {
		found := false
		for _, block := range pkg.Func(test.name).Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*ssa.Call)
				if !ok || call.Common().StaticCallee() == nil || call.Common().StaticCallee().Name() != test.target {
					continue
				}
				shape, err := planCoroHostOperationCallShape(call)
				if err != nil || uint32(shape.opcode) != test.opcode || int(shape.argumentCount) != test.arguments || uint32(shape.pointerMask) != test.pointers || shape.metadataWords != 0 {
					t.Fatalf("JS filesystem operation lost typed string/slice ownership: %+v, %v", shape, err)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("production JS helper %s has no host operation", test.name)
		}
	}
}
