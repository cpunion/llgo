//go:build !llgo

package crosscompile

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSFSCallOperationProtocol(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the JavaScript host protocol gate")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--max-old-space-size=128", "--unhandled-rejections=strict",
		filepath.Join(root, "internal/crosscompile/testdata/coro_js_fs/operations.cjs"),
		filepath.Join(root, "targets/wasm_exec.js"))
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "coro JS fs operation protocol ok\n") {
		t.Fatalf("JS fsCall lifecycle protocol failed: %v\n%s", err, output)
	}
}
