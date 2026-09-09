package cl

import (
	"go/constant"
	"go/token"
	"go/types"

	"github.com/xgo-dev/llgo/internal/safepointplan"
	"golang.org/x/tools/go/ssa"
)

// Bound the expanded work, not just each function's body: a short chain of
// functions that each call the next twice must not become an unbounded region
// without a cooperative poll. Missing bodies, cycles and unknown effects all
// keep the existing instrumentation. No package-name whitelist is involved.
const wasmScalarLeafBudget = 128

func (p *context) isWasmScalarLeaf(fn *ssa.Function) bool {
	if p.prog.Target().GOARCH != "wasm" || fn == nil {
		return false
	}
	if p.wasmScalarCosts == nil {
		p.wasmScalarCosts = make(map[*ssa.Function]int)
	}
	return p.wasmScalarLeafCost(fn) > 0
}

func (p *context) wasmScalarLeafCost(fn *ssa.Function) int {
	if cost, ok := p.wasmScalarCosts[fn]; ok {
		return cost
	}
	// A recursive edge sees the provisional rejection, including mutual
	// recursion. This map belongs to one package's lowering context.
	p.wasmScalarCosts[fn] = -1
	if fn == nil || fn.Pkg == nil || len(fn.Blocks) == 0 || len(fn.Blocks) > wasmScalarLeafBudget || len(fn.FreeVars) != 0 {
		return -1
	}
	for _, directive := range []string{"go:linkname", "llgo:link", "go:wasmimport", "export", "llgo:env"} {
		if hasFuncDirective(fn, directive) {
			return -1
		}
	}
	// A file-level link directive can redirect a body without appearing in
	// its doc comment. Never use the source proof for a substituted symbol.
	name := funcName(fn.Pkg.Pkg, fn, false)
	if _, linked := p.prog.Linkname(name); linked {
		return -1
	}
	if _, _, imported := p.prog.WasmImport(name); imported {
		return -1
	}
	if fn.Signature.Recv() != nil {
		return -1
	}
	for _, tuple := range []*types.Tuple{fn.Signature.Params(), fn.Signature.Results()} {
		for i := 0; i < tuple.Len(); i++ {
			if !wasmScalarType(tuple.At(i).Type()) {
				return -1
			}
		}
	}
	if len(safepointplan.Backedges(fn)) != 0 {
		return -1
	}
	cost := 1
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			cost++
			switch instr := instr.(type) {
			case *ssa.Return, *ssa.If, *ssa.Jump, *ssa.DebugRef:
			case *ssa.Phi:
				if !wasmScalarType(instr.Type()) {
					return -1
				}
			case *ssa.Extract:
				if !wasmScalarType(instr.Type()) {
					return -1
				}
			case *ssa.ChangeType:
				if !wasmScalarType(instr.X.Type()) || !wasmScalarType(instr.Type()) {
					return -1
				}
			case *ssa.Convert:
				if !wasmScalarType(instr.X.Type()) || !wasmScalarType(instr.Type()) {
					return -1
				}
			case *ssa.UnOp:
				if !wasmScalarType(instr.X.Type()) || (instr.Op != token.SUB && instr.Op != token.NOT && instr.Op != token.XOR) {
					return -1
				}
			case *ssa.BinOp:
				if !wasmScalarBinOp(instr) {
					return -1
				}
			case *ssa.Call:
				callee := instr.Call.StaticCallee()
				if callee == nil {
					return -1
				}
				calleeCost := p.wasmScalarLeafCost(callee)
				if calleeCost < 0 {
					return -1
				}
				cost += calleeCost
			default:
				// No memory access, allocation, defer, panic, goroutine, channel,
				// interface dispatch, or other hidden runtime operation is allowed.
				return -1
			}
			if cost > wasmScalarLeafBudget {
				return -1
			}
		}
	}
	p.wasmScalarCosts[fn] = cost
	return cost
}

func wasmScalarType(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Info()&(types.IsBoolean|types.IsInteger|types.IsFloat) != 0
}

func wasmScalarBinOp(instr *ssa.BinOp) bool {
	if !wasmScalarType(instr.X.Type()) || !wasmScalarType(instr.Y.Type()) {
		return false
	}
	right, constantRight := instr.Y.(*ssa.Const)
	switch instr.Op {
	case token.QUO, token.REM:
		basic := types.Unalias(instr.X.Type()).Underlying().(*types.Basic)
		return basic.Info()&types.IsInteger == 0 || constantRight && constant.Sign(right.Value) != 0
	case token.SHL, token.SHR:
		basic := types.Unalias(instr.Y.Type()).Underlying().(*types.Basic)
		return basic.Info()&types.IsUnsigned != 0 || constantRight && constant.Sign(right.Value) >= 0
	}
	return true
}
