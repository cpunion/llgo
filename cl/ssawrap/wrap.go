package ssawrap

import (
	"go/token"
	"go/types"
	"unsafe"

	"golang.org/x/tools/go/ssa"
)

type domInfo struct {
	idom      *ssa.BasicBlock   // immediate dominator (parent in domtree)
	children  []*ssa.BasicBlock // nodes immediately dominated by this one
	pre, post int32             // pre- and post-order numbering within domtree
}

type _BasicBlock struct {
	Index        int                // index of this block within Parent().Blocks
	Comment      string             // optional label; no semantic significance
	parent       *ssa.Function      // parent function
	Instrs       []ssa.Instruction  // instructions in order
	Preds, Succs []*ssa.BasicBlock  // predecessors and successors
	succs2       [2]*ssa.BasicBlock // initial space for Succs
	dom          domInfo            // dominator tree info
	gaps         int                // number of nil Instrs (transient)
	rundefers    int                // number of rundefers (transient)
}

type anInstruction struct {
	block *ssa.BasicBlock // the basic block of this instruction
}

type _Return struct {
	anInstruction
	Results []ssa.Value
	pos     token.Pos
}

type register struct {
	anInstruction
	num       int        // "name" of virtual register, e.g. "t0".  Not guaranteed unique.
	typ       types.Type // type of virtual register
	pos       token.Pos  // position of source expression, or NoPos
	referrers []ssa.Instruction
}

type _Call struct {
	register
	Call ssa.CallCommon
}

type _Extract struct {
	register
	Tuple ssa.Value
	Index int
}

type _Parameter struct {
	name      string
	object    *types.Var
	typ       types.Type
	parent    *ssa.Function
	referrers []ssa.Instruction
}

func MakeCallWrapper(prog *ssa.Program, f *ssa.Function) *ssa.Function {
	return MakeCallWrapperNamed(prog, f, f.Name()+"$wrapper")
}

// MakeCallWrapperNamed creates the same forwarding wrapper as MakeCallWrapper
// with an explicit SSA/linker name. Frontends use it when multiple distinct
// callees with the same short Go name need owner-scoped wrappers.
func MakeCallWrapperNamed(prog *ssa.Program, f *ssa.Function, name string) *ssa.Function {
	parameters := make([]wrapperParameter, len(f.Params))
	for i, original := range f.Params {
		parameters[i] = wrapperParameter{
			name:   original.Name(),
			object: original.Object(),
			typ:    original.Type(),
		}
	}
	return makeValueCallWrapperNamed(prog, f, f.Signature, parameters, name)
}

// MakeValueCallWrapperNamed creates a forwarding wrapper for a callable SSA
// value whose effective signature is known at one exact call site. It is used
// for inline-only builtins that must become independently schedulable `go`
// roots; ordinary function wrappers should continue to use
// MakeCallWrapperNamed so parameter object identity is preserved.
func MakeValueCallWrapperNamed(prog *ssa.Program, value ssa.Value, signature *types.Signature, name string) *ssa.Function {
	parameters := make([]wrapperParameter, 0, signature.Params().Len()+1)
	if receiver := signature.Recv(); receiver != nil {
		parameters = append(parameters, wrapperParameter{
			name: receiver.Name(), object: receiver, typ: receiver.Type(),
		})
	}
	for i := 0; i < signature.Params().Len(); i++ {
		parameter := signature.Params().At(i)
		parameters = append(parameters, wrapperParameter{
			name: parameter.Name(), object: parameter, typ: parameter.Type(),
		})
	}
	return makeValueCallWrapperNamed(prog, value, signature, parameters, name)
}

type wrapperParameter struct {
	name   string
	object types.Object
	typ    types.Type
}

func makeValueCallWrapperNamed(
	prog *ssa.Program,
	value ssa.Value,
	signature *types.Signature,
	parameters []wrapperParameter,
	name string,
) *ssa.Function {
	fn := prog.NewFunction(name, signature, "wrapper")
	entry := &ssa.BasicBlock{
		Index:   0,
		Comment: "entry",
	}
	(*_BasicBlock)(unsafe.Pointer(entry)).parent = fn
	fn.Blocks = append(fn.Blocks, entry)
	args := make([]ssa.Value, 0, len(parameters))
	fn.Params = make([]*ssa.Parameter, len(parameters))
	for i, original := range parameters {
		param := &ssa.Parameter{}
		parameter := (*_Parameter)(unsafe.Pointer(param))
		parameter.name = original.name
		parameter.object, _ = original.object.(*types.Var)
		parameter.typ = original.typ
		parameter.parent = fn
		fn.Params[i] = param
		args = append(args, param)
	}
	call := &ssa.Call{
		Call: ssa.CallCommon{
			Value: value,
			Args:  args,
		},
	}
	callImpl := (*_Call)(unsafe.Pointer(call))
	callImpl.block = entry
	results := signature.Results()
	resultCount := 0
	if results != nil {
		resultCount = results.Len()
	} else {
		results = types.NewTuple()
	}
	if resultCount == 1 {
		callImpl.typ = results.At(0).Type()
	} else {
		callImpl.typ = results
	}
	for _, param := range fn.Params {
		parameter := (*_Parameter)(unsafe.Pointer(param))
		parameter.referrers = append(parameter.referrers, call)
	}
	entry.Instrs = append(entry.Instrs, call)
	returnValues := make([]ssa.Value, 0, resultCount)
	switch resultCount {
	case 0:
	case 1:
		returnValues = append(returnValues, call)
	default:
		for i := 0; i < resultCount; i++ {
			extract := &ssa.Extract{Tuple: call, Index: i}
			extractImpl := (*_Extract)(unsafe.Pointer(extract))
			extractImpl.block = entry
			extractImpl.num = i + 1
			extractImpl.typ = results.At(i).Type()
			callImpl.referrers = append(callImpl.referrers, extract)
			entry.Instrs = append(entry.Instrs, extract)
			returnValues = append(returnValues, extract)
		}
	}
	ret := &ssa.Return{Results: returnValues}
	for _, result := range returnValues {
		switch result := result.(type) {
		case *ssa.Call:
			impl := (*_Call)(unsafe.Pointer(result))
			impl.referrers = append(impl.referrers, ret)
		case *ssa.Extract:
			impl := (*_Extract)(unsafe.Pointer(result))
			impl.referrers = append(impl.referrers, ret)
		}
	}
	(*_Return)(unsafe.Pointer(ret)).block = entry
	entry.Instrs = append(entry.Instrs, ret)
	return fn
}
