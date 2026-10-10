package build

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/xgo-dev/llgo/internal/packages"
)

// The selected Go compiler owns GODEBUG policy, including version defaults,
// workspace directives, selected source files, and generated test mains.
// Keep its result per entry instead of baking application policy into a
// cached runtime package shared by multiple executables.
func resolveDefaultGODEBUG(cfg *packages.Config, goroot string, roots []*packages.Package, patterns []string) (map[string]string, error) {
	entries := make(map[string]string)
	for _, pkg := range roots {
		if pkg.Name == "main" {
			entries[pkg.ID] = ""
		}
	}
	if len(entries) == 0 {
		return entries, nil
	}
	goExe := "go"
	if runtime.GOOS == "windows" {
		goExe += ".exe"
	}
	args := []string{"list", "-e", "-json"}
	if cfg.Tests {
		args = append(args, "-test")
	}
	args = append(args, cfg.BuildFlags...)
	args = append(args, "--")
	args = append(args, patterns...)
	// Match the package driver's logical working directory. Inheriting the
	// parent's PWD can make Go canonicalize symlinked paths differently and
	// miss absolute overlay keys (notably /var and /private/var on macOS).
	commands := commandEnv{dir: cfg.Dir, environ: withEnv(cfg.Env, "PWD="+cfg.Dir)}
	cmd := commands.configure(exec.Command(filepath.Join(goroot, "bin", goExe), args...))
	output, err := cmd.Output()
	if err != nil {
		return nil, sourceGoConfigError("resolve default GODEBUG", err)
	}
	dec := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg struct {
			ImportPath     string
			DefaultGODEBUG string
			Error          *struct{ Err string }
		}
		if err := dec.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode default GODEBUG: %w", err)
		}
		if _, ok := entries[pkg.ImportPath]; !ok {
			continue
		}
		if pkg.Error != nil {
			return nil, fmt.Errorf("default GODEBUG for %s: %s", pkg.ImportPath, pkg.Error.Err)
		}
		// Go versions before 1.21 omit the field, yielding an empty default.
		entries[pkg.ImportPath] = pkg.DefaultGODEBUG
	}
	return entries, nil
}
