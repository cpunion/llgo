//go:build !llgo

package ssa

import (
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestWideShiftCountCheckedBeforeNarrowing(t *testing.T) {
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	pkg := prog.NewPackage("wideshift", "wideshift")

	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "x", types.Typ[types.Int]),
		types.NewParam(token.NoPos, nil, "count", types.Typ[types.Uint64]),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "", types.Typ[types.Int]))
	sig := types.NewSignatureType(nil, nil, nil, params, results, false)
	for _, op := range []token.Token{token.SHL, token.SHR} {
		fn := pkg.NewFunc(op.String(), sig, InGo)
		b := fn.MakeBody(1)
		b.Return(b.BinOp(op, fn.Param(0), fn.Param(1)))
		b.EndBuild()

		ir := fn.impl.String()
		wideCheck := strings.Index(ir, "icmp uge i64")
		narrow := strings.Index(ir, "trunc i64")
		if wideCheck < 0 || narrow < 0 || wideCheck > narrow {
			t.Errorf("%s checks the shift count after narrowing:\n%s", op, ir)
		}
	}
}
