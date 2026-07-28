//go:build !llgo

package build

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

const boundsChecksFixture = "./testdata/boundschecks"
const boundsChecksFixturePackage = "github.com/goplus/llgo/internal/build/testdata/boundschecks"

func TestDisableBoundsChecksIR(t *testing.T) {
	checked := boundsChecksModuleIR(t, false)
	unchecked := boundsChecksModuleIR(t, true)

	for _, helper := range []string{"CheckIndexRange", "StringSlice2", "NewSlice2", "NewSlice3Bounds", "PanicSliceConvert"} {
		if strings.Contains(unchecked.module, helper) {
			t.Errorf("-B IR unexpectedly contains bounds helper %q", helper)
		}
	}

	for _, function := range []string{
		"indexString", "indexSlice", "indexArray", "indexArrayPointer",
		"sliceString", "sliceSlice", "sliceArray", "sliceArrayPointer", "sliceThree",
	} {
		checkedBody := boundsChecksFunctionBody(t, checked, function)
		uncheckedBody := boundsChecksFunctionBody(t, unchecked, function)
		if checkedBody == uncheckedBody {
			t.Errorf("-B did not change the structured bounds lowering for %s", function)
		}
		for _, helper := range []string{"CheckIndexRange", "StringSlice2", "NewSlice2", "NewSlice3Bounds"} {
			if strings.Contains(uncheckedBody, helper) {
				t.Errorf("-B %s contains bounds helper %q:\n%s", function, helper, uncheckedBody)
			}
		}
	}
	for _, function := range []string{"shortSliceToArrayPointer", "shortSliceToArrayValue"} {
		checkedBody := boundsChecksFunctionBody(t, checked, function)
		uncheckedBody := boundsChecksFunctionBody(t, unchecked, function)
		if checkedBody == uncheckedBody {
			t.Errorf("-B did not change the structured conversion bounds lowering for %s", function)
		}
		if strings.Contains(uncheckedBody, "PanicSliceConvert") {
			t.Errorf("-B %s contains a conversion bounds check:\n%s", function, uncheckedBody)
		}
	}
	if body := boundsChecksFunctionBody(t, unchecked, "shortSliceToArrayValue"); !strings.Contains(body, "load [4 x i8]") {
		t.Errorf("slice-to-array value conversion does not dereference its converted pointer:\n%s", body)
	}

	for _, function := range []string{"indexArrayPointer", "sliceArrayPointer"} {
		body := boundsChecksFunctionBody(t, unchecked, function)
		if !strings.Contains(body, "icmp eq ptr") {
			t.Errorf("-B %s lost its structured *array nil check:\n%s", function, body)
		}
	}
	for _, width := range []string{"zext i8", "zext i16", "zext i32"} {
		if !strings.Contains(unchecked.module, width) {
			t.Errorf("-B IR does not retain integer-width conversion %q", width)
		}
	}
	for _, function := range []string{"makeUnsafeString", "makeUnsafeSlice"} {
		checkedBody := boundsChecksFunctionBody(t, checked, function)
		uncheckedBody := boundsChecksFunctionBody(t, unchecked, function)
		if strings.Count(checkedBody, "br i1") != strings.Count(uncheckedBody, "br i1") {
			t.Errorf("-B changed mandatory unsafe builtin checks in %s:\ndefault:\n%s\n-B:\n%s",
				function, checkedBody, uncheckedBody)
		}
	}
}

func TestDisableBoundsChecksLegalResultsMatchDefault(t *testing.T) {
	wantFields := []string{"98", "30", "10", "40", "bc", "2", "3", "2", "3", "2", "3", "2", "3", "10", "40", "20", "30"}
	checked := runBinary(t, buildBoundsChecksBinary(t, false))
	unchecked := runBinary(t, buildBoundsChecksBinary(t, true))
	if checked != unchecked {
		t.Fatalf("default and -B output differ:\ndefault %q\n-B      %q", checked, unchecked)
	}
	if fields := strings.Fields(unchecked); !reflect.DeepEqual(fields, wantFields) {
		t.Fatalf("legal -B results = %v, want %v", fields, wantFields)
	}
}

func TestDisableBoundsChecksShortSliceConversionsDoNotPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounds-disabled")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	conf := NewDefaultConf(ModeBuild)
	conf.OutFile = path
	conf.DisableBoundsChecks = true
	if _, err := Do([]string{"./testdata/boundschecks_convert_short"}, conf); err != nil {
		t.Fatalf("build short conversion fixture with bounds checks disabled: %v", err)
	}
	if output := runBinary(t, path); !reflect.DeepEqual(strings.Fields(output), []string{"1", "4", "1", "4"}) {
		fields := strings.Fields(output)
		t.Fatalf("short conversions with bounds checks disabled = %v, want [1 4 1 4]; output %q", fields, output)
	}
}

