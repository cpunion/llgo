package cl

import (
	"fmt"
	"go/types"

	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func isStringType(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.String
}

func isByteSliceType(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	b, ok := types.Unalias(s.Elem()).Underlying().(*types.Basic)
	return ok && (b.Kind() == types.Byte || b.Kind() == types.Uint8)
}

func isEmbedFSType(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := n.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "embed" && obj.Name() == "FS"
}

func (p *context) tryEmbedGlobalInit(pkg llssa.Package, gbl *ssa.Global, g llssa.Global, globalName string) bool {
	if len(p.embedMap) == 0 {
		return false
	}
	info, ok := p.embedMap[gbl.Name()]
	if !ok || len(info.Files) == 0 {
		return false
	}
	ptr := types.Unalias(gbl.Type()).(*types.Pointer)
	elem := types.Unalias(ptr.Elem())
	switch {
	case isStringType(elem):
		if len(info.Files) != 1 {
			panic("//go:embed string variable must resolve to one file")
		}
		g.Init(pkg.ConstString(string(info.Files[0].Data)))
		return true
	case isByteSliceType(elem):
		if len(info.Files) != 1 {
			panic("//go:embed []byte variable must resolve to one file")
		}
		g.Init(pkg.ConstBytes(info.Files[0].Data))
		return true
	case isEmbedFSType(elem):
		g.Init(p.embedFSConstant(pkg, elem, globalName, goembed.BuildFSEntries(info.Files)))
		return true
	default:
		panic(fmt.Sprintf("go:embed cannot apply to var of type %s", elem.String()))
	}
}

const (
	embedFSFieldFiles   = 0
	embedFileFieldName  = 0
	embedFileFieldBytes = 1
)

// embedFSConstant uses the existing exact Go layout, but its file records and
// slice header are static data. No hidden package-init allocation, coroutine
// effect or target-specific startup path is needed.
func (p *context) embedFSConstant(pkg llssa.Package, fsTyp types.Type, name string, entries []goembed.FileData) llssa.Expr {
	fsStruct := fsTyp.Underlying().(*types.Struct)
	filesPtr := types.Unalias(fsStruct.Field(embedFSFieldFiles).Type()).(*types.Pointer)
	filesSlice := types.Unalias(filesPtr.Elem()).(*types.Slice)
	fileTyp := filesSlice.Elem()
	llFileTyp := p.type_(fileTyp, llssa.InGo)
	llFilesSliceTyp := p.prog.Slice(llFileTyp)
	fileStruct := fileTyp.Underlying().(*types.Struct)
	values := make([]llssa.Expr, len(entries))
	for i, entry := range entries {
		fields := make([]llssa.Expr, fileStruct.NumFields())
		for index := range fields {
			fields[index] = p.prog.Zero(p.type_(fileStruct.Field(index).Type(), llssa.InGo))
		}
		fields[embedFileFieldName] = pkg.ConstString(entry.Name)
		fields[embedFileFieldBytes] = pkg.ConstString(string(entry.Data))
		values[i] = p.prog.ConstStruct(llFileTyp, fields)
	}
	files := pkg.ConstReadOnlySlice(name+"$embed.files", llFilesSliceTyp, values)
	fields := make([]llssa.Expr, fsStruct.NumFields())
	for index := range fields {
		fields[index] = p.prog.Zero(p.type_(fsStruct.Field(index).Type(), llssa.InGo))
	}
	fields[embedFSFieldFiles] = pkg.ConstAddress(files)
	return p.prog.ConstStruct(p.type_(fsTyp, llssa.InGo), fields)
}
