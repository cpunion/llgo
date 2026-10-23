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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeDeferredReplacementDemandPublication(t *testing.T) {
	if (runtime.GOOS != "darwin" && runtime.GOOS != "linux") ||
		(runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64") {
		t.Skip("requires native coroutine replacement owners")
	}
	compiler := acceptanceLLGoBinary(t)
	root := findLLGoRoot(t)
	image := testExecutablePath(t.TempDir(), "deferred-demand")
	build := exec.Command(compiler, "build", "-tags=llgo_coro_deferred_demand_probe",
		"-o", image, "./internal/runtime/testdata/deferred_demand")
	build.Dir = filepath.Join(root, "runtime")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build forced replacement probe: %v\n%s", err, out)
	}
	for i := 0; i < 5; i++ {
		out, err := exec.Command(image).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "deferred probe demand true") ||
			!strings.Contains(string(out), "ok deferred demand publication") {
			t.Fatalf("forced replacement %d: %v\n%s", i, err, out)
		}
	}
}
