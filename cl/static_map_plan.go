/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/types"
	"sort"
	"strconv"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// StaticMapInitializer describes frozen evaluation and Go-reference facts.
// None of these fields alone authorizes omission: the final linker-input and
// visibility contract and a producer-bound cache fragment remain necessary.
// This API does not extend coroutine function-cell or raw-input authority.
type StaticMapInitializer struct {
	PackageIdentity   string
	PhysicalSymbol    string
	ConstructorID     string
	GateSymbol        string
	Entries           int
	GoUnread          bool
	ValueCellsDefined bool
	RawInputsClosed   bool
	Blockers          []string
}

type staticMapDataCell struct {
	owner   *preparedEmissionPackage
	symbol  string
	typeKey string
	id      string
	members []*ssa.Global
	closed  bool
}

type frozenStaticMapInitializer struct {
	facts        StaticMapInitializer
	interval     staticMapInitInterval
	instructions []ssa.Instruction
	operands     [][]ssa.Value
	cell         *staticMapDataCell
}

// StaticMapInitializers returns defensive copies and checks that the source
// interval still has its frozen operands and instruction order. A changed SSA
// graph must be replanned, never silently matched by a global's source name.
func (u *EmissionUniverse) StaticMapInitializers() ([]StaticMapInitializer, error) {
	if u == nil {
		return nil, fmt.Errorf("static map initializers require a frozen emission universe")
	}
	if len(u.staticMapInitializers) != 0 && u.staticMapReferenceHash != u.staticMapReferenceFingerprint() {
		return nil, fmt.Errorf("static map initializers changed their frozen Go references")
	}
	result := make([]StaticMapInitializer, len(u.staticMapInitializers))
	for i, plan := range u.staticMapInitializers {
		block := plan.interval.block
		if block == nil || plan.interval.end >= len(block.Instrs) ||
			plan.interval.end-plan.interval.start+1 != len(plan.instructions) {
			return nil, fmt.Errorf("static map initializer %q changed its source interval", plan.facts.PhysicalSymbol)
		}
		for j, instr := range plan.instructions {
			if block.Instrs[plan.interval.start+j] != instr {
				return nil, fmt.Errorf("static map initializer %q changed instruction order", plan.facts.PhysicalSymbol)
			}
			operands := instr.Operands(nil)
			if len(operands) != len(plan.operands[j]) {
				return nil, fmt.Errorf("static map initializer %q changed its operands", plan.facts.PhysicalSymbol)
			}
			for k, operand := range operands {
				if operand == nil || *operand != plan.operands[j][k] {
					return nil, fmt.Errorf("static map initializer %q changed a source operand", plan.facts.PhysicalSymbol)
				}
			}
		}
		result[i] = plan.facts
		result[i].Blockers = append([]string(nil), plan.facts.Blockers...)
	}
	return result, nil
}

func staticMapGlobalType(global *ssa.Global) bool {
	if global == nil {
		return false
	}
	pointer, ok := types.Unalias(global.Type()).(*types.Pointer)
	if !ok {
		return false
	}
	_, ok = pointer.Elem().Underlying().(*types.Map)
	return ok
}

