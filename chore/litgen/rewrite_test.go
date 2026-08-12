package main

import (
	"strings"
	"testing"
)

func TestRewriteSource_InsertsMainAndClosure(t *testing.T) {
	const src = `// LITTEST
package main

func main() {
	fn := func() {}
	fn()
}
`
	const ir = `define void @"example.com/p.main"() {
_llgo_0:
  %0 = call ptr @"example.com/p.main$1"()
  ret void
}

define void @"example.com/p.main$1"() {
_llgo_0:
  ret void
}

`
	got, err := rewriteSource(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	mainCheck := `// CHECK-LABEL: define void @"{{.*}}/p.main"(){{.*}} {`
	mainDecl := "func main() {"
	if !strings.Contains(got, mainCheck) {
		t.Fatalf("main checks not inserted before func main:\n%s", got)
	}
	if strings.Index(got, mainCheck) > strings.Index(got, mainDecl) {
		t.Fatalf("main checks should appear before func main:\n%s", got)
	}
	closureCheck := "\t// CHECK-LABEL: define void @\"{{.*}}/p.main$1\"(){{.*}} {"
	closureStmt := "\tfn := func() {}"
	if !strings.Contains(got, closureCheck) {
		t.Fatalf("closure checks not inserted before func literal:\n%s", got)
	}
	if strings.Index(got, closureCheck) > strings.Index(got, closureStmt) {
		t.Fatalf("closure checks should appear before func literal:\n%s", got)
	}
}

func TestRewriteSource_AddsInitAndCheckEmptyAndSkipsHelpers(t *testing.T) {
	const src = `// LITTEST
package main

var x = 1

func main() {}
`
	const ir = `define void @"example.com/p.init"() {
_llgo_0:
  br i1 true, label %_llgo_1, label %_llgo_2

_llgo_1:
  ret void

_llgo_2:
  ret void
}

define i1 @"example.com/runtime/internal/runtime.strequal"(ptr %0, ptr %1) {
_llgo_0:
  ret i1 true
}

define void @"example.com/p.main"() {
_llgo_0:
  ret void
}
`
	got, err := rewriteSource(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	initCheck := `// CHECK-LABEL: define void @"{{.*}}/p.init"(){{.*}} {`
	if !strings.Contains(got, initCheck) {
		t.Fatalf("init checks not inserted before var decl:\n%s", got)
	}
	if strings.Index(got, initCheck) > strings.Index(got, "var x = 1") {
		t.Fatalf("init checks should appear before var decl:\n%s", got)
	}
	if !strings.Contains(got, "// CHECK-EMPTY:") {
		t.Fatalf("blank IR lines should use CHECK-EMPTY:\n%s", got)
	}
	if strings.Contains(got, "runtime.strequal") {
		t.Fatalf("runtime.strequal helper should be skipped:\n%s", got)
	}
}

func TestRewriteSource_PreservesIROrderWhenAnchorMovesBackward(t *testing.T) {
	const src = `// LITTEST
package main

var seed = 40

func add(x, y int) int {
	return x + y
}

func main() {}
`
	const ir = `define i64 @"example.com/p.add"(i64 %0, i64 %1) {
_llgo_0:
  %2 = add i64 %0, %1
  ret i64 %2
}

define void @"example.com/p.init"() {
_llgo_0:
  ret void
}

define void @"example.com/p.main"() {
_llgo_0:
  ret void
}
`
	got, err := rewriteSource(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	addCheck := `// CHECK-LABEL: define i64 @"{{.*}}/p.add"(i64 %0, i64 %1){{.*}} {`
	initCheck := `// CHECK-LABEL: define void @"{{.*}}/p.init"(){{.*}} {`
	if strings.Index(got, addCheck) < 0 || strings.Index(got, initCheck) < 0 {
		t.Fatalf("missing checks:\n%s", got)
	}
	if strings.Index(got, addCheck) > strings.Index(got, initCheck) {
		t.Fatalf("IR order should be preserved even if init anchor is earlier:\n%s", got)
	}
}

func TestRewriteSource_AddsReferencedNumericGlobalsAtTop(t *testing.T) {
	const src = `// LITTEST
package main

func main() {}
`
	const ir = `@0 = private unnamed_addr constant [4 x i8] c"Hi\0A\00", align 1
@1 = private unnamed_addr constant [3 x i8] c"%s\00", align 1
@"example.com/p.named" = global i64 1

define void @"example.com/p.main"() {
_llgo_0:
  call void @puts(ptr @0)
  call void @printf(ptr @1)
  ret void
}
`
	got, err := rewriteSource(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `// CHECK: {{^}}@{{[0-9]+}} = private unnamed_addr constant [4 x i8] c"Hi\0A\00", align 1{{$}}`) {
		t.Fatalf("missing numeric global @0:\n%s", got)
	}
	if !strings.Contains(got, `// CHECK: {{^}}@{{[0-9]+}} = private unnamed_addr constant [3 x i8] c"%s\00", align 1{{$}}`) {
		t.Fatalf("missing numeric global @1:\n%s", got)
	}
	if strings.Contains(got, `// CHECK: {{^}}@"{{.*}}/p.named" = global i64 1{{$}}`) {
		t.Fatalf("named globals should not be emitted by default:\n%s", got)
	}
	if strings.Index(got, `// CHECK: {{^}}@{{[0-9]+}} = private unnamed_addr constant [4 x i8] c"Hi\0A\00", align 1{{$}}`) > strings.Index(got, "func main()") {
		t.Fatalf("global checks should be placed before first declaration:\n%s", got)
	}
}

func TestRewriteSource_PreservesDeclarationDirectiveAdjacency(t *testing.T) {
	const src = `// LITTEST
package main

import _ "unsafe"

//go:linkname cSqrt C.sqrt
func cSqrt(float64) float64

func callSqrt(x float64) float64 {
	println("sqrt")
	return cSqrt(x)
}
`
	const ir = `@0 = private unnamed_addr constant [4 x i8] c"sqrt"

declare double @sqrt(double)

define double @"example.com/p.callSqrt"(double %0) {
_llgo_0:
  call void @"example.com/runtime/internal/runtime.PrintString"(ptr @0)
  %1 = call double @sqrt(double %0)
  ret double %1
}
`
	got, err := rewriteSource(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	want := `// LITTEST
package main

import _ "unsafe"

// CHECK: {{^}}@{{[0-9]+}} = private unnamed_addr constant [4 x i8] c"sqrt"{{$}}

//go:linkname cSqrt C.sqrt
func cSqrt(float64) float64

// CHECK-LABEL: define double @"{{.*}}/p.callSqrt"(double %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   call void @"{{.*}}/runtime/internal/runtime.PrintString"(ptr @{{[0-9]+}})
// CHECK-NEXT:   %1 = call double @sqrt(double %0)
// CHECK-NEXT:   ret double %1
// CHECK-NEXT: }

func callSqrt(x float64) float64 {
	println("sqrt")
	return cSqrt(x)
}
`
	if got != want {
		t.Fatalf("rewriteSource mismatch:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestRewriteSource_PreservesDirectiveBeforeInlineClosure(t *testing.T) {
	const funcIR = `define ptr @"example.com/p.makeFn"() {
_llgo_0:
  ret ptr @"example.com/p.makeFn$1"
}

define void @"example.com/p.makeFn$1"() {
_llgo_0:
  ret void
}
`
	const initIR = `@0 = private unnamed_addr constant [3 x i8] c"fn"

define void @"example.com/p.init"() {
_llgo_0:
  store ptr @"example.com/p.init$1", ptr null
  ret void
}

define void @"example.com/p.init$1"() {
_llgo_0:
  ret void
}
`
	tests := []struct {
		name      string
		src       string
		ir        string
		adjacency string
	}{
		{
			name: "function declaration",
			src: `// LITTEST
package main

//go:noinline
func makeFn() func() { return func() {} }
`,
			ir:        funcIR,
			adjacency: "//go:noinline\nfunc makeFn",
		},
		{
			name: "variable declaration",
			src: `// LITTEST
package main

//llgo:tls
var fn = func() {}
`,
			ir:        initIR,
			adjacency: "//llgo:tls\nvar fn",
		},
		{
			name: "variable specification",
			src: `// LITTEST
package main

var (
	//llgo:tls
	fn = func() {}
)
`,
			ir:        initIR,
			adjacency: "\t//llgo:tls\n\tfn =",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := rewriteSource(test.src, "in.go", "example.com/p", "example.com", test.ir)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, test.adjacency) {
				t.Fatalf("declaration directive separated from declaration:\n%s", got)
			}
			if !strings.Contains(got, "$1") {
				t.Fatalf("closure checks not inserted:\n%s", got)
			}
		})
	}
}

func TestRewriteSource_SharesInitClosureCountsAcrossDecls(t *testing.T) {
	const src = `// LITTEST
package main

var a = func() int { return 1 }()
var b = func() int { return 2 }()
`
	const ir = `define void @"example.com/p.init"() {
_llgo_0:
  %0 = call i64 @"example.com/p.init$1"()
  %1 = call i64 @"example.com/p.init$2"()
  ret void
}

define i64 @"example.com/p.init$1"() {
_llgo_0:
  ret i64 1
}

define i64 @"example.com/p.init$2"() {
_llgo_0:
  ret i64 2
}
`
	got, err := rewriteSource(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	firstCheck := `// CHECK-LABEL: define i64 @"{{.*}}/p.init$1"(){{.*}} {`
	secondCheck := `// CHECK-LABEL: define i64 @"{{.*}}/p.init$2"(){{.*}} {`
	firstVar := "var a = func() int { return 1 }()"
	secondVar := "var b = func() int { return 2 }()"
	if strings.Index(got, firstCheck) > strings.Index(got, firstVar) {
		t.Fatalf("first init closure should be anchored before first var decl:\n%s", got)
	}
	if strings.Index(got, secondCheck) > strings.Index(got, secondVar) {
		t.Fatalf("second init closure should be anchored before second var decl:\n%s", got)
	}
}

func TestGeneralizeDefineLine_WildcardsAttrsBeforeBrace(t *testing.T) {
	line := `define void @"example.com/p.main"() local_unnamed_addr #0 {`
	got := generalizeDefineLine(line, "example.com")
	want := `define void @"{{.*}}/p.main"(){{.*}} {`
	if got != want {
		t.Fatalf("generalizeDefineLine = %q, want %q", got, want)
	}
}

func TestGeneralizeClosureEnvAttrs(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{
			`define void @"example.com/nest.swiftself"(ptr swiftself %env) {`,
			`define void @"example.com/nest.swiftself"(ptr {{(nest|swiftself)}} %env) {`,
		},
		{
			`  call void %fn(ptr nest %env, ptr %arg)`,
			`  call void %fn(ptr {{(nest|swiftself)}} %env, ptr %arg)`,
		},
		{
			`@0 = private constant [14 x i8] c"nest swiftself"`,
			`@0 = private constant [14 x i8] c"nest swiftself"`,
		},
		{
			`@nest = global ptr @swiftself`,
			`@nest = global ptr @swiftself`,
		},
	}
	for _, test := range tests {
		if got := generalizeClosureEnvAttrs(test.line); got != test.want {
			t.Errorf("generalizeClosureEnvAttrs(%q) = %q, want %q", test.line, got, test.want)
		}
	}
}

func TestGeneralizeModulePath_ReplacesOnlyQuotedSegments(t *testing.T) {
	line := `  %0 = getelementptr inbounds %"go/example.Type", ptr @"go/example.fn"`
	got := generalizeModulePath(line, "go")
	want := `  %0 = getelementptr inbounds %"{{.*}}/example.Type", ptr @"{{.*}}/example.fn"`
	if got != want {
		t.Fatalf("generalizeModulePath = %q, want %q", got, want)
	}
}

func TestGeneralizeModulePath_IgnoresEscapedQuotes(t *testing.T) {
	line := "  !0 = !{!\"prefix \\\"quoted\\\" suffix\", !\"go/example.fn\"}"
	got := generalizeModulePath(line, "go")
	want := "  !0 = !{!\"prefix \\\"quoted\\\" suffix\", !\"{{.*}}/example.fn\"}"
	if got != want {
		t.Fatalf("generalizeModulePath = %q, want %q", got, want)
	}
}

func TestGeneralizeSymbolPaths_WildcardsTestCasePrefix(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{
			`define void @"github.com/goplus/llgo/cl/_testgo/deferfn.A"() {`,
			`define void @"{{.*}}.A"() {`,
		},
		{
			`call void @"github.com/goplus/llgo/cl/_testgo/deferfn/foo.B"()`,
			`call void @"{{.*}}/foo.B"()`,
		},
		{
			`call void @"github.com/goplus/llgo/runtime.Start"()`,
			`call void @"{{.*}}/runtime.Start"()`,
		},
	}
	for _, test := range tests {
		if got := generalizeSymbolPaths(test.line, "github.com/goplus/llgo"); got != test.want {
			t.Errorf("generalizeSymbolPaths(%q) = %q, want %q", test.line, got, test.want)
		}
	}
}

func TestGeneralizePlatformIR(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{`%0 = call i32 @__sigsetjmp(ptr %buf, i32 0)`, `%0 = call i32 @{{(__)?}}sigsetjmp(ptr %buf, i32 0)`},
		{`call void @siglongjmp(ptr %buf, i32 1)`, `call void @{{(__)?}}siglongjmp(ptr %buf, i32 1)`},
		{`%0 = call i32 @_setjmp(ptr %buf)`, `%0 = call i32 @{{_*}}setjmp(ptr %buf)`},
		{`call void @longjmp(ptr %buf, i32 1)`, `call void @{{_*}}longjmp(ptr %buf, i32 1)`},
		{`%0 = alloca i8, i64 196, align 1`, `%0 = alloca i8, i64 {{(196|200)}}, align 1`},
		{
			`store %"github.com/goplus/llgo/runtime/internal/clite/pthread/sync.Mutex" { [64 x i8] zeroinitializer }, ptr %0`,
			`store %"github.com/goplus/llgo/runtime/internal/clite/pthread/sync.Mutex" { [{{(40|48|64)}} x i8] zeroinitializer }, ptr %0`,
		},
		{
			`store %"github.com/goplus/llgo/runtime/internal/clite/pthread/sync.MutexAttr" { [16 x i8] zeroinitializer }, ptr %0`,
			`store %"github.com/goplus/llgo/runtime/internal/clite/pthread/sync.MutexAttr" { [{{(4|8|16)}} x i8] zeroinitializer }, ptr %0`,
		},
		{
			`store %"github.com/goplus/llgo/runtime/internal/clite/pthread/sync.RWLockAttr" { [24 x i8] zeroinitializer }, ptr %0`,
			`store %"github.com/goplus/llgo/runtime/internal/clite/pthread/sync.RWLockAttr" { [{{(8|16|24)}} x i8] zeroinitializer }, ptr %0`,
		},
		{
			`store %"github.com/goplus/llgo/runtime/internal/clite/pthread/sync.CondAttr" { [8 x i8] zeroinitializer }, ptr %0`,
			`store %"github.com/goplus/llgo/runtime/internal/clite/pthread/sync.CondAttr" { [{{(4|8|16)}} x i8] zeroinitializer }, ptr %0`,
		},
	}
	for _, test := range tests {
		if got := generalizePlatformIR(test.line); got != test.want {
			t.Errorf("generalizePlatformIR(%q) = %q, want %q", test.line, got, test.want)
		}
	}
}

func TestGeneralizeIRLine_WildcardsUnstableIDs(t *testing.T) {
	line := `  call void @0(ptr @19, ptr @"_llgo_closure$QIHBTaw1IFobr8yvWpq-2AJFm3xBNhdW_aNBicqUBGk"), !dbg !42`
	got := generalizeIRLine(line, "")
	want := `  call void @{{[0-9]+}}(ptr @{{[0-9]+}}, ptr @"_llgo_closure${{[-A-Za-z0-9_]+}}")`
	if got != want {
		t.Fatalf("generalizeIRLine() = %q, want %q", got, want)
	}
}

func TestGeneralizeIRLine_EscapesFileCheckSyntaxAndCgoHash(t *testing.T) {
	line := `  %0 = load ptr, ptr @0[[, ptr @main._cgo_52352d07b8a3_Cfunc_free`
	got := generalizeIRLine(line, "")
	want := `  %0 = load ptr, ptr @{{[0-9]+}}{{\[\[}}, ptr @main._cgo_{{[0-9a-f]+}}_Cfunc_free`
	if got != want {
		t.Fatalf("generalizeIRLine() = %q, want %q", got, want)
	}
}

func TestIndexFunctionChecks(t *testing.T) {
	funcs := parseIR(`define void @main.main() {
entry:
  ret void
}
`).funcs
	checks := indexFunctionChecks(funcs, "example.com")
	got, found, err := findFunctionForCheckGroup("// CHECK-LABEL: define void @main.main(){{.*}} {\n", funcs, checks)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got.symbol != "main.main" {
		t.Fatalf("findFunctionForCheckGroup() = (%q, %v), want main.main", got.symbol, found)
	}
}

func TestUpdateSourceChecks_UpdatesOnlyFailingGroupInPlace(t *testing.T) {
	const src = `// LITTEST
package p

// CHECK-LABEL: define void @"example.com/p.good"(){{.*}} {
// CHECK-NEXT: entry:
// CHECK-NEXT:   ret void
// CHECK-NEXT: }
func good() {}

// CHECK-LABEL: define i64 @"example.com/p.changed"(ptr %0){{.*}} {
// CHECK-NEXT: entry:
// CHECK-NEXT:   %1 = load i64, ptr %0
// CHECK-NEXT:   ret i64 %1
func changed(*int) int { return 0 }
`
	const ir = `define void @"example.com/p.good"() {
entry:
  ret void
}

define i64 @"example.com/p.changed"(ptr %0) {
entry:
  %nilcheck = icmp eq ptr %0, null
  br i1 %nilcheck, label %panic, label %cont

panic:
  unreachable

cont:
  %1 = load i64, ptr %0
  ret i64 %1
}
`
	got, changed, err := updateSourceChecks(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("updateSourceChecks reported no change")
	}
	good := src[strings.Index(src, `// CHECK-LABEL: define void`):strings.Index(src, `func good()`)]
	if !strings.Contains(got, good) {
		t.Fatalf("passing CHECK group changed:\n%s", got)
	}
	if strings.Index(got, `// CHECK-LABEL: define i64`) > strings.Index(got, `func changed(`) {
		t.Fatalf("updated CHECK group moved after its declaration:\n%s", got)
	}
	if !strings.Contains(got, `%nilcheck = icmp eq ptr %0, null`) {
		t.Fatalf("updated CHECK group missing changed IR:\n%s", got)
	}
}

func TestUpdateSourceChecks_UpdatesBodyGroupInsideFunction(t *testing.T) {
	const src = `// LITTEST
package p

// CHECK-LABEL: define i64 @"example.com/p.changed"(ptr %0){{.*}} {
func changed(*int) int {
	// CHECK-NEXT: entry:
	// CHECK-NEXT:   %1 = load i64, ptr %0
	// CHECK-NEXT:   ret i64 %1
	// CHECK-NEXT: }
	return 0
}
`
	const ir = `define i64 @"example.com/p.changed"(ptr %0) {
entry:
  %aggregate = insertvalue { ptr, ptr } undef, ptr %0, 0
  %nilcheck = icmp eq ptr %0, null
  br i1 %nilcheck, label %panic, label %cont

panic:
  unreachable

cont:
  %1 = load i64, ptr %0
  ret i64 %1
}
`
	got, changed, err := updateSourceChecks(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(got, "\t// CHECK-NEXT:   %nilcheck = icmp eq ptr %0, null") {
		t.Fatalf("body CHECK group was not updated in place:\n%s", got)
	}
	if strings.Count(got, "CHECK-LABEL") != 1 {
		t.Fatalf("function label was duplicated:\n%s", got)
	}
}

func TestUpdateSourceChecks_PreservesDefinitionAndEOF(t *testing.T) {
	const src = `// LITTEST
package p

// CHECK: define i64 @"{{.*}}.changed"(ptr %0){{.*}} {
// CHECK-NEXT: entry:
// CHECK-NEXT:   %1 = load i64, ptr %0
// CHECK-NEXT:   ret i64 %1
// CHECK-NEXT: }
func changed(*int) int { return 0 }
`
	const ir = `define i64 @"example.com/p.changed"(ptr %0) {
entry:
  %nilcheck = icmp eq ptr %0, null
  br i1 %nilcheck, label %panic, label %cont

panic:
  unreachable

cont:
  %1 = load i64, ptr %0
  ret i64 %1
}
`
	got, changed, err := updateSourceChecks(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("updateSourceChecks reported no change")
	}
	if !strings.Contains(got, `// CHECK: define i64 @"{{.*}}.changed"`) || strings.Contains(got, "CHECK-LABEL") {
		t.Fatalf("definition directive changed:\n%s", got)
	}
	if strings.HasSuffix(got, "\n\n") {
		t.Fatalf("update added a blank line at EOF:\n%q", got)
	}
}

func TestUpdateSourceChecks_IgnoresOtherCheckPrefixes(t *testing.T) {
	const src = `// LITTEST
package main

// SYMBOL-DAG: main
func main() {}
`
	const ir = `define void @"example.com/p.main"() {
entry:
  ret void
}
`
	got, changed, err := updateSourceChecks(src, "in.go", "example.com/p", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	if changed || got != src {
		t.Fatalf("non-CHECK directives should remain unchanged:\n%s", got)
	}
}

func TestUpdateSourceChecks_RegeneratesPassingContinuousSnapshot(t *testing.T) {
	const src = `// LITTEST
package p

// CHECK-LABEL: define void @main.main(){{.*}} {
// CHECK-NEXT: entry:
// CHECK-NEXT:   ret {{.*}}
// CHECK-NEXT: }
func main() {}
`
	const ir = `define void @main.main() {
entry:
  ret void
}
`
	got, changed, err := updateSourceChecks(src, "in.go", "main", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(got, "// CHECK-NEXT:   ret void") {
		t.Fatalf("passing continuous snapshot was not regenerated:\n%s", got)
	}
}

func TestUpdateSourceChecks_SplitsAdjacentAnchorsFromContinuousSnapshot(t *testing.T) {
	const src = `// LITTEST
package p

// CHECK: @0 = private constant i8 1
// CHECK: define i64 @main.changed(ptr %0){{.*}} {
// CHECK-NEXT: entry:
// CHECK-NEXT:   %1 = load i64, ptr %0
// CHECK-NEXT:   ret i64 %1
// CHECK-NEXT: }
func changed(*int) int { return 0 }
`
	const ir = `@0 = private constant i8 1

define i64 @main.changed(ptr %0) {
entry:
  %nilcheck = icmp eq ptr %0, null
  br i1 %nilcheck, label %panic, label %cont

panic:
  unreachable

cont:
  %1 = load i64, ptr %0
  ret i64 %1
}
`
	got, changed, err := updateSourceChecks(src, "in.go", "main", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(got, "// CHECK-NEXT:   %nilcheck = icmp eq ptr %0, null") {
		t.Fatalf("adjacent function snapshot was not regenerated:\n%s", got)
	}
	if strings.Count(got, "// CHECK: @0 = private constant i8 1") != 1 {
		t.Fatalf("adjacent manual anchor changed:\n%s", got)
	}
}

func TestUpdateSourceChecks_RecoversChangedFunctionSignature(t *testing.T) {
	const src = `// LITTEST
package p

// CHECK-LABEL: define i64 @main.changed(i64 %0){{.*}} {
// CHECK-NEXT: entry:
// CHECK-NEXT:   ret i64 %0
// CHECK-NEXT: }
func changed(int) int { return 0 }
`
	const ir = `define void @main.changed(ptr %0) {
entry:
  ret void
}
`
	got, changed, err := updateSourceChecks(src, "in.go", "main", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(got, "// CHECK-LABEL: define void @main.changed(ptr %0){{.*}} {") {
		t.Fatalf("changed function signature was not recovered:\n%s", got)
	}
}

func TestUpdateSourceChecks_RecoversChangedLocalSnapshotBounds(t *testing.T) {
	const src = `// LITTEST
package p

// CHECK-LABEL: define void @main.main(){{.*}} {
func main() {
	// CHECK:   call void @main.work()
	// CHECK-NEXT:   ret void
	work()
}
`
	const ir = `define void @main.main() {
entry:
  call void @main.work()
  call void @main.added()
  ret void
}
`
	got, changed, err := updateSourceChecks(src, "in.go", "main", "example.com", ir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(got, "// CHECK-NEXT:   call void @main.added()") {
		t.Fatalf("changed local snapshot was not recovered:\n%s", got)
	}
}

func TestUpdateSourceChecks_RejectsAmbiguousSnapshotBounds(t *testing.T) {
	const src = `// LITTEST
package p

func main() {
	// CHECK:   load i64, ptr %0
	// CHECK-NEXT:   ret void
}
`
	const ir = `define void @main.main() {
entry:
  load i64, ptr %0
  load i64, ptr %0
  call void @main.added()
  ret void
}
`
	_, _, err := updateSourceChecks(src, "in.go", "main", "example.com", ir)
	if err == nil || !strings.Contains(err.Error(), "start anchor matches 2 IR lines") {
		t.Fatalf("ambiguous snapshot error = %v", err)
	}
}

func TestUpdateSourceChecks_RejectsFailingManualCheck(t *testing.T) {
	const src = `// LITTEST
package p

// CHECK-LABEL: define void @main.main()
// CHECK: call void @main.missing()
func main() {}
`
	const ir = `define void @main.main() {
entry:
  ret void
}
`
	_, _, err := updateSourceChecks(src, "in.go", "main", "example.com", ir)
	if err == nil || !strings.Contains(err.Error(), "cannot be safely updated") {
		t.Fatalf("manual CHECK error = %v", err)
	}
}

func TestUpdateSourceChecks_RequiresForceToInitialize(t *testing.T) {
	const src = "// LITTEST\npackage p\n"
	_, _, err := updateSourceChecks(src, "in.go", "main", "example.com", "")
	if err == nil || !strings.Contains(err.Error(), "use -force") {
		t.Fatalf("missing CHECK error = %v", err)
	}
}
