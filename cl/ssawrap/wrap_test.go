//go:build !llgo
// +build !llgo

package ssawrap

import (
	"bytes"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Test source code: contains simple functions for wrapping
const testSrc = `
package demo

func Add(a, b int) int {
	return a + b
}

func Greet(name string) string {
	return "Hello, " + name
}

func NoReturn(a int) {
	_ = a
}

func Pair(value int) (int, string) {
	return value, "value"
}

func Print(value string) {
	println(value)
}
`

// buildTestProgram builds an SSA program for testing
func buildTestProgram(t *testing.T) (*ssa.Program, *ssa.Package) {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "demo.go", testSrc, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	files := []*ast.File{f}

	pkg := types.NewPackage("demo", "")
	ssapkg, _, err := ssautil.BuildPackage(
		&types.Config{Importer: importer.Default()},
		fset, pkg, files, ssa.SanityCheckFunctions,
	)
	if err != nil {
		t.Fatalf("build error: %v", err)
	}

	return ssapkg.Prog, ssapkg
}

// TestMakeCallWrapper_Basic tests basic function wrapping
func TestMakeCallWrapper_Basic(t *testing.T) {
	prog, ssapkg := buildTestProgram(t)

	// Get the original Add function
	origFn := ssapkg.Func("Add")
	if origFn == nil {
		t.Fatal("Add function not found")
	}

	// Generate wrapper function
	wrapper := MakeCallWrapper(prog, origFn)
	if wrapper == nil {
		t.Fatal("MakeCallWrapper returned nil")
	}

	// Verify wrapper signature matches the original function
	if !types.Identical(wrapper.Signature, origFn.Signature) {
		t.Errorf("wrapper signature mismatch:\n got: %v\nwant: %v",
			wrapper.Signature, origFn.Signature)
	}

	// Verify wrapper has exactly 1 basic block
	if len(wrapper.Blocks) != 1 {
		t.Errorf("expected 1 block, got %d", len(wrapper.Blocks))
	}

	entry := wrapper.Blocks[0]

	// Verify basic block has 2 instructions: Call + Return
	if len(entry.Instrs) != 2 {
		t.Errorf("expected 2 instructions, got %d", len(entry.Instrs))
		for i, instr := range entry.Instrs {
			t.Logf("  instr[%d]: %T = %v", i, instr, instr)
		}
	}

	// Verify first instruction is Call
	call, ok := entry.Instrs[0].(*ssa.Call)
	if !ok {
		t.Fatalf("expected first instruction to be *ssa.Call, got %T", entry.Instrs[0])
	}

	// Verify Call invokes the original function
	if call.Call.Value != origFn {
		t.Errorf("call target mismatch: got %v, want %v", call.Call.Value, origFn)
	}

	// Verify Call arguments are wrapper parameters
	if len(call.Call.Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(call.Call.Args))
	}
	for i, arg := range call.Call.Args {
		if arg != wrapper.Params[i] {
			t.Errorf("arg[%d] mismatch: got %v, want %v", i, arg, wrapper.Params[i])
		}
	}
	for i, param := range wrapper.Params {
		if param == origFn.Params[i] {
			t.Fatalf("wrapper parameter %d reuses the original SSA node", i)
		}
		if param.Parent() != wrapper {
			t.Fatalf("wrapper parameter %d parent = %v, want wrapper", i, param.Parent())
		}
		refs := param.Referrers()
		if refs == nil || len(*refs) != 1 || (*refs)[0] != call {
			t.Fatalf("wrapper parameter %d referrers = %v, want call", i, refs)
		}
	}

	// Verify second instruction is Return with Call result
	ret, ok := entry.Instrs[1].(*ssa.Return)
	if !ok {
		t.Fatalf("expected second instruction to be *ssa.Return, got %T", entry.Instrs[1])
	}
	if len(ret.Results) != 1 || ret.Results[0] != call {
		t.Errorf("return results mismatch: got %v, want [%v]", ret.Results, call)
	}

	// Print SSA for readability verification
	var buf bytes.Buffer
	wrapper.WriteTo(&buf)
	ssastr := buf.String()
	t.Logf("Generated SSA:\n%s", ssastr)

	// Verify SSA text contains expected content
	if !strings.Contains(ssastr, "Add$wrapper") {
		t.Error("SSA output missing wrapper function name")
	}
	if !strings.Contains(ssastr, "demo.Add") {
		t.Error("SSA output missing original function call")
	}
	if !strings.Contains(ssastr, "return") {
		t.Error("SSA output missing return statement")
	}
}

