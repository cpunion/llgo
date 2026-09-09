package build

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Diagnostic branch only: stage timing and original LLVM IR, never a lowering
// change or an acceptance-budget override. Do not promote this file or its hooks.
func r4DiagnosticBackendStage(pkg *aPackage, stage string, snapshot bool) {
	dir := os.Getenv("LLGO_R4_BACKEND_TRACE")
	if dir == "" || pkg.Name != "main" {
		return
	}
	line := fmt.Sprintf("%s %s %s\n", time.Now().UTC().Format(time.RFC3339Nano), stage, pkg.PkgPath)
	fmt.Fprint(os.Stderr, line)
	f, err := os.OpenFile(filepath.Join(dir, "backend-stages.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	_, writeErr := f.WriteString(line)
	closeErr := f.Close()
	if writeErr != nil {
		panic(writeErr)
	}
	if closeErr != nil {
		panic(closeErr)
	}
	if snapshot {
		if err := os.WriteFile(filepath.Join(dir, stage+".ll"), []byte(pkg.LPkg.Module().String()), 0600); err != nil {
			panic(err)
		}
	}
}
