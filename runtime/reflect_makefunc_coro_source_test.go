//go:build !llgo

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

package runtime

import (
	"os"
	"strings"
	"testing"
)

func TestReflectMakeFuncRawCallbackRemainsBounded(t *testing.T) {
	const sourcePath = "internal/lib/reflect/makefunc.go"
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"tin         []*abi.Type",
		"tin:         ins,",
		"values := copyMakeFuncArgs(fd, args)",
		"ffi.CallRaw(fd.invokeCIF, fd.invokeEntry, ret, &argv[0])",
		"var makeFuncInvokeValue any",
		"makeFuncInvokeValue = makeFuncInvoke",
		"descriptor := ffi.NewRuntimeCoroDescriptor(",
		"func ffiToOwnedValue(ptr unsafe.Pointer, typ, storageType *abi.Type)",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("%s lacks bounded MakeFunc bridge marker %q", sourcePath, required)
		}
	}

	start := strings.Index(text, "func bindCoro(")
	end := strings.Index(text, "\nfunc copyMakeFuncArgs(")
	if start < 0 || end <= start {
		t.Fatalf("%s has no isolated bindCoro body", sourcePath)
	}
	callback := text[start:end]
	for _, forbidden := range []string{
		"closureOf(",
		"fd.fn(",
		"time.Sleep(",
		"ffi.CallLLGo(",
	} {
		if strings.Contains(callback, forbidden) {
			t.Errorf("raw bindCoro callback contains unbounded operation %q", forbidden)
		}
	}

	start = strings.Index(text, "func ffiToOwnedValue(")
	end = strings.Index(text[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatalf("%s has no ffiToOwnedValue body", sourcePath)
	}
	ownedValue := text[start : start+end]
	if strings.Contains(ownedValue, "closureOf(") {
		t.Fatal("raw argument copying still performs reflect closure-type cache lookup")
	}
}