func TestMakeCallWrapper_MultipleReturns(t *testing.T) {
	prog, ssapkg := buildTestProgram(t)
	origFn := ssapkg.Func("Pair")
	wrapper := MakeCallWrapper(prog, origFn)
	if !types.Identical(wrapper.Signature, origFn.Signature) {
		t.Fatalf("signature mismatch: got %v, want %v", wrapper.Signature, origFn.Signature)
	}
	entry := wrapper.Blocks[0]
	if len(entry.Instrs) != 4 {
		t.Fatalf("instructions = %d, want call + two extracts + return: %v", len(entry.Instrs), entry.Instrs)
	}
	call, ok := entry.Instrs[0].(*ssa.Call)
	if !ok {
		t.Fatalf("instruction 0 = %T, want *ssa.Call", entry.Instrs[0])
	}
	if !types.Identical(call.Type(), origFn.Signature.Results()) {
		t.Fatalf("call type = %v, want result tuple %v", call.Type(), origFn.Signature.Results())
	}
	results := make([]ssa.Value, 2)
	for i := range results {
		extract, ok := entry.Instrs[i+1].(*ssa.Extract)
		if !ok {
			t.Fatalf("instruction %d = %T, want *ssa.Extract", i+1, entry.Instrs[i+1])
		}
		if extract.Tuple != call || extract.Index != i || !types.Identical(extract.Type(), origFn.Signature.Results().At(i).Type()) {
			t.Fatalf("extract %d = tuple %v index %d type %v", i, extract.Tuple, extract.Index, extract.Type())
		}
		results[i] = extract
	}
	ret, ok := entry.Instrs[3].(*ssa.Return)
	if !ok {
		t.Fatalf("instruction 3 = %T, want *ssa.Return", entry.Instrs[3])
	}
	if len(ret.Results) != len(results) || ret.Results[0] != results[0] || ret.Results[1] != results[1] {
		t.Fatalf("return results = %v, want %v", ret.Results, results)
	}
	universe, err := coro.NewSSAEmissionUniverse(prog, []*ssa.Function{origFn, wrapper})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(prog, coro.Roots{{Function: wrapper, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: universe,
		FunctionIDs: coro.FunctionIDConfig{ResolveSynthetic: func(fn *ssa.Function) (string, bool, error) {
			if fn == wrapper {
				return "ssawrap-test-pair", true, nil
			}
			return "", false, nil
		}},
	})
	if err != nil {
		t.Fatalf("AnalyzeSSA wrapper: %v", err)
	}
	if _, ok := plan.FunctionPlan(wrapper); !ok {
		t.Fatal("wrapper is absent from analyzed plan")
	}
}

// TestMakeCallWrapper_StringReturn tests wrapping a function returning string
func TestMakeCallWrapper_StringReturn(t *testing.T) {
	prog, ssapkg := buildTestProgram(t)

	origFn := ssapkg.Func("Greet")
	if origFn == nil {
		t.Fatal("Greet function not found")
	}

	wrapper := MakeCallWrapper(prog, origFn)
	if wrapper == nil {
		t.Fatal("MakeCallWrapper returned nil")
	}

	// Verify signature
	if !types.Identical(wrapper.Signature, origFn.Signature) {
		t.Errorf("signature mismatch:\n got: %v\nwant: %v",
			wrapper.Signature, origFn.Signature)
	}

	// Verify instruction sequence
	entry := wrapper.Blocks[0]
	if len(entry.Instrs) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(entry.Instrs))
	}

	call := entry.Instrs[0].(*ssa.Call)
	ret := entry.Instrs[1].(*ssa.Return)

	if call.Call.Value != origFn {
		t.Error("call target mismatch")
	}
	if len(ret.Results) != 1 || ret.Results[0] != call {
		t.Error("return value mismatch")
	}
}

