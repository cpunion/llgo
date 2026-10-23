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
	"strings"
	"testing"
)

// A return keeps the aggregate captured before an intervening write, including
// when the C ABI transports that aggregate in one integer register.
func TestSmallAggregateReturnSnapshot(t *testing.T) {
	const source = `package main

type snapshotPair struct{ first, second uint32 }

var snapshotGlobal snapshotPair

//go:noinline
func overwriteSnapshot(p *snapshotPair) {
	p.first, p.second = 0xdeadbeef, 0x12345678
}

//go:noinline
func overwriteGlobalSnapshot() {
	snapshotGlobal = snapshotPair{0xdeadbeef, 0x12345678}
}

//go:noinline
func takeGlobalSnapshot() snapshotPair {
	value := snapshotGlobal
	overwriteGlobalSnapshot()
	return value
}

//go:noinline
func takePointerSnapshot(p *snapshotPair) snapshotPair {
	value := *p
	overwriteSnapshot(p)
	return value
}

//go:noinline
func takeBranchSnapshot(p *snapshotPair, mutate bool) snapshotPair {
	value := *p
	if mutate {
		overwriteSnapshot(p)
	}
	return value
}

func main() {
	var checksum uint64
	for i := uint32(0); i < 4096; i++ {
		want := snapshotPair{i + 1, i*7 + 11}
		snapshotGlobal = want
		global := takeGlobalSnapshot()
		local := want
		pointer := takePointerSnapshot(&local)
		branchInput := want
		branch := takeBranchSnapshot(&branchInput, true)
		if global != want || pointer != want || branch != want {
			println("bad snapshot", i, global.first, global.second, pointer.first, pointer.second, branch.first, branch.second)
			return
		}
		if snapshotGlobal.first != 0xdeadbeef || local.first != 0xdeadbeef || branchInput.first != 0xdeadbeef {
			println("bad mutation", i)
			return
		}
		checksum += uint64(global.first)<<32 | uint64(global.second)
	}
	println("ok snapshot", checksum)
}
`
	_, dir := prepareCallerAcceptanceProbe(t, source)
	images := map[string]string{
		"go":   testExecutablePath(dir, "go-snapshot"),
		"llgo": testExecutablePath(dir, "llgo-snapshot"),
	}
	var expected string
	for _, tool := range []string{"go", "llgo"} {
		compiler := tool
		args := []string{"build", "-p=1", "-o", images[tool], "main.go"}
		if tool == "llgo" {
			compiler = acceptanceLLGoBinary(t)
			args = append([]string{"build", "-Os"}, args[1:]...)
		}
		cmd := exec.Command(compiler, args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", tool, err, output)
		}
		cmd = exec.Command(images[tool])
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run %s: %v\n%s", tool, err, output)
		}
		actual := strings.TrimSpace(string(output))
		if tool == "go" {
			expected = actual
			if !strings.HasPrefix(expected, "ok snapshot ") {
				t.Fatal(expected)
			}
		} else if actual != expected {
			t.Fatalf("LLGo=%q Go=%q", actual, expected)
		}
	}
}
