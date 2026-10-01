package plan9asm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/scanner"
	"unicode"

	"github.com/xgo-dev/llgo/internal/env"
	"github.com/xgo-dev/llgo/internal/packages"
	extplan9asm "github.com/xgo-dev/plan9asm"
)

const assemblySourceByteLimit = 64 << 20

// ReadAssemblyFileWithIncludes preprocesses actual selected source and headers.
// The search stays fixed at the package directory and selected GOROOT/pkg/include;
// nested headers do not change it. Only the P9 state machine opens active headers.
// File reads enforce the shared 64 MiB source/include quota while reading; overlay
// bytes are checked before preprocessing. Expanded output is also bounded.
func ReadAssemblyFileWithIncludes(pkg *packages.Package, file string, overlay map[string][]byte, goos, goarch string, opt TranslateOptions) ([]byte, error) {
	source, err := ReadFileWithOverlay(overlay, file)
	if err != nil {
		return nil, err
	}
	return preprocessAssemblyForPkg(pkg, file, source, overlay, goos, goarch, opt)
}

func preprocessAssemblyForPkg(pkg *packages.Package, file string, source []byte, overlay map[string][]byte, goos, goarch string, opt TranslateOptions) ([]byte, error) {
	return preprocessAssemblyForPkgWithLimit(pkg, file, source, overlay, goos, goarch, opt, assemblySourceByteLimit)
}

func preprocessAssemblyForPkgWithLimit(pkg *packages.Package, file string, source []byte, overlay map[string][]byte, goos, goarch string, opt TranslateOptions, limit int) ([]byte, error) {
	if pkg == nil {
		return nil, fmt.Errorf("assembly preprocessing requires a package context")
	}
	if err := validateAssemblyReadLimit(limit); err != nil {
		return nil, err
	}
	if len(source) > limit {
		return nil, fmt.Errorf("assembly source exceeds %d-byte inventory bound", limit)
	}
	defines := opt.AssemblyDefines
	if defines == nil {
		defines = extplan9asm.GoAssemblerDefines(goos, goarch)
	}
	consumedBytes := len(source)
	var searchDirs []assemblyIncludeDirectory
	resolver := func(parent, name string) (string, []byte, error) {
		if name == "" || filepath.IsAbs(name) || strings.ContainsRune(name, 0) {
			return "", nil, fmt.Errorf("unsupported bounded assembly include %q", name)
		}
		if searchDirs == nil {
			var err error
			searchDirs, err = assemblyIncludeDirectories(pkg, file, opt.SourceGOROOT)
			if err != nil {
				return "", nil, err
			}
		}
		for _, search := range searchDirs {
			candidate := filepath.Clean(filepath.Join(search.canonical, filepath.FromSlash(name)))
			if !assemblyWithinRoot(search.root, candidate) {
				return "", nil, fmt.Errorf("assembly include %q escapes its source root", name)
			}
			original := filepath.Clean(filepath.Join(search.original, filepath.FromSlash(name)))
			body, overlaid := overlay[original]
			if !overlaid {
				body, overlaid = overlay[candidate]
			}
			resolved, err := filepath.EvalSymlinks(candidate)
			if os.IsNotExist(err) && overlaid {
				parentDir, parentErr := filepath.EvalSymlinks(filepath.Dir(candidate))
				if parentErr != nil {
					return "", nil, parentErr
				}
				resolved, err = filepath.Join(parentDir, filepath.Base(candidate)), nil
			}
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", nil, err
			}
			if !assemblyWithinRoot(search.root, resolved) {
				return "", nil, fmt.Errorf("assembly include %q escapes through a symlink", name)
			}
			if !overlaid {
				body, err = readAssemblyFileBounded(nil, resolved, limit-consumedBytes)
				if err != nil {
					return "", nil, fmt.Errorf("assembly include %q: %w", name, err)
				}
			}
			if len(body) > limit-consumedBytes {
				return "", nil, fmt.Errorf("assembly include graph exceeds %d-byte inventory bound", limit)
			}
			consumedBytes += len(body)
			return resolved, body, nil
		}
		if name == "go_asm.h" {
			body, err := extplan9asm.GoAssemblyHeader(extplan9asm.GoPackage{Path: pkg.PkgPath, Types: pkg.Types}, goarch)
			if len(body) > limit-consumedBytes {
				return "", nil, fmt.Errorf("generated assembly header exceeds inventory bound")
			}
			consumedBytes += len(body)
			return "<generated-go_asm.h:" + pkg.PkgPath + ">", body, err
		}
		return "", nil, fmt.Errorf("assembly include %q not found for %s", name, parent)
	}
	expanded, err := extplan9asm.PreprocessAssemblySource(string(source), extplan9asm.AssemblyPreprocessOptions{
		FileName: file, Defines: defines, ReadInclude: resolver,
	})
	if err != nil {
		return nil, err
	}
	if len(expanded) > limit {
		return nil, fmt.Errorf("expanded assembly exceeds %d-byte inventory bound", limit)
	}
	// These are header macros, not instructions. The legacy parser accepts
	// NO_LOCAL_POINTERS permissively; actual source must define and expand it.
	if err := rejectUndefinedAssemblyMetadata(expanded, file); err != nil {
		return nil, err
	}
	return []byte(expanded), nil
}

