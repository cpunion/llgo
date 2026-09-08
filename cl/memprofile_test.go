//go:build !llgo

package cl

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestWasmMemProfileCaptureSeparatesGLSResolver(t *testing.T) {
	// Compile the production capture source with minimal stand-ins for the
	// runtime data. A source-level guard alone is insufficient: locality
	// lowering resolves a function's GLS package block in its entry block.
	source, err := os.ReadFile("../runtime/internal/runtime/caller_memprofile_wasm.go")
	if err != nil {
		t.Fatal(err)
	}
	const stubs = `
type runtimeContext struct{}
type g struct { context *runtimeContext }
var currentG *g
type CallerFrame struct { captured uintptr }
type callerLocationStore struct { stack []CallerFrame }
//llgo:gls
var callerLocationStoreCurrent *callerLocationStore
const callersPCValue = uintptr(3)
func callerSyntheticRegistryFor(s *callerLocationStore) *callerLocationStore { return s }
func (s *callerLocationStore) capturePC(f *CallerFrame, v uintptr) uintptr { return f.captured | v }
var wasmMemProfileEnabled bool
func MemProfilePause() {}
func MemProfileResume() {}
func PushCallerLocationFrame(uintptr, string, string, int) int { return len(callerLocationStoreCurrent.stack) }
func PopCallerLocationFrame(int) { callerLocationStoreCurrent = nil }
func RecordCallerLocation(uintptr, string, string, int) { callerLocationStoreCurrent = nil }
func RecordPanicLocation(uintptr, string, string, int) { callerLocationStoreCurrent = nil }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "capture.go", string(source)+stubs, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	wrappers, err := parser.ParseFile(fset, "../runtime/internal/runtime/caller_wasm.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{file, wrappers}
	info := newLocalityTypeInfo()
	pkg, err := (&types.Config{Importer: importer.Default()}).Check("example.com/capture", fset, files, info)
	if err != nil {
		t.Fatal(err)
	}
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "js", GOARCH: "wasm"})
	defer prog.Dispose()
	prog.SetRuntime(func() *types.Package {
		rt, err := importer.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal(err)
		}
		// Host runtime exports supply the usual slice and panic ABI; the
		// logical-G resolver exists only in the Wasm runtime source set.
		rt.Scope().Insert(localityRuntimePackage().Scope().Lookup("GoroutineLocalPackage"))
		return rt
	})
	prog.EnableLogicalGoroutineLocality(true)
	if err := ParsePkgSyntax(prog, fset, pkg, files); err != nil {
		t.Fatal(err)
	}
	if err := PrepareLocalVariables(prog, fset, pkg, info, files); err != nil {
		t.Fatal(err)
	}
	goProg := ssa.NewProgram(fset, ssa.SanityCheckFunctions)
	goProg.CreatePackage(types.Unsafe, nil, nil, true)
	ssaPkg := goProg.CreatePackage(pkg, files, info, true)
	ssaPkg.Build()
	compiled, err := NewPackage(prog, ssaPkg, files)
	if err != nil {
		t.Fatal(err)
	}
	outer := compiled.Module().NamedFunction("example.com/capture.memProfileCaptureStack").String()
	inner := compiled.Module().NamedFunction("example.com/capture.memProfileCaptureActiveStack").String()
	if strings.Contains(outer, "__llgo_gls_block") || strings.Contains(outer, "GoroutineLocalPackage") {
		t.Fatalf("raw G guard resolves GLS before checking its context:\n%s", outer)
	}
	if !strings.Contains(outer, ".memProfileCaptureActiveStack") || !strings.Contains(outer, ".memProfileHasCurrentG") {
		t.Fatalf("capture does not guard a separate active-stack call:\n%s", outer)
	}
	guard := compiled.Module().NamedFunction("example.com/capture.memProfileHasCurrentG").String()
	if strings.Contains(guard, "__llgo_gls_block") || !strings.Contains(guard, ".currentG") {
		t.Fatalf("availability check must read the raw G without GLS:\n%s", guard)
	}
	if !strings.Contains(inner, "__llgo_gls_block") {
		t.Fatalf("active-stack helper does not resolve GLS:\n%s", inner)
	}
	if !strings.Contains(inner, "; Function Attrs: noinline") {
		t.Fatalf("active-stack boundary may be inlined before the guard:\n%s", inner)
	}
	for _, name := range []string{"PushCallerLocationFrame", "PopCallerLocationFrame", "RecordCallerLocation", "RecordPanicLocation"} {
		body := compiled.Module().NamedFunction("example.com/capture." + name + "Wasm").String()
		if strings.Contains(body, "__llgo_gls_block") || !strings.Contains(body, ".memProfileHasCurrentG") {
			t.Errorf("%s must guard the raw G without resolving GLS:\n%s", name, body)
		}
		if !strings.Contains(body, "; Function Attrs: noinline") || !strings.Contains(body, "example.com/capture."+name+"\"") {
			t.Errorf("%s must retain a guarded non-inline runtime boundary:\n%s", name, body)
		}
	}
}

func TestWasmMemProfileFramePop(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "js", GOARCH: "wasm"})
		prog.EnableGCRoots(true)
		prog.EnableWasmMemoryProfiling(enabled)
		pkg := prog.NewPackage("foo", "example.com/foo")
		fn := pkg.NewFunc("example.com/foo.f", llssa.NoArgsNoRet, llssa.InGo)
		b := fn.MakeBody(1)
		ctx := &context{prog: prog, pkg: pkg, fn: fn, callerFrameMark: prog.Val(0)}
		ctx.popCallerLocationFrame(b)
		b.Return()
		b.EndBuild()
		body := pkg.Module().NamedFunction("example.com/foo.f").String()
		if got := strings.Contains(body, ".PopCallerLocationFrameWasm"); got != enabled {
			t.Fatalf("profile=%v uses guarded Pop=%v:\n%s", enabled, got, body)
		}
		prog.Dispose()
	}
}

func TestMemProfileConsumer(t *testing.T) {
	for _, test := range []struct {
		name, path, source string
		want               bool
	}{
		{"direct", "user", `package p; import "runtime"; func F(){ runtime.MemProfile(nil, true) }`, true},
		{"function value", "user", `package p; import "runtime"; var Read = runtime.MemProfile`, true},
		{"rate address", "user", `package p; import "runtime"; var Rate = &runtime.MemProfileRate`, true},
		{"method", "user", `package p; import "runtime"; type T int; func(T) Read(){runtime.MemProfile(nil, false)}`, true},
		{"closure", "user", `package p; import "runtime"; func F() func(){ return func(){runtime.MemProfileRate=1} }`, true},
		{"generic", "user", `package p; import "runtime"; func F[T any](v T){runtime.MemProfile(nil, false)}; func Use(){F(1)}`, true},
		{"pprof linkname", "runtime/pprof", `package pprof`, true},
		{"provider", "runtime", `package runtime; var MemProfileRate int; func MemProfile([]int,bool){}`, false},
		{"patched provider", "github.com/xgo-dev/llgo/runtime/internal/lib/runtime", `package runtime`, false},
		{"other runtime use", "user", `package p; import "runtime"; func F(){runtime.Gosched()}`, false},
		{"unrelated name", "user", `package p; var MemProfileRate int; func MemProfile(){}`, false},
		{"plain", "user", `package p; func F(){}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pkg, _ := buildCallerFrameSSAPackage(t, test.path, test.source)
			if got := MemProfileConsumer([]*ssa.Package{nil, {}, pkg}); (got != "") != test.want {
				t.Fatalf("consumer = %q, want enabled %v", got, test.want)
			}
		})
	}
}

