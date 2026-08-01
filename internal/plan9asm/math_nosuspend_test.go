//go:build !llgo
// +build !llgo

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

package plan9asm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/goplus/llgo/internal/packages"
)

func TestStdlibMathArchExpAmd64NoSuspendProof(t *testing.T) {
	goroot := runtime.GOROOT()
	if goroot == "" {
		t.Skip("GOROOT not available")
	}
	sfile := filepath.Join(goroot, "src", "math", "exp_amd64.s")
	source, err := os.ReadFile(sfile)
	if os.IsNotExist(err) {
		t.Skip("GOROOT has no amd64 archExp assembly")
	}
	if err != nil {
		t.Fatal(err)
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesSizes | packages.NeedTypesInfo |
			packages.NeedImports,
		Env: append(os.Environ(), "GOOS=linux", "GOARCH=amd64"),
	}
	pkgs, err := packages.LoadEx(nil, nil, cfg, "math")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Types == nil {
		t.Fatalf("load math: got %d packages", len(pkgs))
	}

	translation, err := TranslateSourceModuleForPkg(pkgs[0], sfile, source, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer translation.Module.Dispose()
	if _, err := ProveNoSuspendLeaf(translation, "math.archExp"); err != nil {
		t.Fatalf("prove translated math.archExp no-suspend leaf: %v", err)
	}
}
