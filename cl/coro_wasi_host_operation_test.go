//go:build !llgo

package cl

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroWASIRawFilesystemHostOperationShapes(t *testing.T) {
	// Analyze the production adapters, not a second handwritten model of their
	// calls. Only the standard package's private scalar aliases are supplied.
	source, err := os.ReadFile("../runtime/_patch/syscall/fs_wasip1_coro.go")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(source), "package syscall", "package hostop", 1) + `
type Errno uint32
type size = uint32
type fdflags = uint32
type filesize = uint64
type lookupflags = uint32
type oflags = uint32
type rights = uint64
type timestamp = uint64
type dircookie = uint64
type filedelta = int64
type fstflags = uint32
type iovec struct { buf uint32; bufLen size }
`
	pkg, _, _ := buildGoSSAPkg(t, code)
	for index, spec := range []struct {
		name string
		args uint8
		mask uint16
	}{
		{"fd_read", 4, 0xa}, {"fd_write", 4, 0xa},
		{"fd_pread", 6, 0x22}, {"fd_pwrite", 6, 0x22},
		{"fd_close", 1, 0}, {"fd_seek", 5, 0x10},
		{"fd_readdir", 6, 0x22}, {"fd_filestat_get", 2, 2},
		{"fd_filestat_set_size", 3, 0}, {"fd_sync", 1, 0},
		{"fd_fdstat_set_rights", 5, 0}, {"path_open", 8, 0xa4},
		{"path_filestat_get", 5, 0x14}, {"path_filestat_set_times", 9, 4},
		{"path_create_directory", 3, 2}, {"path_unlink_file", 3, 2},
		{"path_remove_directory", 3, 2}, {"path_rename", 6, 0x12},
		{"path_link", 7, 0x24}, {"path_symlink", 5, 9},
		{"path_readlink", 6, 0x2a},
	} {
		t.Run(spec.name, func(t *testing.T) {
			fn := pkg.Func(spec.name)
			if fn == nil {
				t.Fatal("missing production adapter")
			}
			var call *ssa.Call
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					candidate, ok := instruction.(*ssa.Call)
					if ok && candidate.Common().StaticCallee() != nil &&
						candidate.Common().StaticCallee().Name() == "coroWASI_"+spec.name {
						call = candidate
					}
				}
			}
			if call == nil {
				t.Fatal("missing exact host operation")
			}
			shape, err := planCoroHostOperationCallShape(call)
			if err != nil || shape.opcode != uint32(3<<16|index+1) ||
				shape.argumentCount != spec.args || shape.pointerMask != spec.mask || shape.metadataWords != 0 {
				t.Fatalf("production shape = %+v, %v; want verb %d, args %d, pointers %#x", shape, err, index+1, spec.args, spec.mask)
			}
		})
	}
}
