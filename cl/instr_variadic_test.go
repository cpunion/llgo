package cl

import (
	"regexp"
	"strings"
	"testing"
)

func TestIndirectCVariadicArguments(t *testing.T) {
	const source = `package foo
//llgo:type C
type Variadic func(marker int32, __llgo_va_list ...any) int32
func Call(function Variadic, marker int32, integer int32, floating float64) int32 {
	return function(marker, integer, floating)
}
func Empty(function Variadic, marker int32) int32 { return function(marker) }
func Ordinary(function func(marker int32, values ...any) int32, marker int32, integer int32) int32 {
	return function(marker, integer)
}
`
	_, module := mustCompileLLPkgFromSrc(t, source)
	defer module.Dispose()
	text := module.String()
	// C ellipsis arguments must be concrete scalar LLVM values, rather than a
	// Go slice header. Ordinary Go variadic function values keep their slice ABI.
	if !regexp.MustCompile(`call i32 \(i32, \.\.\.\) %[^ (]+\(i32 %[^,]+, i32 %[^,]+, double %[^)]+\)`).MatchString(text) {
		t.Fatalf("missing expanded indirect C varargs:\n%s", text)
	}
	if !regexp.MustCompile(`call i32 \(i32, \.\.\.\) %[^ (]+\(i32 %[^)]+\)`).MatchString(text) {
		t.Fatalf("missing zero-tail indirect C call:\n%s", text)
	}
	if !strings.Contains(text, `runtime.Slice" %`) {
		t.Fatalf("ordinary Go variadic call lost its slice argument:\n%s", text)
	}
}
