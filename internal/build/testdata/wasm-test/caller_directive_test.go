package wasmtest

import (
	"runtime"
	"strings"
	"testing"
)

func callerDirectiveLocation() (string, int) {
	_, file, line, _ := runtime.Caller(1)
	return file, line
}

func TestCallerDistinguishesDirectiveFilesAtSameLine(t *testing.T) {
//line first.go:101
	firstFile, firstLine := callerDirectiveLocation()
//line second.go:101
	secondFile, secondLine := callerDirectiveLocation()
	if !strings.HasSuffix(firstFile, "first.go") || firstLine != 101 {
		t.Fatalf("first caller = %s:%d, want first.go:101", firstFile, firstLine)
	}
	if !strings.HasSuffix(secondFile, "second.go") || secondLine != 101 {
		t.Fatalf("second caller = %s:%d, want second.go:101", secondFile, secondLine)
	}
}