type memProfileNilOperand struct{ ssa.Instruction }

func (memProfileNilOperand) Operands([]*ssa.Value) []*ssa.Value { return []*ssa.Value{nil} }

func TestMemProfileConsumerImportedGeneric(t *testing.T) {
	_, root := buildCallerFrameSSAProgram(t, "dep", `package dep
import "runtime"
func Read[T any](v T) { runtime.MemProfile(nil,false) }
`, "user", `package user
import "dep"
var Reader=dep.Read[int]
func F(){Reader(1)}
`)
	if got := MemProfileConsumer(root.Prog.AllPackages()); got != "dep" {
		t.Fatalf("imported generic consumer=%q", got)
	}
	// Operand lists may contain nil slots. Keep the consumer scanner as
	// defensive as the shared function-operand walk.
	plain, _ := buildCallerFrameSSAPackage(t, "plain", `package plain; import "runtime"; func F(){runtime.Gosched()}`)
	fn := plain.Func("F")
	fn.Blocks[0].Instrs = append(fn.Blocks[0].Instrs, memProfileNilOperand{})
	if got := MemProfileConsumer([]*ssa.Package{plain}); got != "" {
		t.Fatalf("nil operand invented consumer: %q", got)
	}
}

func TestMemoryProfileAllocationFrames(t *testing.T) {
	pkg, _ := buildCallerFrameSSAPackage(t, "profile", `package profile
var Sink any
func plain() int { return 1 }
func leaf() *int { return new(int) }
func wrapper() *int { return leaf() }
func closure(v int) func() *int { return func() *int { Sink=v; return leaf() } }
func indirect(f func() *int) *int { return f() }
type I interface { Get() *int }
func invoke(i I) *int { return i.Get() }
type T int
func (T) Get() *int { return leaf() }
func stores() { Sink = T(0).Get; Sink = leaf }
func stringsAdd(a,b string) string { return a+b }
func numericAdd(a,b int) int { return a+b }
func bytesString(b []byte) string { return string(b) }
func stringBytes(s string) []byte { return []byte(s) }
func runeString(r rune) string { return string(r) }
func stringSame(s string) string { return string(s) }
func makes(n int) { Sink=make([]byte,n); Sink=make(map[int]int); Sink=make(chan int,n) }
func appendBytes(b []byte) []byte { return append(b,1) }
func builtinLen(b []byte) int { return len(b) }
func goCall() { go plain() }
func deferCall() { defer plain() }
func selectCall(ch chan int) { select {case ch<-1: default:} }
func send(ch chan int) { ch<-1 }
func recv(ch chan int) int { return <-ch }
func rangeMap(m map[int]int) int { n:=0;for _,v:=range m{n+=v};return n }
func rangeString(s string) int { n:=0;for _,v:=range s{n+=int(v)};return n }
func update(m map[int]int) { m[1]=1 }
`)
	roots, _ := collectRuntimeCallerFunctions(pkg)
	roots[nil] = true
	funcs := memoryProfileFunctions(roots)
	frames := memoryProfileAllocationFrames(funcs)
	for _, name := range []string{"leaf", "wrapper", "closure", "indirect", "invoke", "stores", "stringsAdd", "bytesString", "stringBytes", "runeString", "makes", "appendBytes", "goCall", "deferCall", "selectCall", "send", "recv", "rangeMap", "rangeString", "update"} {
		if !frames[pkg.Func(name)] {
			t.Errorf("lost allocation frame %s", name)
		}
	}
	for _, name := range []string{"plain", "numericAdd", "stringSame", "builtinLen"} {
		if frames[pkg.Func(name)] {
			t.Errorf("nonallocating %s was instrumented", name)
		}
	}
	if !memoryProfileInstructionMayAllocate(&ssa.MultiConvert{}, nil) {
		t.Fatal("generic conversion must be conservative")
	}
	if memoryProfileInstructionMayAllocate(&ssa.Alloc{}, nil) {
		t.Fatal("stack allocation does not sample")
	}
}

