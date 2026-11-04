//go:build !llgo && (darwin || linux)

package runtime

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"testing"
	"time"
)

// Run the production factory bodies with their real atomic ledger and C queue.
// Only the pthread creation boundary and fatal exit are replaced: successful
// creates still start the production worker routine on joinable native threads.
// This host projection complements the LLGo/collector native acceptance.
func TestRuntimeCoroWorkerCreationFailureWithNativeQueue(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("cc is unavailable")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const module = "github.com/xgo-dev/llgo/runtime/workercreationprobe"
	write := func(name string, data []byte) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", []byte(fmt.Sprintf("module %s\n\ngo 1.27.0\n\nrequire github.com/xgo-dev/llgo/runtime v0.0.0\nreplace github.com/xgo-dev/llgo/runtime => %q\n", module, root)))
	var source bytes.Buffer
	source.WriteString("package workercreationprobe\nimport (\n\"github.com/xgo-dev/llgo/runtime/internal/coro\"\ncoroworker \"" + module + "/worker\"\nthreadpkg \"" + module + "/thread\"\n)\n")
	for _, path := range []string{
		"internal/runtime/coro_worker_native_llgo.go",
		"internal/runtime/coro_physical_thread_capacity_native_llgo.go",
	} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if group, ok := decl.(*ast.GenDecl); ok && group.Tok == token.IMPORT {
				continue
			}
			// All factory declarations through the growth hook are included;
			// subsequent functions implement scheduler ingress and dispatch.
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "coroNativeWorkerPoolStartV1" {
				break
			}
			if err := format.Node(&source, fset, decl); err != nil {
				t.Fatal(err)
			}
			source.WriteByte('\n')
		}
	}
	write("factory.go", source.Bytes())
	for _, name := range []string{"factory_test.go", "worker/bridge.go", "worker/bridge.c", "thread/thread.go"} {
		data, err := os.ReadFile(filepath.Join("testdata/worker_creation", name))
		if err != nil {
			t.Fatal(err)
		}
		write(name, data)
	}
	for _, name := range []string{"worker.c", "worker.h"} {
		data, err := os.ReadFile(filepath.Join("internal/coroworker/_worker", name))
		if err != nil {
			t.Fatal(err)
		}
		destination := name
		if name == "worker.c" {
			destination = "worker_impl.h"
		}
		write("worker/"+destination, data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	image := filepath.Join(dir, "factory.test")
	cmd := exec.CommandContext(ctx, filepath.Join(stdruntime.GOROOT(), "bin/go"), "test", "-c", "-p=1", "-race", "-o", image, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=1", "GOMAXPROCS=2")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build native worker creation control: %v\n%s", err, output)
	}
	for _, name := range []string{"TestInitialFailure", "TestPartialFailure2", "TestPartialFailure3", "TestPartialFailure4", "TestSuccessfulGrowthAndJoin", "TestSealWaitsForAdmittedNativeCreate"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, image, "-test.v", "-test.run=^"+name+"$", "-test.timeout=25s")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOMAXPROCS=2")
			output, err := cmd.CombinedOutput()
			if err != nil || !bytes.Contains(output, []byte("--- PASS: "+name+" ")) || bytes.Contains(output, []byte("--- SKIP:")) {
				t.Fatalf("native creation control: %v\n%s", err, output)
			}
			t.Log(string(output))
		})
	}
}
