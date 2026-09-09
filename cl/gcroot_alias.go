package cl

import (
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// A separately published, unchanged base root retains its complete allocation.
// Initializers can otherwise reserve thousands of permanent root slots for
// different elements of one array, amplifying Asyncify's local-state graph.
// Do not use a root slot that can be overwritten by another loop iteration,
// or an SSA allocation that lowering may elide (notably C varargs).
func pruneStableWasmRootAliases(roots map[ssa.Value]struct{}) {
	stable := make(map[ssa.Value]bool)
	for value := range roots {
		base := wasmRootAliasBase(value)
		if base == value {
			continue
		}
		if _, planned := roots[base]; !planned {
			continue
		}
		ok, known := stable[base]
		if !known {
			switch base := base.(type) {
			case *ssa.Parameter:
				ok = true
			case *ssa.Alloc:
				ok = base.Heap && base.Comment != "varargs" && !blockCanRepeat(base.Block())
			}
			stable[base] = ok
		}
		if ok {
			delete(roots, value)
		}
	}
}

func wasmRootAliasBase(value ssa.Value) ssa.Value {
	for {
		switch address := value.(type) {
		case *ssa.FieldAddr:
			value = address.X
		case *ssa.IndexAddr:
			if _, pointer := types.Unalias(address.X.Type()).Underlying().(*types.Pointer); !pointer {
				// A slice descriptor is not its backing allocation.
				return value
			}
			value = address.X
		default:
			// Loads, calls, phi nodes, and unsafe conversions may designate
			// another object. Their roots remain independent.
			return value
		}
	}
}

func blockCanRepeat(block *ssa.BasicBlock) bool {
	if block == nil {
		return true
	}
	seen := make(map[*ssa.BasicBlock]bool)
	work := append([]*ssa.BasicBlock(nil), block.Succs...)
	for len(work) != 0 {
		next := work[len(work)-1]
		work = work[:len(work)-1]
		if next == block {
			return true
		}
		if !seen[next] {
			seen[next] = true
			work = append(work, next.Succs...)
		}
	}
	return false
}
