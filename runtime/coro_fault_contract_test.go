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
	"os/exec"
	"testing"
)

// TestCoroImplicitFaultContract keeps the named production-source island in
// the ordinary host test graph. The runtime package cannot import its own
// private interface-layout prefix into an external test package, so the
// focused child build deliberately compiles only the production adapter and
// its contract test.
func TestCoroImplicitFaultContract(t *testing.T) {
	cmd := exec.Command(
		"go", "test",
		"-tags=coro_nil_fault_adapter_test",
		"coro_nil_fault.go", "coro_panic_trace_release_test_adapter.go",
		"coro_nil_fault_test.go",
		"-run", "^TestCoro.*Fault",
		"-count=1",
	)
	cmd.Dir = "internal/runtime"
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("implicit coroutine fault contract: %v\n%s", err, output)
	}
}
