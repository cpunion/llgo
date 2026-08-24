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

func TestMapHashingUsesStaticTypeMetadata(t *testing.T) {
	dir := filepath.Join("internal", "runtime")
	alg, err := os.ReadFile(filepath.Join(dir, "alg.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"func maptypehash(t *maptype, p unsafe.Pointer, h uintptr) uintptr",
		"return typehashImpl(t.Key, p, h)",
		"Keep the Hasher field in the runtime ABI",
	} {
		if !strings.Contains(string(alg), required) {
			t.Errorf("alg.go lacks static map hash marker %q", required)
		}
	}

	for _, name := range []string{"map_fast32.go", "map_fast64.go", "map_faststr.go"} {
		source, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		if !strings.Contains(text, "maptypehash(t,") {
			t.Errorf("%s does not use the static map hash path", name)
		}
		if strings.Contains(text, ".Hasher(") {
			t.Errorf("%s retains an opaque map Hasher function-value call", name)
		}
	}
}
