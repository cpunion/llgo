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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/token"
	"sort"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const staticMapProducerMetadata = "llgo.static.map.initializers.v1"

// StaticMapInitProducer is a canonical package producer, independent of any
// consumer's reachability. Capturing it does not authorize an omit fragment.
type StaticMapInitProducer struct {
	Version         int
	PackageIdentity string
	PhysicalSymbol  string
	ConstructorID   string
	GateSymbol      string
	Entries         int
}

type staticMapGateRegion struct {
	producer StaticMapInitProducer
	end      *ssa.Store
	after    llssa.BasicBlock
}

func (p *context) staticMapGateRegions(block *ssa.BasicBlock, start, count int) map[ssa.Instruction]*staticMapGateRegion {
	if !p.options.CanonicalStaticMapInits || p.emissionUniverse == nil || p.pyMod != "" ||
		block == nil || block.Parent() == nil || block.Parent().Synthetic != "package initializer" {
		return nil
	}
	u := p.emissionUniverse
	var selected []*frozenStaticMapInitializer
	for i := range u.staticMapInitializers {
		plan := &u.staticMapInitializers[i]
		if plan.interval.block == block && plan.interval.start >= start && plan.interval.end < start+count &&
			plan.facts.ValueCellsDefined && plan.facts.ConstructorID != "" {
			selected = append(selected, plan)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	if _, err := u.StaticMapInitializers(); err != nil {
		panic(fmt.Errorf("canonical map constructor emission: %w", err))
	}
	regions := make(map[ssa.Instruction]*staticMapGateRegion, len(selected))
	for _, plan := range selected {
		valid := true
		for _, instr := range plan.instructions {
			if _, skipped := p.unevaluatedSSA[instr]; skipped {
				valid = false
			}
		}
		if !valid {
			continue
		}
		producer := StaticMapInitProducer{
			Version: 1, PackageIdentity: plan.facts.PackageIdentity,
			PhysicalSymbol: plan.facts.PhysicalSymbol,
			ConstructorID:  plan.facts.ConstructorID, GateSymbol: plan.facts.GateSymbol,
			Entries: plan.facts.Entries,
		}
		regions[plan.interval.allocation] = &staticMapGateRegion{producer: producer, end: plan.interval.store}
	}
	return regions
}

func validateStaticMapProducer(producer StaticMapInitProducer) error {
	digest, err := hex.DecodeString(producer.ConstructorID)
	if err != nil || len(digest) != 32 || producer.Version != 1 || producer.PackageIdentity == "" ||
		producer.PhysicalSymbol == "" || producer.Entries <= 0 || producer.Entries > maxStaticMapInitEntries ||
		producer.GateSymbol != "__llgo_static_map_init_live_v1_"+producer.ConstructorID {
		return fmt.Errorf("invalid canonical map initializer producer")
	}
	return nil
}

func validateStaticMapGateGlobal(mod llvm.Module, producer StaticMapInitProducer) error {
	global := mod.NamedGlobal(producer.GateSymbol)
	if global.IsNil() || global.GlobalValueType() != mod.Context().Int8Type() ||
		global.IsGlobalConstant() || global.Linkage() != llvm.WeakAnyLinkage ||
		global.Initializer().IsNil() || global.Initializer().IsAConstantInt().IsNil() ||
		global.Initializer().ZExtValue() != 1 {
		return fmt.Errorf("canonical map initializer %q lacks its mutable weak retain-one gate", producer.PhysicalSymbol)
	}
	return nil
}

// StaticMapInitProducers validates the exact pre-optimization producer globals
// and metadata. Optional cache transport must additionally bind these records
// to the package artifact and compiler identity; unknown producers retain.
func StaticMapInitProducers(pkg llssa.Package) ([]StaticMapInitProducer, error) {
	if pkg == nil {
		return nil, fmt.Errorf("canonical map producers require a package")
	}
	mod := pkg.Module()
	nodes := mod.NamedMetadataOperands(staticMapProducerMetadata)
	result := make([]StaticMapInitProducer, 0, len(nodes))
	seen := make(map[string]bool)
	for _, node := range nodes {
		operands := node.MDNodeOperands()
		if len(operands) != 1 || !operands[0].IsAMDString() {
			return nil, fmt.Errorf("invalid canonical map initializer metadata")
		}
		data := operands[0].MDString()
		var producer StaticMapInitProducer
		if len(data) > 8192 || json.Unmarshal([]byte(data), &producer) != nil {
			return nil, fmt.Errorf("invalid canonical map initializer metadata payload")
		}
		canonical, err := json.Marshal(producer)
		if err != nil || string(canonical) != data {
			return nil, fmt.Errorf("noncanonical map initializer producer metadata")
		}
		if err := validateStaticMapProducer(producer); err != nil {
			return nil, err
		}
		if seen[producer.GateSymbol] {
			return nil, fmt.Errorf("duplicate canonical map initializer producer %q", producer.GateSymbol)
		}
		seen[producer.GateSymbol] = true
		if err := validateStaticMapGateGlobal(mod, producer); err != nil {
			return nil, err
		}
		result = append(result, producer)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GateSymbol < result[j].GateSymbol })
	return result, nil
}

func (p *context) beginStaticMapGate(b llssa.Builder, gate *staticMapGateRegion) {
	producer := gate.producer
	if err := validateStaticMapProducer(producer); err != nil {
		panic(err)
	}
	mod := p.pkg.Module()
	global := mod.NamedGlobal(producer.GateSymbol)
	if global.IsNil() {
		variable := p.pkg.NewVarEx(producer.GateSymbol, p.prog.Pointer(p.prog.Byte()))
		variable.Init(p.prog.IntVal(1, p.prog.Byte()))
		global = mod.NamedGlobal(producer.GateSymbol)
		global.SetLinkage(llvm.WeakAnyLinkage)
		global.SetGlobalConstant(false)
		data, err := json.Marshal(producer)
		if err != nil {
			panic(err)
		}
		ctx := mod.Context()
		mod.AddNamedMetadataOperand(staticMapProducerMetadata, ctx.MDNode([]llvm.Metadata{ctx.MDString(string(data))}))
	} else {
		// A declaration or a same-named source global is not a producer receipt.
		producers, err := StaticMapInitProducers(p.pkg)
		if err != nil {
			panic(err)
		}
		found := false
		for _, existing := range producers {
			found = found || existing == producer
		}
		if !found {
			panic("canonical map initializer gate collides with an unrelated global")
		}
	}
	if err := validateStaticMapGateGlobal(mod, producer); err != nil {
		panic(err)
	}
	variable := p.pkg.VarOf(producer.GateSymbol)
	if variable == nil {
		panic("canonical map initializer gate lacks its package binding")
	}
	blocks := p.fn.MakeBlocks(2)
	gate.after = blocks[1]
	condition := b.BinOp(token.NEQ, b.Load(variable.Expr), p.prog.IntVal(0, p.prog.Byte()))
	b.If(condition, blocks[0], blocks[1])
	b.SetBlockContinuation(blocks[0])
}

func finishStaticMapGate(b llssa.Builder, instr ssa.Instruction, active **staticMapGateRegion) {
	if *active == nil || instr != (*active).end {
		return
	}
	gate := *active
	if gate.after == nil {
		panic("canonical map initializer gate has no continuation")
	}
	b.Jump(gate.after)
	b.SetBlockContinuation(gate.after)
	*active = nil
}
