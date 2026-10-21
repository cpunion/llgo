// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// Only these source uses preserve exclusive access by the creating G. Build
// use edges from operands instead of trusting a possibly stale Referrers list.
// In particular, passing, storing, returning, capturing and selecting a channel
// reject the entire allocation, including its earlier send/receive sites.
func coroLocalSingleSlotChannels(fn *ssa.Function) map[ssa.Value]*ssa.MakeChan {
	var result map[ssa.Value]*ssa.MakeChan
	var allocations []*ssa.MakeChan
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			make, ok := instruction.(*ssa.MakeChan)
			if !ok {
				continue
			}
			capacity, ok := make.Size.(*ssa.Const)
			if !ok || capacity.Value == nil || capacity.Value.Kind() != constant.Int {
				continue
			}
			if size, exact := constant.Int64Val(capacity.Value); !exact || size != 1 {
				continue
			}
			element := make.Type().Underlying().(*types.Chan).Elem()
			basic, ok := element.Underlying().(*types.Basic)
			if ok && basic.Info()&(types.IsInteger|types.IsFloat) != 0 {
				allocations = append(allocations, make)
			}
		}
	}
	if len(allocations) == 0 {
		return result
	}
	uses := make(map[ssa.Value][]ssa.Instruction)
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			for _, operand := range instruction.Operands(nil) {
				if operand != nil && *operand != nil {
					if _, channel := (*operand).Type().Underlying().(*types.Chan); channel {
						uses[*operand] = append(uses[*operand], instruction)
					}
				}
			}
		}
	}
	for _, allocation := range allocations {
		aliases := map[ssa.Value]bool{allocation: true}
		queue := []ssa.Value{allocation}
		valid := true
		for len(queue) != 0 && valid {
			value := queue[0]
			queue = queue[1:]
			for _, use := range uses[value] {
				switch use := use.(type) {
				case *ssa.ChangeType:
					channel, ok := use.Type().Underlying().(*types.Chan)
					if use.X != value || !ok || !types.Identical(channel.Elem(), allocation.Type().Underlying().(*types.Chan).Elem()) {
						valid = false
					} else if !aliases[use] {
						aliases[use] = true
						queue = append(queue, use)
					}
				case *ssa.Send:
					valid = use.Chan == value && use.X != value
				case *ssa.UnOp:
					valid = use.Op == token.ARROW && use.X == value
				case *ssa.Call:
					builtin, ok := use.Common().Value.(*ssa.Builtin)
					valid = ok && len(use.Common().Args) == 1 && use.Common().Args[0] == value &&
						(builtin.Name() == "len" || builtin.Name() == "cap" || builtin.Name() == "close")
				case *ssa.DebugRef:
				default:
					valid = false
				}
				if !valid {
					break
				}
			}
		}
		if valid {
			if result == nil {
				result = make(map[ssa.Value]*ssa.MakeChan)
			}
			for alias := range aliases {
				result[alias] = allocation
			}
		}
	}
	return result
}

type coroLocalChannelProof struct {
	allocation  *ssa.MakeChan
	channel     ssa.Value
	elementSize int64
	// Field indices are selected from the authoritative runtime Go type before
	// LLVM emission. No numeric byte offsets or new runtime flags are used.
	fields map[string]int
}

func (plan *coroPhysicalFunctionPlan) validateLocalChannels() error {
	var aliases map[ssa.Value]*ssa.MakeChan
	for instruction, proof := range plan.localChannels {
		physical, exists := plan.instructions[instruction]
		if !exists || proof == nil {
			return fmt.Errorf("local channel proof has no exact physical instruction")
		}
		if aliases == nil {
			aliases = coroLocalSingleSlotChannels(plan.function)
		}
		var channel ssa.Value
		switch physical.operation {
		case coroPhysicalOperationChannelSend:
			if send, ok := instruction.(*ssa.Send); ok {
				channel = send.Chan
			}
		case coroPhysicalOperationChannelReceive:
			if recv, ok := instruction.(*ssa.UnOp); ok && recv.Op == token.ARROW {
				channel = recv.X
			}
		}
		if channel == nil || channel != proof.channel || proof.allocation == nil ||
			aliases[channel] != proof.allocation || len(proof.fields) != 9 ||
			(proof.elementSize != 4 && proof.elementSize != 8) {
			return fmt.Errorf("local channel proof no longer owns its exact nonescaping allocation at %q", instruction.String())
		}
	}
	return nil
}

func planCoroLocalChannelLayout(prog llssa.Program) map[string]int {
	runtimeType := prog.LookupRuntimeSourceType("Chan")
	if runtimeType == nil {
		return nil
	}
	if _, named := types.Unalias(runtimeType).(*types.Named); !named {
		return nil
	}
	structure, ok := runtimeType.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	fields := make(map[string]int)
	for i := 0; i < structure.NumFields(); i++ {
		field := structure.Field(i)
		valid := false
		switch field.Name() {
		case "qcount", "dataqsiz", "elemsize":
			valid = types.Identical(field.Type(), types.Typ[types.Int])
		case "buf":
			valid = types.Identical(field.Type(), types.Typ[types.UnsafePointer])
		case "closed", "timerSync", "synctest":
			valid = types.Identical(field.Type(), types.Typ[types.Bool])
		case "sendq", "recvq":
			queue, ok := field.Type().Underlying().(*types.Struct)
			if ok && queue.NumFields() > 0 && queue.Field(0).Name() == "first" {
				_, valid = queue.Field(0).Type().Underlying().(*types.Pointer)
			}
		}
		if valid {
			fields[field.Name()] = i
		}
	}
	if len(fields) != 9 {
		return nil
	}
	return fields
}

