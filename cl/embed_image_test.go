//go:build !llgo

package cl

import (
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestCoroEmbedFSStaticImage(t *testing.T) {
	const source = `package foo
import "embed"
var A, B, Empty embed.FS
var Bytes, OtherBytes []byte
`
	for _, target := range []struct {
		name string
		arch *llssa.Target
		bits int
	}{
		{name: "native", bits: 64},
		{name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, bits: 32},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog := newLLSSAProgForTarget(t, target.arch)
			defer prog.Dispose()
			ssaPkg, _, files := buildGoSSAPkg(t, source)
			data := []goembed.FileData{{Name: "assets/hello.txt", Data: []byte("static payload")}}
			embedMap := goembed.VarMap{
				"A": {Files: data}, "B": {Files: data},
				"Bytes": {Files: data}, "OtherBytes": {Files: data},
			}
			pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, embedMap, PackageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify static embed image: %v\n%s", err, module.String())
			}
			var headers, tables []llvm.Value
			for _, name := range []string{"foo.A", "foo.B"} {
				global := module.NamedGlobal(name)
				header := global.Initializer().Operand(0)
				if header.IsAGlobalVariable().IsNil() || !header.IsGlobalConstant() || header.Linkage() != llvm.PrivateLinkage {
					t.Fatalf("%s has no private constant slice header: %s", name, header.String())
				}
				slice := header.Initializer()
				table := slice.Operand(0)
				if table != module.NamedGlobal(name+"$embed.files") || !table.IsGlobalConstant() || table.Linkage() != llvm.PrivateLinkage {
					t.Fatalf("%s has no private constant file table: %s", name, table.String())
				}
				count := len(goembed.BuildFSEntries(data))
				for _, index := range []int{1, 2} {
					value := slice.Operand(index)
					if value.Type().IntTypeWidth() != target.bits || value.ZExtValue() != uint64(count) {
						t.Fatalf("%s slice word %d = %s; want %d-bit %d", name, index, value.String(), target.bits, count)
					}
				}
				array := table.Initializer()
				if array.OperandsCount() != count {
					t.Fatalf("%s entry count = %d; want %d", name, array.OperandsCount(), count)
				}
				for i := 0; i < count; i++ {
					entry := array.Operand(i)
					for field := 2; field < entry.OperandsCount(); field++ {
						if !entry.Operand(field).IsNull() {
							t.Fatalf("%s entry %d extra field %d is not zero", name, i, field)
						}
					}
				}
				headers, tables = append(headers, header), append(tables, table)
			}
			if headers[0] == headers[1] || tables[0] == tables[1] {
				t.Fatal("distinct embed.FS variables share address-bearing metadata")
			}
			if !module.NamedGlobal("foo.Empty").Initializer().IsNull() {
				t.Fatal("zero embed.FS is not represented by its nil static image")
			}
			bytes := module.NamedGlobal("foo.Bytes").Initializer().Operand(0)
			otherBytes := module.NamedGlobal("foo.OtherBytes").Initializer().Operand(0)
			if bytes == otherBytes || bytes.IsGlobalConstant() || otherBytes.IsGlobalConstant() {
				t.Fatal("embedded []byte lost distinct writable storage")
			}
			ir := module.String()
			for _, value := range []string{"assets/hello.txt", "static payload"} {
				if !strings.Contains(ir, value) {
					t.Fatalf("static image is missing %q", value)
				}
			}
			for _, forbidden := range []string{"MakeSlice", "AllocZ"} {
				if strings.Contains(ir, forbidden) {
					t.Fatalf("static embed image introduced runtime allocation %s", forbidden)
				}
			}
		})
	}
}
