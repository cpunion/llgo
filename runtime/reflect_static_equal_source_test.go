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
	"path/filepath"
	"strings"
	"testing"
)

func TestReflectCompositeEqualClosuresUseStaticTypeEqual(t *testing.T) {
	source := readReflectStaticEqualSource(t, "type.go")
	for _, required := range []string{
		"array.Equal = nil",
		"if etyp.Equal != nil {",
		"array.Equal = func(p, q unsafe.Pointer) bool {",
		"if !typeequal(etyp, pi, qi) {",
		"comparable = comparable && (ft.Equal != nil)",
		"typ.Equal = nil",
		"typ.Equal = func(p, q unsafe.Pointer) bool {",
		"if ft.Name_ == \"_\" {",
		"if !typeequal(ft.Typ, pi, qi) {",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("reflect/type.go lacks static equality marker %q", required)
		}
	}
	for _, forbidden := range []string{"eequal(pi, qi)", "ft.Typ.Equal(pi, qi)"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("reflect/type.go retains dynamic equality call %q", forbidden)
		}
	}
}

func TestReflectIsZeroUsesStaticTypeEqual(t *testing.T) {
	source := readReflectStaticEqualSource(t, "value.go")
	if got := strings.Count(source, "return typeequal(v.typ(), noescape(v.ptr), unsafe.Pointer(&zeroVal[0]))"); got != 2 {
		t.Errorf("Value.IsZero has %d static typeequal returns, want 2", got)
	}
	for _, required := range []string{
		"if v.typ().Equal != nil && v.typ().Size() <= maxZero {",
		"if tt.Fields[i].Name_ == \"_\" {",
		"//go:linkname typeequal github.com/goplus/llgo/runtime/internal/runtime.typeequal",
		"func typeequal(t *abi.Type, p, q unsafe.Pointer) bool",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("reflect/value.go lacks static equality marker %q", required)
		}
	}
	if strings.Contains(source, "return v.typ().Equal(") {
		t.Error("Value.IsZero retains a dynamic Equal callback")
	}
}

func readReflectStaticEqualSource(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("internal", "lib", "reflect", name)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(source)
}