func (p *context) plannedCoroLocalChannel(instruction ssa.Instruction, channel ssa.Value) *coroLocalChannelProof {
	physical := p.coroEmissionPlan()
	current := p.coroEmissionSite()
	if physical == nil || current == nil || current.instruction != instruction || !current.hasExpectedPhysical {
		panic("local channel selection has no exact frozen physical site")
	}
	proof := physical.localChannels[instruction]
	if proof != nil && proof.channel != channel {
		panic("local channel changed its frozen operand")
	}
	return proof
}

// emitCoroLocalChannelBufferTry consumes an allocation-wide exclusive-use
// proof. Other goroutines, timer deliveries and callbacks cannot possess this
// channel. A preemption may suspend its sole user but cannot add a concurrent
// channel writer. The one-slot indices remain zero, including after a miss.
// Every uncertain state falls back to the original complete park transaction.
func (p *context) emitCoroLocalChannelBufferTry(
	b llssa.Builder, channel, element llssa.Expr, send bool, proof *coroLocalChannelProof,
) llssa.Expr {
	if proof == nil || proof.allocation == nil || proof.allocation.Parent() != p.goFn ||
		proof.channel == nil || proof.channel.Parent() != p.goFn ||
		(proof.elementSize != 4 && proof.elementSize != 8) || len(proof.fields) != 9 {
		panic("local channel lowering requires its exact frozen allocation proof")
	}
	inspect, ready, miss, joined := b.Func.MakeBlock(), b.Func.MakeBlock(), b.Func.MakeBlock(), b.Func.MakeBlock()
	b.If(b.BinOp(token.NEQ, channel, b.Prog.Zero(channel.Type)), inspect, miss)
	b.SetBlockEx(inspect, llssa.AtEnd, false)
	ch := b.Convert(b.Prog.Pointer(b.Prog.RuntimeType("Chan")), channel)
	field := func(name string) llssa.Expr {
		index, exists := proof.fields[name]
		if !exists {
			panic(fmt.Sprintf("local channel layout omitted %q", name))
		}
		return b.FieldAddrKnownNonNil(ch, index)
	}
	integer := func(value int64) llssa.Expr { return b.Prog.IntVal(uint64(value), b.Prog.Int()) }
	count, buffer := b.Load(field("qcount")), b.Load(field("buf"))
	expected := int64(0)
	queue := "recvq"
	if !send {
		expected, queue = 1, "sendq"
	}
	condition := b.BinOp(token.EQL, count, integer(expected))
	and := func(next llssa.Expr) { condition = b.BinOp(token.AND, condition, next) }
	and(b.BinOp(token.EQL, b.Load(field("dataqsiz")), integer(1)))
	and(b.BinOp(token.EQL, b.Load(field("elemsize")), integer(proof.elementSize)))
	and(b.BinOp(token.NEQ, buffer, b.Prog.Zero(buffer.Type)))
	for _, flag := range []string{"timerSync", "synctest"} {
		and(b.UnOp(token.NOT, b.Load(field(flag))))
	}
	if send {
		and(b.UnOp(token.NOT, b.Load(field("closed"))))
	}
	first := b.Load(b.FieldAddrKnownNonNil(field(queue), 0))
	and(b.BinOp(token.EQL, first, b.Prog.Zero(first.Type)))
	b.If(condition, ready, miss)

	b.SetBlockEx(ready, llssa.AtEnd, false)
	slot := b.Convert(element.Type, buffer)
	if send {
		b.Store(slot, b.Load(element))
		b.Store(field("qcount"), integer(1))
	} else {
		b.Store(element, b.Load(slot))
		// Conservative collectors may observe pointer-looking numeric bits even
		// when ordinary Go dataflow never reads this buffer slot again.
		b.StoreVolatile(slot, b.Prog.Zero(b.Prog.Elem(element.Type)))
		b.Store(field("qcount"), integer(0))
	}
	b.Jump(joined)
	b.SetBlockEx(miss, llssa.AtEnd, false)
	b.Jump(joined)
	b.SetBlockContinuation(joined)
	status := b.Phi(b.Prog.Uint32())
	status.AddIncoming(b, []llssa.BasicBlock{ready, miss}, func(index int, _ llssa.BasicBlock) llssa.Expr {
		value := uint64(coroChanResumeInvalid)
		if index == 0 {
			value = coroChanResumeRecvOK
			if send {
				value = coroChanResumeSendOK
			}
		}
		return b.Prog.IntVal(value, b.Prog.Uint32())
	})
	return status.Expr
}