func (u *EmissionUniverse) freezeStaticMapInitializers(
	groups map[string][]emissionGlobalPhysicalCandidate,
	linknameTargets map[string]none,
	rawAbsent func(string) bool,
) {
	// Group by the actual emitted symbol, including conflicts across package
	// owners. Keep this independent from the function-cell identity registry.
	bySymbol := make(map[string][]emissionGlobalPhysicalCandidate)
	for _, group := range groups {
		for _, candidate := range group {
			bySymbol[candidate.physicalSymbol] = append(bySymbol[candidate.physicalSymbol], candidate)
		}
	}
	byGlobal := make(map[*ssa.Global]*staticMapDataCell)
	for symbol, group := range bySymbol {
		first := group[0]
		cell := &staticMapDataCell{owner: first.owner, symbol: symbol, typeKey: first.structuralType, closed: true}
		_, linkAliased := linknameTargets[symbol]
		for _, candidate := range group {
			cell.members = append(cell.members, candidate.global)
			cell.closed = cell.closed && symbol != "" && candidate.owner != nil &&
				candidate.owner == first.owner && candidate.owner.identity != "" &&
				candidate.structuralType != "" && candidate.structuralType == first.structuralType &&
				candidate.background == llssa.InGo && candidate.define && !candidate.linknamed &&
				candidate.ordinaryData && !linkAliased
		}
		if cell.closed {
			cell.id = framedEmissionKey("cl-static-map-data-cell-v1", first.owner.identity, symbol, cell.typeKey)
		}
		for _, global := range cell.members {
			byGlobal[global] = cell
		}
	}

	var plans []frozenStaticMapInitializer
	writes := make(map[*staticMapDataCell]int)
	stores := make(map[*ssa.Store]*staticMapDataCell)
	for _, function := range u.functions {
		for _, interval := range collectStaticMapInitIntervals(function) {
			cell := byGlobal[interval.global]
			if cell == nil || !cell.closed {
				continue
			}
			facts := StaticMapInitializer{
				PackageIdentity: cell.owner.identity, PhysicalSymbol: cell.symbol,
				Entries: len(interval.updates), GoUnread: true,
				ValueCellsDefined: true, RawInputsClosed: rawAbsent(cell.symbol),
			}
			for _, load := range interval.globalLoads {
				dependency := byGlobal[load.X.(*ssa.Global)]
				if dependency == nil || !dependency.closed {
					facts.ValueCellsDefined = false
				}
			}
			if !facts.ValueCellsDefined {
				facts.Blockers = append(facts.Blockers, "value load lacks an exact ordinary defined Go cell")
			}
			if !facts.RawInputsClosed {
				facts.Blockers = append(facts.Blockers, "raw-input absence is unproved")
			}
			key := []string{"cl-static-map-constructor-v1", cell.id}
			for _, update := range interval.updates {
				key = append(key, update.Key.(*ssa.Const).Value.ExactString())
				if value, ok := update.Value.(*ssa.Const); ok {
					if value.Value == nil {
						key = append(key, "nil")
					} else {
						key = append(key, "constant", value.Value.ExactString())
					}
				} else {
					load := update.Value.(*ssa.UnOp)
					dependency := byGlobal[load.X.(*ssa.Global)]
					if dependency == nil || !dependency.closed {
						key = append(key, "unproved-value-cell")
					} else {
						key = append(key, "global-load", dependency.id)
					}
				}
			}
			if facts.ValueCellsDefined {
				digest := sha256.Sum256([]byte(framedEmissionKey(key...)))
				facts.ConstructorID = hex.EncodeToString(digest[:])
				facts.GateSymbol = "__llgo_static_map_init_live_v1_" + facts.ConstructorID
			}
			plan := frozenStaticMapInitializer{facts: facts, interval: interval, cell: cell}
			plan.instructions = append([]ssa.Instruction(nil), interval.block.Instrs[interval.start:interval.end+1]...)
			for _, instr := range plan.instructions {
				values := []ssa.Value{}
				for _, operand := range instr.Operands(nil) {
					if operand == nil {
						values = append(values, nil)
					} else {
						values = append(values, *operand)
					}
				}
				plan.operands = append(plan.operands, values)
			}
			plans = append(plans, plan)
			writes[cell]++
			stores[interval.store] = cell
		}
	}
	if len(plans) == 0 {
		return
	}

	observed := make(map[*staticMapDataCell]bool)
	unknownMapReference := false
	var operands [8]*ssa.Value
	for _, function := range u.functions {
		for _, block := range function.Blocks {
			for _, instr := range block.Instrs {
				if _, debug := instr.(*ssa.DebugRef); debug {
					continue
				}
				for _, operand := range instr.Operands(operands[:0]) {
					if operand == nil {
						continue
					}
					global, ok := (*operand).(*ssa.Global)
					if !ok {
						continue
					}
					cell := byGlobal[global]
					if cell == nil {
						unknownMapReference = unknownMapReference || staticMapGlobalType(global)
						continue
					}
					store, isStore := instr.(*ssa.Store)
					if !isStore || stores[store] != cell || store.Addr != global {
						observed[cell] = true
					}
				}
			}
		}
	}
	for i := range plans {
		plan := &plans[i]
		if observed[plan.cell] || writes[plan.cell] != 1 || unknownMapReference {
			plan.facts.GoUnread = false
			plan.facts.Blockers = append(plan.facts.Blockers, "Go use, multiple constructors, or unresolved physical map alias")
		}
	}
	sort.SliceStable(plans, func(i, j int) bool {
		left, right := plans[i].facts, plans[j].facts
		return framedEmissionKey(left.PackageIdentity, left.PhysicalSymbol, left.ConstructorID, strconv.Itoa(plans[i].interval.start)) <
			framedEmissionKey(right.PackageIdentity, right.PhysicalSymbol, right.ConstructorID, strconv.Itoa(plans[j].interval.start))
	})
	u.staticMapInitializers = plans
	u.staticMapReferenceHash = u.staticMapReferenceFingerprint()
}

// This process-local fingerprint detects changed read/escape facts; it is not
// a cache key. Stable canonical constructor IDs deliberately exclude consumers
// and raw-input eligibility so the retained producer remains reusable.
func (u *EmissionUniverse) staticMapReferenceFingerprint() string {
	hash := sha256.New()
	var operands [8]*ssa.Value
	for _, function := range u.functions {
		for _, block := range function.Blocks {
			for index, instr := range block.Instrs {
				if _, debug := instr.(*ssa.DebugRef); debug {
					continue
				}
				for operandIndex, operand := range instr.Operands(operands[:0]) {
					if operand != nil {
						if global, ok := (*operand).(*ssa.Global); ok {
							fmt.Fprintf(hash, "%p/%d/%d/%p/%d/%p;", function, block.Index, index, instr, operandIndex, global)
						}
					}
				}
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}
