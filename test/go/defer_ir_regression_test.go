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

package gotest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const recoverThenDeferredPanicProbe = `package main

func end() {
	if recovered := recover(); recovered != nil {
		defer panic(recovered)
		println("will panic in defer")
	}
	println("end")
}

func main() {
	defer end()
	panic("panic in main")
}
`

const cgoDeferredFreeProbe = `package main

/*
#include <stdlib.h>
*/
import "C"

func main() {
	p := C.malloc(8)
	if p == nil {
		panic("C.malloc returned nil")
	}
	defer C.free(p)
}
`

func TestRecoverThenDeferredPanicIRTerminatesBlocks(t *testing.T) {
	ir := llgoIRFromProbe(t, "recover-then-deferred-panic", recoverThenDeferredPanicProbe)
	assertNoInstructionsAfterUnreachable(t, ir)
}

func TestCgoDeferredFreeUsesFrameOwnedWorkerCleanup(t *testing.T) {
	if strings.TrimSpace(runGoCmd(t, "", "env", "CGO_ENABLED")) != "1" {
		t.Skip("cgo is disabled")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang is unavailable")
	}

	ir := llgoIRFromProbe(t, "cgo-deferred-free", cgoDeferredFreeProbe)
	// LLVM may print an otherwise identical C symbol quoted or unquoted. Match
	// the call shape and semantic suffix instead of depending on that spelling.
	freeThunk := llvmFunctionBodyContaining(ir, "call [0 x i8] @")
	if freeThunk == "" || !strings.Contains(freeThunk, "._Cfunc_free") {
		t.Fatalf("missing deferred C.free worker thunk in IR:\n%s", ir)
	}
	freeThunkSymbol := llvmDefinedFunctionSymbol(freeThunk)
	if freeThunkSymbol == "" {
		t.Fatalf("cannot identify deferred C.free worker thunk:\n%s", freeThunk)
	}
	cleanup := llvmFunctionBodyContainingExcluding(
		ir,
		"ptrtoint (ptr "+freeThunkSymbol+" to ",
		freeThunkSymbol,
	)
	if cleanup == "" {
		t.Fatalf("missing coroutine cleanup that submits %s:\n%s", freeThunkSymbol, ir)
	}
	park := indexLineContaining(cleanup, "call void @__llgo_coro_worker_park_v1")
	resume := indexLineContaining(cleanup, "call i32 @__llgo_coro_worker_resume_v1")
	retain := indexLineContaining(cleanup, "call void (...) @llvm.fake.use")
	if park < 0 || resume < 0 || retain < 0 {
		t.Fatalf(
			"deferred C.free cleanup lacks frame-owned worker lifecycle (park=%d resume=%d retain=%d):\n%s",
			park, resume, retain, cleanup,
		)
	}
	if indexLineContaining(ir, "call void @", "FreeDeferNode") >= 0 {
		t.Fatalf("static coroutine cleanup unexpectedly allocated a legacy defer node:\n%s", ir)
	}
}

func llgoIRFromProbe(t *testing.T, name, src string) string {
	t.Helper()

	root := findLLGoRoot(t)
	dir, err := os.MkdirTemp(root, "."+name+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("remove temp probe dir %s: %v", dir, err)
		}
	})

	mainFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	runGoCmd(t, root, "run", "./chore/llgen", filepath.ToSlash(dir))
	data, err := os.ReadFile(filepath.Join(dir, "llgo_autogen.ll"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertNoInstructionsAfterUnreachable(t *testing.T, ir string) {
	t.Helper()

	lines := strings.Split(ir, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "unreachable" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" {
				continue
			}
			if trimmed == "}" || isLLVMBasicBlockLabel(trimmed) {
				break
			}
			t.Fatalf("instruction after unreachable at IR line %d: %s", j+1, trimmed)
		}
	}
}

func isLLVMBasicBlockLabel(line string) bool {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return false
	}
	return !strings.ContainsAny(line[:colon], " \t")
}

func indexLineContaining(s string, parts ...string) int {
	for i, line := range strings.Split(s, "\n") {
		ok := true
		for _, part := range parts {
			if !strings.Contains(line, part) {
				ok = false
				break
			}
		}
		if ok {
			return i + 1
		}
	}
	return -1
}

func llvmFunctionBodyContaining(ir, marker string) string {
	return llvmFunctionBodyContainingExcluding(ir, marker, "")
}

func llvmFunctionBodyContainingExcluding(ir, marker, excludedSymbol string) string {
	lines := strings.Split(ir, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "define ") {
			start = i
		}
		if start < 0 || strings.TrimSpace(line) != "}" {
			continue
		}
		body := strings.Join(lines[start:i+1], "\n")
		if strings.Contains(body, marker) &&
			(excludedSymbol == "" || llvmDefinedFunctionSymbol(body) != excludedSymbol) {
			return body
		}
		start = -1
	}
	return ""
}

func llvmDefinedFunctionSymbol(body string) string {
	header, _, _ := strings.Cut(body, "\n")
	at := strings.IndexByte(header, '@')
	if at < 0 {
		return ""
	}
	end := strings.IndexByte(header[at:], '(')
	if end < 0 {
		return ""
	}
	return header[at : at+end]
}