// TestMakeCallWrapper_NoReturn tests wrapping a function with no return value
func TestMakeCallWrapper_NoReturn(t *testing.T) {
	prog, ssapkg := buildTestProgram(t)

	origFn := ssapkg.Func("NoReturn")
	if origFn == nil {
		t.Fatal("NoReturn function not found")
	}

	wrapper := MakeCallWrapper(prog, origFn)
	if wrapper == nil {
		t.Fatal("MakeCallWrapper returned nil")
	}

	// Verify signature
	if !types.Identical(wrapper.Signature, origFn.Signature) {
		t.Errorf("signature mismatch:\n got: %v\nwant: %v",
			wrapper.Signature, origFn.Signature)
	}

	// Verify instruction sequence
	entry := wrapper.Blocks[0]
	if len(entry.Instrs) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(entry.Instrs))
	}

	call := entry.Instrs[0].(*ssa.Call)
	ret := entry.Instrs[1].(*ssa.Return)

	if call.Call.Value != origFn {
		t.Error("call target mismatch")
	}
	// No-return function: Return should have no results
	if len(ret.Results) != 0 {
		t.Errorf("expected 0 return results, got %d", len(ret.Results))
	}
}

// TestMakeCallWrapper_NilFunction tests passing nil function
func TestMakeCallWrapper_NilFunction(t *testing.T) {
	prog, _ := buildTestProgram(t)

	// Passing nil should panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil function, but got none")
		}
	}()

	MakeCallWrapper(prog, nil)
}

func TestMakeCallWrapperNamed(t *testing.T) {
	prog, ssapkg := buildTestProgram(t)
	wrapper := MakeCallWrapperNamed(prog, ssapkg.Func("Add"), "Add$wrapper$owner$key")
	if got, want := wrapper.Name(), "Add$wrapper$owner$key"; got != want {
		t.Fatalf("wrapper name = %q, want %q", got, want)
	}
}

func TestMakeValueCallWrapperNamed_Builtin(t *testing.T) {
	prog, ssapkg := buildTestProgram(t)
	printFn := ssapkg.Func("Print")
	var builtin *ssa.Builtin
	for _, block := range printFn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			builtin, _ = call.Common().Value.(*ssa.Builtin)
			if builtin != nil {
				break
			}
		}
	}
	if builtin == nil || builtin.Name() != "println" {
		t.Fatalf("println builtin not found in %v", printFn)
	}
	parameter := types.NewVar(token.NoPos, nil, "value", types.Typ[types.String])
	signature := types.NewSignatureType(
		nil, nil, nil, types.NewTuple(parameter), types.NewTuple(), false,
	)
	wrapper := MakeValueCallWrapperNamed(prog, builtin, signature, "println$typed$carrier")
	if wrapper.Synthetic != "wrapper" || wrapper.Name() != "println$typed$carrier" ||
		!types.Identical(wrapper.Signature, signature) {
		t.Fatalf("wrapper identity = name %q synthetic %q signature %v", wrapper.Name(), wrapper.Synthetic, wrapper.Signature)
	}
	if len(wrapper.Blocks) != 1 || len(wrapper.Blocks[0].Instrs) != 2 || len(wrapper.Params) != 1 {
		t.Fatalf("wrapper shape = blocks %d params %d instructions %v", len(wrapper.Blocks), len(wrapper.Params), wrapper.Blocks[0].Instrs)
	}
	call, ok := wrapper.Blocks[0].Instrs[0].(*ssa.Call)
	if !ok || call.Common().Value != builtin || len(call.Common().Args) != 1 || call.Common().Args[0] != wrapper.Params[0] {
		t.Fatalf("forwarding call = %#v", wrapper.Blocks[0].Instrs[0])
	}
	ret, ok := wrapper.Blocks[0].Instrs[1].(*ssa.Return)
	if !ok || len(ret.Results) != 0 {
		t.Fatalf("terminal instruction = %#v", wrapper.Blocks[0].Instrs[1])
	}
}

// TestMakeCallWrapper_Referrers verifies Value reference relationships are correct
func TestMakeCallWrapper_Referrers(t *testing.T) {
	prog, ssapkg := buildTestProgram(t)

	origFn := ssapkg.Func("Add")
	wrapper := MakeCallWrapper(prog, origFn)
	entry := wrapper.Blocks[0]
	call := entry.Instrs[0].(*ssa.Call)

	// Verify Call's Referrers include Return
	refs := call.Referrers()
	if refs == nil {
		t.Fatal("call.Referrers() is nil")
	}

	foundReturn := false
	for _, ref := range *refs {
		if _, ok := ref.(*ssa.Return); ok {
			foundReturn = true
			break
		}
	}
	if !foundReturn {
		t.Error("Return instruction not found in Call's referrers")
	}
}
