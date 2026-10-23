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
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// Source-local limits alone cannot bound a synchronous native call chain.
// Each stage has a rare 64 KiB local whose address is borrowed by consume.
// x/tools marks that allocation Heap, while LLGo's frozen lifetime proof can
// put it back on the stack. The small branch still reserves a backend frame.
func TestOutcomeCallChainLocalStorage(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package main
import ("os"; "runtime"; "strconv")
var large bool
var checksum uint64 = 7
//go:noinline
func consume(v *[65536]byte) uint64 {
  var sum uint64
`)
	for index := 0; index < 65536; index += 4096 {
		fmt.Fprintf(&source, "  sum += uint64(v[%d])\n", index)
	}
	source.WriteString("  return sum\n}\n")
	const stages = 144
	for index := 0; index < stages; index++ {
		fmt.Fprintf(&source, `
//go:noinline
func stage%d(depth int) uint64 {
  if large {
    var local [65536]byte
    local[depth %% len(local)] = byte(depth+1)
    checksum ^= consume(&local)
  }
  before := checksum
  if depth == 0 { return before + 1 }
`, index)
		if index+1 < stages {
			fmt.Fprintf(&source, "  child := stage%d(depth-1)\n", index+1)
		} else {
			source.WriteString("  child := before + 1\n")
		}
		source.WriteString("  checksum = before ^ child\n  return child + checksum + uint64(depth & 7)\n}\n")
	}
	source.WriteString(`
func main() {
  large = os.Args[1] == "large"
  depth, err := strconv.Atoi(os.Args[2]); if err != nil { panic(err) }
  run := func() { println("stack budget", stage0(depth), checksum) }
  if os.Args[3] == "peer" {
    runtime.LockOSThread()
    done := make(chan bool)
    go func() { run(); done <- true }()
    <-done
    runtime.UnlockOSThread()
  } else { run() }
}
`)
	_, dir := prepareCallerAcceptanceProbe(t, source.String())
	llgo := acceptanceLLGoBinary(t)
	images := map[string]string{
		"go":   testExecutablePath(dir, "go-probe"),
		"llgo": testExecutablePath(dir, "llgo-probe"),
	}
	for _, tool := range []string{"go", "llgo"} {
		compiler := tool
		args := []string{"build", "-p=1", "-o", images[tool], "main.go"}
		if tool == "llgo" {
			compiler = llgo
			args = append([]string{"build", "-Os"}, args[1:]...)
		}
		cmd := exec.Command(compiler, args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", tool, err, output)
		}
	}
	for _, procs := range []int{1, 4} {
		for _, place := range []string{"main", "peer"} {
			for _, test := range []struct {
				mode  string
				depth int
			}{
				{"small", 16}, {"small", 64}, {"small", 256}, {"large", 2},
			} {
				t.Run(fmt.Sprintf("P%d/%s/%s%d", procs, place, test.mode, test.depth), func(t *testing.T) {
					var expected string
					for _, tool := range []string{"go", "llgo"} {
						cmd := exec.Command(images[tool], test.mode, strconv.Itoa(test.depth), place)
						cmd.Dir = dir
						cmd.Env = append(os.Environ(), "GOMAXPROCS="+strconv.Itoa(procs))
						output, err := cmd.CombinedOutput()
						if err != nil {
							t.Fatalf("%s: %v\n%s", tool, err, output)
						}
						actual := strings.TrimSpace(string(output))
						if tool == "go" {
							expected = actual
							if !strings.HasPrefix(expected, "stack budget ") {
								t.Fatal(expected)
							}
						} else if actual != expected {
							t.Fatalf("LLGo=%q Go=%q", actual, expected)
						}
					}
				})
			}
		}
	}
}
