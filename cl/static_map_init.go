/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0.
 */

package cl

import (
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// A constructor interval is only a local evaluation proof. It does not prove
// that the destination is unread, close physical aliases or foreign consumers,
// or authorize suppressing an initializer. Those need a separate frozen plan.
// In particular, globalLoads must resolve to ordinary defined Go cells before
// a future lowering may put this interval behind a canonical retain gate.
type staticMapInitInterval struct {
	global      *ssa.Global
	allocation  *ssa.MakeMap
	store       *ssa.Store
	block       *ssa.BasicBlock
	start, end  int
	updates     []*ssa.MapUpdate
	globalLoads []*ssa.UnOp
}

const maxStaticMapInitEntries = 1 << 16

func collectStaticMapInitIntervals(fn *ssa.Function) []staticMapInitInterval {
	if fn == nil || fn.Pkg == nil || fn != fn.Pkg.Func("init") || fn.Synthetic != "package initializer" {
		return nil
	}
	var intervals []staticMapInitInterval
	for _, block := range fn.Blocks {
		positions := make(map[ssa.Instruction]int, len(block.Instrs))
		for i, instr := range block.Instrs {
			positions[instr] = i
		}
		for i, instr := range block.Instrs {
			store, ok := instr.(*ssa.Store)
			if !ok {
				continue
			}
			if interval, ok := recognizeStaticMapInitInterval(fn, block, positions, i, store); ok {
				intervals = append(intervals, interval)
			}
		}
	}
	return intervals
}

func recognizeStaticMapInitInterval(
	fn *ssa.Function, block *ssa.BasicBlock, positions map[ssa.Instruction]int,
	end int, store *ssa.Store,
) (staticMapInitInterval, bool) {
	fail := staticMapInitInterval{}
	global, ok := store.Addr.(*ssa.Global)
	if !ok || global.Pkg != fn.Pkg {
		return fail, false
	}
	allocation, ok := store.Val.(*ssa.MakeMap)
	if !ok || allocation.Block() != block || allocation.Referrers() == nil {
		return fail, false
	}
	mapType, ok := allocation.Type().Underlying().(*types.Map)
	if !ok {
		return fail, false
	}
	keyType, ok := mapType.Key().Underlying().(*types.Basic)
	if !ok || keyType.Kind() != types.String {
		return fail, false
	}
	reserve, ok := allocation.Reserve.(*ssa.Const)
	if !ok || reserve.Value == nil || reserve.Value.Kind() != constant.Int {
		return fail, false
	}
	count, exact := constant.Int64Val(reserve.Value)
	if !exact || count <= 0 || count > maxStaticMapInitEntries {
		return fail, false
	}
	start, found := positions[allocation]
	if !found || start >= end || end-start > 6*int(count)+4 {
		return fail, false
	}
	interval := staticMapInitInterval{
		global: global, allocation: allocation, store: store,
		block: block, start: start, end: end,
	}
	allowed := map[ssa.Instruction]bool{allocation: true, store: true}
	loads := make(map[*ssa.UnOp]bool)
	for _, use := range *allocation.Referrers() {
		pos, exists := positions[use]
		if !exists || pos < start || pos > end {
			return fail, false
		}
		switch use := use.(type) {
		case *ssa.Store:
			if use != store || use.Val != allocation {
				return fail, false
			}
		case *ssa.DebugRef:
			// Debug references have no language evaluation effect.
		case *ssa.MapUpdate:
			key, ok := use.Key.(*ssa.Const)
			if use.Map != allocation || !ok || key.Value == nil || key.Value.Kind() != constant.String {
				return fail, false
			}
			switch value := use.Value.(type) {
			case *ssa.Const:
				// Includes typed nil values; no dynamic interface construction.
			case *ssa.UnOp:
				cell, ok := value.X.(*ssa.Global)
				if value.Op != token.MUL || value.CommaOk || !ok || cell.Pkg == nil || value.Referrers() == nil {
					return fail, false
				}
				loadPos, exists := positions[value]
				if !exists || loadPos <= start || loadPos >= end {
					return fail, false
				}
				loads[value] = true
			default:
				return fail, false
			}
			interval.updates = append(interval.updates, use)
		default:
			return fail, false
		}
		allowed[use] = true
	}
	if int64(len(interval.updates)) != count {
		return fail, false
	}
	for load := range loads {
		allowed[load] = true
		for _, use := range *load.Referrers() {
			if _, debug := use.(*ssa.DebugRef); debug {
				pos, found := positions[use]
				if !found || pos < start || pos > end {
					return fail, false
				}
				allowed[use] = true
			} else if !allowed[use] {
				// Skipping a load needed outside this interval changes evaluation.
				return fail, false
			}
		}
	}
	// Use source order for the descriptor, independent of referrer/map ordering.
	interval.updates = interval.updates[:0]
	for _, instr := range block.Instrs[start : end+1] {
		if debug, ok := instr.(*ssa.DebugRef); ok {
			if value, ok := debug.X.(ssa.Instruction); !ok || !allowed[value] {
				return fail, false
			}
			continue
		}
		if !allowed[instr] {
			return fail, false
		}
		switch instr := instr.(type) {
		case *ssa.MapUpdate:
			interval.updates = append(interval.updates, instr)
		case *ssa.UnOp:
			interval.globalLoads = append(interval.globalLoads, instr)
		}
	}
	return interval, true
}