func TestDisableBoundsChecksRetainsRequiredPanics(t *testing.T) {
	output := runBinary(t, buildBoundsChecksBinaryFrom(t, "./testdata/boundschecks_required", true))
	want := []string{"true", "true", "true", "true"}
	if fields := strings.Fields(output); !reflect.DeepEqual(fields, want) {
		t.Fatalf("required -B panics = %v, want %v; output %q", fields, want, output)
	}
}

type boundsChecksIRSnapshot struct {
	module  string
	symbols map[string]string
}

var boundsChecksFunctions = []string{
	"indexString", "indexSlice", "indexArray", "indexArrayPointer",
	"sliceString", "sliceSlice", "sliceArray", "sliceArrayPointer", "sliceThree",
	"shortSliceToArrayPointer", "shortSliceToArrayValue",
	"makeUnsafeString", "makeUnsafeSlice",
}

func boundsChecksModuleIR(t *testing.T, disable bool) boundsChecksIRSnapshot {
	t.Helper()
	conf := NewDefaultConf(ModeGen)
	conf.DisableBoundsChecks = disable
	var ir string
	functionIDs := make(map[string]coro.FunctionID, len(boundsChecksFunctions))
	conf.CoroPlanObserver = func(pkg *ssa.Package, plan *coro.SSAPlan) {
		if pkg == nil || pkg.Pkg == nil || pkg.Pkg.Path() != boundsChecksFixturePackage {
			return
		}
		for _, name := range boundsChecksFunctions {
			if function := pkg.Func(name); function != nil {
				if id, ok := plan.FunctionID(function); ok {
					functionIDs[name] = id
				}
			}
		}
	}
	conf.ModuleHook = func(pkg Package) {
		if pkg.PkgPath == boundsChecksFixturePackage {
			ir = pkg.LPkg.String()
		}
	}
	pkgs, err := Do([]string{boundsChecksFixture}, conf)
	if err != nil {
		t.Fatalf("generate bounds-check IR (disabled=%v): %v", disable, err)
	}
	if len(pkgs) != 1 || pkgs[0].LPkg == nil {
		t.Fatalf("generate bounds-check IR (disabled=%v): packages = %#v", disable, pkgs)
	}
	defer pkgs[0].LPkg.Prog.Dispose()
	if ir == "" {
		t.Fatalf("generate bounds-check IR (disabled=%v): fixture module was not observed", disable)
	}
	summaries, err := coro.ParseLibraryEffectSummaryRecords(pkgs[0].CoroLibraryEffectRecords)
	if err != nil {
		t.Fatalf("parse bounds-check coroutine symbols (disabled=%v): %v", disable, err)
	}
	physical := make(map[coro.FunctionID]string)
	for _, summary := range summaries {
		for _, function := range summary.Functions {
			physical[function.ID] = function.PrimarySymbol
		}
	}
	symbols := make(map[string]string, len(boundsChecksFunctions))
	for _, name := range boundsChecksFunctions {
		id, ok := functionIDs[name]
		if !ok {
			t.Fatalf("bounds-check function %s has no FunctionID (disabled=%v)", name, disable)
		}
		symbol := physical[id]
		if symbol == "" {
			t.Fatalf("bounds-check function %s (%s) has no published primary symbol (disabled=%v)", name, id, disable)
		}
		symbols[name] = symbol
	}
	return boundsChecksIRSnapshot{module: ir, symbols: symbols}
}

func buildBoundsChecksBinary(t *testing.T, disable bool) string {
	t.Helper()
	return buildBoundsChecksBinaryFrom(t, boundsChecksFixture, disable)
}

func buildBoundsChecksBinaryFrom(t *testing.T, fixture string, disable bool) string {
	t.Helper()
	name := "checked"
	if disable {
		name = "unchecked"
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	conf := NewDefaultConf(ModeBuild)
	conf.OutFile = path
	conf.DisableBoundsChecks = disable
	if _, err := Do([]string{fixture}, conf); err != nil {
		t.Fatalf("build bounds-check fixture (disabled=%v): %v", disable, err)
	}
	return path
}

func boundsChecksFunctionBody(t *testing.T, snapshot boundsChecksIRSnapshot, name string) string {
	t.Helper()
	symbol := snapshot.symbols[name]
	if symbol == "" {
		t.Fatalf("no physical symbol recorded for %q", name)
	}
	return llvmFunctionBody(t, snapshot.module, symbol)
}

func llvmFunctionBody(t *testing.T, module, name string) string {
	t.Helper()
	marker := "@\"" + name + "\"("
	markerAt := 0
	start := -1
	for {
		next := strings.Index(module[markerAt:], marker)
		if next < 0 {
			break
		}
		markerAt += next
		lineStart := strings.LastIndex(module[:markerAt], "\n") + 1
		if strings.HasPrefix(module[lineStart:markerAt], "define ") {
			start = lineStart
			break
		}
		markerAt += len(marker)
	}
	if start < 0 {
		t.Fatalf("LLVM definition for %q not found", name)
	}
	end := strings.Index(module[markerAt:], "\n}")
	if end < 0 {
		t.Fatalf("end of LLVM definition for %q not found", name)
	}
	return module[start : markerAt+end+2]
}
