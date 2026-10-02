package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/xgo-dev/llgo/internal/assemblypath"
)

// This diagnostic is isolated from the LLVM-backed compiler. It checks the
// original selected toolchain paths with the same host Go as the failing CI.
func main() {
	fmt.Printf("toolchain=%s host=%s/%s runtimeGOROOT=%q envGOROOT=%q\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOROOT(), os.Getenv("GOROOT"))
	root, err := exec.Command("go", "env", "GOROOT").CombinedOutput()
	fmt.Printf("go env GOROOT: %q error=%v\n", root, err)
	paths := []string{
		os.TempDir(), runtime.GOROOT(),
		filepath.Join(runtime.GOROOT(), "src", "internal", "cpu"),
		filepath.Join(runtime.GOROOT(), "src", "internal", "bytealg"),
		filepath.Join(runtime.GOROOT(), "pkg", "include"),
		filepath.Join(runtime.GOROOT(), "pkg", "include", "textflag.h"),
		filepath.Join(runtime.GOROOT(), "pkg", "include", "funcdata.h"),
	}
	failed := err != nil
	for _, original := range paths {
		absolute, err := filepath.Abs(original)
		fmt.Printf("original=%q absolute=%q error=%v\n", original, absolute, err)
		if err != nil {
			failed = true
			continue
		}
		info, statErr := os.Stat(absolute)
		lexical, evalErr := filepath.EvalSymlinks(absolute)
		fmt.Printf("original Go symlink walk: canonical=%q error=%v\n", lexical, evalErr)
		canonical, canonicalErr := assemblypath.Canonical(absolute)
		canonicalInfo, canonicalStatErr := os.Stat(canonical)
		same := statErr == nil && canonicalStatErr == nil && os.SameFile(info, canonicalInfo)
		fmt.Printf("physical original identity: stat=%v canonical=%q resolve=%v canonicalStat=%v sameFile=%t\n",
			statErr, canonical, canonicalErr, canonicalStatErr, same)
		if statErr != nil || canonicalErr != nil || !same {
			failed = true
		}
		if statErr == nil && !info.IsDir() {
			body, readErr := os.ReadFile(absolute)
			fmt.Printf("original read: bytes=%d error=%v\n", len(body), readErr)
			failed = failed || readErr != nil || len(body) == 0
		}
		for path := absolute; ; path = filepath.Dir(path) {
			probeAncestor(path)
			if parent := filepath.Dir(path); parent == path {
				break
			}
		}
	}
	if failed {
		os.Exit(1)
	}
}

func probeAncestor(path string) {
	info, statErr := os.Lstat(path)
	var detail string
	if info != nil {
		detail = fmt.Sprintf("name=%q mode=%s", info.Name(), info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			detail += fmt.Sprintf(" readlink=%q error=%v", target, err)
		}
	}
	canonical, evalErr := filepath.EvalSymlinks(path)
	fmt.Printf("ancestor=%q lstat=%v %s canonical=%q eval=%v\n", path, statErr, detail, canonical, evalErr)
	wide, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		fmt.Printf("utf16 error=%v\n", err)
		return
	}
	var data syscall.Win32finddata
	handle, findErr := syscall.FindFirstFile(wide, &data)
	fmt.Printf("FindFirstFile=%v name=%q attributes=%#x reparseTag=%#x\n",
		findErr, syscall.UTF16ToString(data.FileName[:]), data.FileAttributes, data.Reserved0)
	if findErr == nil {
		if closeErr := syscall.FindClose(handle); closeErr != nil {
			panic(closeErr)
		}
	}
}
