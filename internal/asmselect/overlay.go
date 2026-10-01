// Package asmselect selects native Go assembly sources for explicitly named
// external packages without changing global standard-library build tags.
package asmselect

import (
	"bytes"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strings"
)

func HeaderOverlay(dir string, context build.Context) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	overlay := make(map[string][]byte)
	assembly := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := filepath.Ext(name)
		if ext != ".go" && ext != ".s" && ext != ".S" {
			continue
		}
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		match, err := context.MatchFile(dir, name)
		if err != nil {
			return nil, fmt.Errorf("match native assembly profile file %s: %w", name, err)
		}
		// Unlike _test.go, test-named assembly can belong to an ordinary
		// package. Go filename/build-constraint selection is authoritative.
		if match && (ext == ".s" || ext == ".S") {
			assembly++
		}
		filename := filepath.Join(dir, name)
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		header := "//go:build llgo && !llgo\n\n"
		if match {
			header = "//go:build llgo || !llgo\n\n"
		}
		// Only build-constraint lines are removed. Everything after the file
		// header is byte-for-byte original, including assembly instructions.
		var out bytes.Buffer
		out.WriteString(header)
		out.Write(withoutConstraintHeader(data))
		overlay[filename] = out.Bytes()
	}
	if assembly == 0 {
		return nil, fmt.Errorf("native assembly profile selects no assembly source files in %s", dir)
	}
	return overlay, nil
}

func withoutConstraintHeader(data []byte) []byte {
	var out bytes.Buffer
	header, block := true, false
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		trimmed := strings.TrimSpace(string(line))
		if header && !block && (strings.HasPrefix(trimmed, "//go:build ") || strings.HasPrefix(trimmed, "// +build ")) {
			continue
		}
		if header {
			if block {
				if _, rest, ok := strings.Cut(trimmed, "*/"); ok {
					block = false
					header = strings.TrimSpace(rest) == ""
				}
			} else if strings.HasPrefix(trimmed, "/*") {
				_, rest, ok := strings.Cut(trimmed, "*/")
				block = !ok
				if ok && strings.TrimSpace(rest) != "" {
					header = false
				}
			} else if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
				header = false
			}
		}
		out.Write(line)
	}
	return out.Bytes()
}

func ParsePackages(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	var result []string
	seen := make(map[string]bool)
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" || name == "all" || name == "std" || name == "cmd" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") || strings.Contains(name, "...") || strings.ContainsAny(name, "*?[]\\ \t\r\n") {
			return nil, fmt.Errorf("native assembly selection requires exact external import paths, got %q", name)
		}
		if !seen[name] {
			result = append(result, name)
			seen[name] = true
		}
	}
	return result, nil
}

func ValidatePackage(name, module string, standard bool) error {
	if standard || module == "" || module == "github.com/xgo-dev/llgo" || module == "github.com/xgo-dev/llgo/runtime" ||
		name != module && !strings.HasPrefix(name, module+"/") {
		return fmt.Errorf("native assembly selection is only for external packages, not stdlib or llgo runtime: %s", name)
	}
	return nil
}