func rejectUndefinedAssemblyMetadata(source, file string) error {
	var tokens scanner.Scanner
	tokens.Init(strings.NewReader(source))
	tokens.Mode = scanner.ScanIdents | scanner.ScanStrings | scanner.ScanRawStrings |
		scanner.ScanChars | scanner.ScanInts | scanner.ScanFloats
	tokens.Whitespace &^= 1 << '\n'
	// cmd/asm identifiers include these assembly-specific Unicode runes.
	tokens.IsIdentRune = func(ch rune, index int) bool {
		return unicode.IsLetter(ch) || ch == '_' || ch == '·' || ch == '∕' || index > 0 && unicode.IsDigit(ch)
	}
	tokens.Error = func(*scanner.Scanner, string) {} // the Plan 9 parser diagnoses malformed tokens
	atStart := true
	for token := tokens.Scan(); token != scanner.EOF; token = tokens.Scan() {
		if token == '\n' || token == ';' {
			atStart = true
			continue
		}
		if !atStart {
			continue
		}
		if token != scanner.Ident {
			atStart = false
			continue
		}
		name := tokens.TokenText()
		next := tokens.Scan()
		if next == ':' { // a label, not an instruction; multiple labels are legal
			continue
		}
		if name == "GO_ARGS" || name == "GO_RESULTS_INITIALIZED" || name == "NO_LOCAL_POINTERS" {
			return fmt.Errorf("undefined Go assembly metadata macro %s in %s", name, file)
		}
		atStart = next == '\n' || next == ';'
	}
	return nil
}

type assemblyIncludeDirectory struct{ original, canonical, root string }

func assemblyIncludeDirectories(pkg *packages.Package, file, goRoot string) ([]assemblyIncludeDirectory, error) {
	var err error
	if goRoot == "" {
		goRoot, err = env.GOROOT()
		if err != nil {
			return nil, err
		}
	}
	goRoot, err = canonicalAssemblyDirectory(goRoot)
	if err != nil {
		return nil, fmt.Errorf("assembly toolchain root: %w", err)
	}
	pkgDir := pkg.Dir
	if pkgDir == "" {
		pkgDir = filepath.Dir(file)
	}
	canonicalPkgDir, err := canonicalAssemblyDirectory(pkgDir)
	if err != nil {
		return nil, fmt.Errorf("assembly package directory: %w", err)
	}
	sourceRoot := canonicalPkgDir
	if pkg.Module != nil && pkg.Module.Dir != "" {
		sourceRoot, err = canonicalAssemblyDirectory(pkg.Module.Dir)
		if err != nil {
			return nil, fmt.Errorf("assembly source root: %w", err)
		}
	}
	if assemblyWithinRoot(goRoot, canonicalPkgDir) {
		sourceRoot = goRoot
	}
	if !assemblyWithinRoot(sourceRoot, canonicalPkgDir) {
		return nil, fmt.Errorf("assembly package escapes source root")
	}
	return []assemblyIncludeDirectory{
		{pkgDir, canonicalPkgDir, sourceRoot},
		{filepath.Join(goRoot, "pkg", "include"), filepath.Join(goRoot, "pkg", "include"), goRoot},
	}, nil
}

func canonicalAssemblyDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func assemblyWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
