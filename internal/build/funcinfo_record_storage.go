package build

import (
	buildfuncinfo "github.com/xgo-dev/llgo/internal/build/funcinfo"
	"github.com/xgo-dev/llvm"
)

func funcInfoRecordStorage(ctx llvm.Context, records []buildfuncinfo.EncodedRecord, allowNarrow bool) (llvm.Type, []llvm.Value, uint8) {
	narrow := allowNarrow && len(records) != 0
	for _, rec := range records {
		if rec.SymbolPkg > 65535 || rec.SymbolName > 65535 || rec.NamePkg > 65535 ||
			rec.NameName > 65535 || rec.FileRoot > 65535 || rec.FileName > 65535 {
			narrow = false
			break
		}
	}
	idType := ctx.Int32Type()
	size := uint8(buildfuncinfo.EncodedRecordSize)
	if narrow {
		idType = ctx.Int16Type()
		size = 16
	}
	recordType := ctx.StructType([]llvm.Type{
		idType, idType, idType, idType, idType, idType, ctx.Int32Type(),
	}, false)
	values := make([]llvm.Value, 0, len(records))
	for _, rec := range records {
		values = append(values, llvm.ConstNamedStruct(recordType, []llvm.Value{
			llvm.ConstInt(idType, uint64(rec.SymbolPkg), false),
			llvm.ConstInt(idType, uint64(rec.SymbolName), false),
			llvm.ConstInt(idType, uint64(rec.NamePkg), false),
			llvm.ConstInt(idType, uint64(rec.NameName), false),
			llvm.ConstInt(idType, uint64(rec.FileRoot), false),
			llvm.ConstInt(idType, uint64(rec.FileName), false),
			llvm.ConstInt(ctx.Int32Type(), uint64(rec.Line), false),
		}))
	}
	return recordType, values, size
}
