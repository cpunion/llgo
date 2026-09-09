package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xgo-dev/llvm"
)

// Diagnostic branch only. Bitcode avoids materializing a large textual IR
// string in the compiler heap while measuring the compiler's own RSS.
func traceR4MainModule(stage, pkgPath string, mod llvm.Module) bool {
	dir := os.Getenv("LLGO_R4_LLVM_TRACE")
	if dir == "" || pkgPath != "main" && pkgPath != "command-line-arguments" {
		return false
	}
	fmt.Fprintf(os.Stderr, "[r4-llvm-phase] %s %s %s\n", time.Now().UTC().Format(time.RFC3339Nano), pkgPath, stage)
	if mod.C == nil {
		return true
	}
	dir = filepath.Join(dir, strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(dir, 0700); err != nil {
		panic(err)
	}
	file, err := os.OpenFile(filepath.Join(dir, stage+".bc"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if err := llvm.WriteBitcodeToFile(mod, file); err != nil {
		panic(err)
	}
	return true
}
