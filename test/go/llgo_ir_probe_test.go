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
	"path/filepath"
	"testing"
)

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