func TestMemoryProfileImportedGenericAndMethodValues(t *testing.T) {
	dep, root := buildCallerFrameSSAProgram(t, "dep", `package dep
func New[T any]() *T { return new(T) }
type Box[T any] struct{}
func (Box[T]) New() *T { return new(T) }
`, "user", `package user
import "dep"
var Saved = dep.New[int]
var Method = dep.Box[int]{}.New
func Get() *int { return Saved() }
func Direct() *string { return dep.New[string]() }
`)
	ct := NewCallerTracking()
	ct.SetMemoryProfileAttribution(true)
	ct.Precompute([]*ssa.Package{dep, root})
	concrete := 0
	for fn, frame := range ct.memoryProfileFrames {
		if frame && fn.Origin() != nil && len(fn.Blocks) != 0 {
			concrete++
		}
	}
	if concrete < 3 || !ct.memoryProfileFrames[root.Func("Get")] || !ct.memoryProfileFrames[root.Func("Direct")] {
		t.Fatalf("lost instantiated/function-value frames: %d concrete", concrete)
	}
	ct.SetMemoryProfileAttribution(true)
	defer func() {
		if recover() == nil {
			t.Fatal("late mode change did not fail")
		}
	}()
	ct.SetMemoryProfileAttribution(false)
}

func TestMemoryProfileConvertMayAllocate(t *testing.T) {
	for _, test := range []struct {
		from, to types.Type
		want     bool
	}{
		{types.Typ[types.Int], types.Typ[types.String], true},
		{types.NewSlice(types.Typ[types.Uint8]), types.Typ[types.String], true},
		{types.NewSlice(types.Typ[types.Int32]), types.Typ[types.String], true},
		{types.Typ[types.String], types.NewSlice(types.Typ[types.Int32]), true},
		{types.Typ[types.String], types.NewSlice(types.Typ[types.Uint8]), true},
		{types.Typ[types.Float64], types.Typ[types.String], false},
		{types.NewSlice(types.Typ[types.Int]), types.Typ[types.String], false},
		{types.NewPointer(types.Typ[types.Int]), types.Typ[types.String], false},
		{types.Typ[types.String], types.Typ[types.Int], false},
		{types.Typ[types.String], types.NewSlice(types.Typ[types.Int]), false},
		{types.Typ[types.String], types.NewPointer(types.Typ[types.Int]), false},
	} {
		if got := memoryProfileConvertMayAllocate(test.from, test.to); got != test.want {
			t.Errorf("%s -> %s = %v", test.from, test.to, got)
		}
	}
}

func TestWasmMemProfileDoesNotInstrumentAtomicProvider(t *testing.T) {
	pkg, files := buildCallerFrameSSAPackage(t, "sync/atomic", `package atomic
type Uint64 struct { v uint64 }
func AddUint64(addr *uint64, delta uint64) uint64
func (x *Uint64) Add(delta uint64) uint64 { return AddUint64(&x.v, delta) }
`)
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "js", GOARCH: "wasm"})
	defer prog.Dispose()
	prog.EnableGCRoots(true)
	prog.EnableWasmMemoryProfiling(true)
	compiled, _, err := NewPackageExWithEmbedMetaOptions(prog, nil, nil, nil, pkg, files, nil, false, Options{ShadowStack: true})
	if err != nil {
		t.Fatal(err)
	}
	for fn := compiled.Module().FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if strings.Contains(fn.Name(), ".Add") && strings.Contains(fn.String(), "CallerLocation") {
			t.Fatalf("atomic provider recursively instruments goroutine initialization:\n%s", fn.String())
		}
	}
}

func TestWasmMemProfileAllocationLocations(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		pkg, files := buildCallerFrameSSAPackage(t, "profile", `package profile
func Leaf() (*int, []byte) {
  a := new(int)
  b := make([]byte, 17)
  return a,b
}
func Wrapper() (*int, []byte) { return Leaf() }
func Plain(x int) int { return x+1 }
`)
		prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "js", GOARCH: "wasm"})
		defer prog.Dispose()
		prog.EnableGCRoots(true)
		prog.EnableWasmMemoryProfiling(enabled)
		compiled, _, err := NewPackageExWithEmbedMetaOptions(prog, nil, nil, nil, pkg, files, nil, false, Options{ShadowStack: true})
		if err != nil {
			t.Fatal(err)
		}
		mod := compiled.Module()
		for _, name := range []string{"Leaf", "Wrapper"} {
			body := mod.NamedFunction("profile." + name).String()
			if strings.Contains(body, "PushCallerLocationFrameWasm") != enabled {
				t.Fatalf("%s enabled=%v:\n%s", name, enabled, body)
			}
		}
		if strings.Contains(mod.NamedFunction("profile.Plain").String(), "CallerLocation") {
			t.Fatal("plain helper gained frame")
		}
		leaf := mod.NamedFunction("profile.Leaf").String()
		lines := map[string]bool{}
		for _, line := range strings.Split(leaf, "\n") {
			if strings.Contains(line, "call void") && strings.Contains(line, "RecordPanicLocationWasm") {
				lines[line[strings.LastIndex(line, "i32 ")+4:strings.LastIndex(line, ")")]] = true
			}
		}
		if enabled && (!lines["3"] || !lines["4"]) {
			t.Fatalf("allocation sites not exact: %v\n%s", lines, leaf)
		}
		if !enabled && len(lines) != 0 {
			t.Fatal("nonconsumer gained allocation locations")
		}
	}
}
