package build

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"

	llruntime "github.com/goplus/llgo/runtime"
	"golang.org/x/tools/go/ast/astutil"
)

func cloneOverlay(src map[string][]byte) map[string][]byte {
	if len(src) == 0 {
		return nil
	}
	dup := make(map[string][]byte, len(src))
	for k, v := range src {
		dup[k] = slices.Clone(v)
	}
	return dup
}

type sourcePatchBuildContext struct {
	goos       string
	goarch     string
	goversion  string
	buildFlags []string
}

func buildSourcePatchOverlay(base map[string][]byte, runtimeDir string) (map[string][]byte, error) {
	return buildSourcePatchOverlayForGOROOT(base, runtimeDir, runtime.GOROOT(), sourcePatchBuildContext{})
}

func buildSourcePatchOverlayForGOROOT(base map[string][]byte, runtimeDir, goroot string, ctx sourcePatchBuildContext) (map[string][]byte, error) {
	var out map[string][]byte
	for _, pkgPath := range llruntime.SourcePatchPkgPaths() {
		changed, next, err := applySourcePatchForPkg(base, out, runtimeDir, goroot, pkgPath, ctx)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		out = next
	}
	if out == nil {
		return base, nil
	}
	return out, nil
}

func applySourcePatchForPkg(base, current map[string][]byte, runtimeDir, goroot, pkgPath string, ctx sourcePatchBuildContext) (bool, map[string][]byte, error) {
	patchDir := filepath.Join(runtimeDir, "_patch", filepath.FromSlash(pkgPath))
	entries, err := os.ReadDir(patchDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, current, nil
		}
		return false, nil, fmt.Errorf("read source patch dir %s: %w", pkgPath, err)
	}

	srcDir := filepath.Join(goroot, "src", filepath.FromSlash(pkgPath))
	srcEntries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, current, nil
		}
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			return false, current, nil
		}
		return false, nil, fmt.Errorf("read stdlib dir %s: %w", pkgPath, err)
	}

	var (
		out                  = current
		changed              bool
		patchSrcs            = make(map[string][]byte)
		assemblyReplacements = make(map[string][]byte)
		skipAll              bool
		skips                = make(map[string]struct{})
		annotations          = make(map[string][]string)
		annotationCounts     = make(map[string]int)
	)
	readOverlay := func(filename string) ([]byte, error) {
		if out != nil {
			if src, ok := out[filename]; ok {
				return src, nil
			}
		}
		if src, ok := base[filename]; ok {
			return src, nil
		}
		return os.ReadFile(filename)
	}
	buildCtx, err := newSourcePatchMatchContext(goroot, ctx)
	if err != nil {
		return false, nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		isGo := strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
		isPlan9Assembly := strings.HasSuffix(name, ".s")
		if !isGo && !isPlan9Assembly {
			continue
		}
		match, err := buildCtx.MatchFile(patchDir, name)
		if err != nil {
			return false, nil, fmt.Errorf("match source patch file %s: %w", filepath.Join(patchDir, name), err)
		}
		if !match {
			continue
		}
		filename := filepath.Join(patchDir, name)
		src, err := readOverlay(filename)
		if err != nil {
			return false, nil, fmt.Errorf("read source patch file %s: %w", filename, err)
		}
		if isPlan9Assembly {
			original := filepath.Join(srcDir, name)
			if _, err := os.Stat(original); err != nil {
				return false, nil, fmt.Errorf("source patch assembly replacement %s has no GOROOT target: %w", filename, err)
			}
			assemblyReplacements[name] = src
			continue
		}
		directives, err := collectSourcePatchDirectives(src)
		if err != nil {
			return false, nil, fmt.Errorf("parse source patch directives %s: %w", filename, err)
		}
		patchSrcs[name] = buildInjectedSourcePatchFile(filename, src)
		if directives.skipAll {
			skipAll = true
		}
		for name := range directives.skips {
			skips[name] = struct{}{}
		}
		for name, markers := range directives.annotations {
			annotations[name] = append(annotations[name], markers...)
		}
	}
	if len(patchSrcs) == 0 {
		return false, current, nil
	}

	ensureOverlay := func() {
		if out == nil {
			out = cloneOverlay(base)
			if out == nil {
				out = make(map[string][]byte)
			}
		}
	}

	if llruntime.SourcePatchReplacesAsmForGOARCH(pkgPath, ctx.goarch) {
		for _, entry := range srcEntries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".s") || strings.HasSuffix(name, "_test.s") {
				continue
			}
			match, err := buildCtx.MatchFile(srcDir, name)
			if err != nil {
				return false, nil, fmt.Errorf("match stdlib assembly file %s: %w", filepath.Join(srcDir, name), err)
			}
			if !match {
				continue
			}
			ensureOverlay()
			out[filepath.Join(srcDir, name)] = []byte("// replaced by LLGo source patch\n")
			changed = true
		}
	}

	if skipAll {
		for _, entry := range srcEntries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			filename := filepath.Join(srcDir, name)
			src, err := readOverlay(filename)
			if err != nil {
				return false, nil, fmt.Errorf("read stdlib source file %s: %w", filename, err)
			}
			stub, err := packageStubSource(src)
			if err != nil {
				return false, nil, fmt.Errorf("build stdlib stub %s: %w", filename, err)
			}
			ensureOverlay()
			out[filename] = stub
			changed = true
		}
	} else if len(skips) != 0 || len(annotations) != 0 {
		for _, entry := range srcEntries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			filename := filepath.Join(srcDir, name)
			src, err := readOverlay(filename)
			if err != nil {
				return false, nil, fmt.Errorf("read stdlib source file %s: %w", filename, err)
			}
			filtered, changedFile := src, false
			if len(skips) != 0 {
				filtered, changedFile, err = filterSourcePatchFile(src, skips)
				if err != nil {
					return false, nil, fmt.Errorf("filter stdlib source file %s: %w", filename, err)
				}
			}
			annotated, annotatedFile, counts, err := annotateSourcePatchFile(filtered, annotations)
			if err != nil {
				return false, nil, fmt.Errorf("annotate stdlib source file %s: %w", filename, err)
			}
			for name, count := range counts {
				annotationCounts[name] += count
			}
			if !changedFile && !annotatedFile {
				continue
			}
			ensureOverlay()
			out[filename] = annotated
			changed = true
		}
	}
	for name := range annotations {
		if annotationCounts[name] == 0 {
			return false, nil, fmt.Errorf("source patch annotation target %q was not found in package %s", name, pkgPath)
		}
	}

	for name, src := range patchSrcs {
		target := filepath.Join(srcDir, "z_llgo_patch_"+name)
		ensureOverlay()
		out[target] = src
		changed = true
	}
	for name, src := range assemblyReplacements {
		target := filepath.Join(srcDir, name)
		ensureOverlay()
		out[target] = slices.Clone(src)
		changed = true
	}
	return changed, out, nil
}

func newSourcePatchMatchContext(goroot string, ctx sourcePatchBuildContext) (build.Context, error) {
	buildCtx := build.Default
	if goroot != "" {
		buildCtx.GOROOT = goroot
	}
	if ctx.goos != "" {
		buildCtx.GOOS = ctx.goos
	}
	if ctx.goarch != "" {
		buildCtx.GOARCH = ctx.goarch
	}
	buildCtx.BuildTags = parseSourcePatchBuildTags(ctx.buildFlags)
	if ctx.goversion != "" {
		releaseTags, err := releaseTagsForGoVersion(ctx.goversion)
		if err != nil {
			return build.Context{}, err
		}
		buildCtx.ReleaseTags = releaseTags
	}
	return buildCtx, nil
}

func parseSourcePatchBuildTags(buildFlags []string) []string {
	tags, _ := partitionGoBuildFlags(buildFlags)
	return slices.Compact(tags)
}

// partitionGoBuildFlags extracts the exact single- and double-dash forms
// understood by the Go command (-tags=value, -tags value, --tags=value, and
// --tags value) and preserves every other flag in its original order.
// Build-tag assembly uses the same split so a later user flag cannot replace
// the compiler-owned tag set. GOFLAGS is outside Config.GoBuildFlags; the
// explicit compiler -tags argument is later on the command line and wins.
func partitionGoBuildFlags(buildFlags []string) (tags, other []string) {
	for i := 0; i < len(buildFlags); i++ {
		flag := buildFlags[i]
		if (flag == "-tags" || flag == "--tags") && i+1 < len(buildFlags) {
			tags = append(tags, splitSourcePatchBuildTags(buildFlags[i+1])...)
			i++
			continue
		}
		if value, ok := strings.CutPrefix(flag, "-tags="); ok {
			tags = append(tags, splitSourcePatchBuildTags(value)...)
			continue
		}
		if value, ok := strings.CutPrefix(flag, "--tags="); ok {
			tags = append(tags, splitSourcePatchBuildTags(value)...)
			continue
		}
		other = append(other, flag)
	}
	return tags, other
}

func splitSourcePatchBuildTags(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' '
	})
}

func releaseTagsForGoVersion(goVersion string) ([]string, error) {
	goVersion = strings.TrimPrefix(goVersion, "go")
	parts := strings.Split(goVersion, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("unsupported Go version %q", goVersion)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("parse Go major version %q: %w", goVersion, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("parse Go minor version %q: %w", goVersion, err)
	}
	if major != 1 || minor < 1 {
		return nil, fmt.Errorf("unsupported Go version %q", goVersion)
	}
	tags := make([]string, 0, minor)
	for v := 1; v <= minor; v++ {
		tags = append(tags, fmt.Sprintf("go1.%d", v))
	}
	return tags, nil
}

func packageStubSource(src []byte) ([]byte, error) {
	lines := strings.SplitAfter(string(src), "\n")
	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(line)
		if strings.HasPrefix(strings.TrimSpace(line), "package ") {
			return []byte(buf.String()), nil
		}
	}
	return nil, fmt.Errorf("package clause not found")
}

type sourcePatchDirectives struct {
	skipAll     bool
	skips       map[string]struct{}
	annotations map[string][]string
}

func collectSourcePatchDirectives(src []byte) (sourcePatchDirectives, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return sourcePatchDirectives{}, err
	}
	d := sourcePatchDirectives{
		skips:       make(map[string]struct{}),
		annotations: make(map[string][]string),
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			line := strings.TrimSpace(comment.Text)
			if name, marker, ok := parseSourcePatchAnnotationDirective(line); ok {
				for _, previous := range d.annotations[name] {
					if previous == marker {
						return sourcePatchDirectives{}, fmt.Errorf(
							"duplicate source patch annotation %q for %q", marker, name,
						)
					}
				}
				d.annotations[name] = append(d.annotations[name], marker)
				continue
			}
			skipAll, names, ok := parseSourcePatchDirective(line)
			if !ok {
				continue
			}
			if skipAll {
				d.skipAll = true
			}
			for _, name := range names {
				d.skips[name] = struct{}{}
			}
		}
	}
	for _, decl := range file.Decls {
		for _, name := range declPatchKeys(decl) {
			d.skips[name] = struct{}{}
		}
	}
	return d, nil
}

// parseSourcePatchDirective recognizes build-time source-patch directives.
//
// Supported directives:
//   - //llgo:skipall: clear every stdlib .go file in the patched package to a package stub
//   - //llgo:skip A B: comment the named declarations from the stdlib package view
//   - //llgo:annotate F marker: attach //llgo:marker to the exact stdlib function F
//   - //llgo:annotate F coro noblock: attach the exact multiword coroutine directive
//
// Unlike cl/import.go directives, these are consumed only while constructing the
// load-time overlay and are rewritten to plain comments before type checking.
func parseSourcePatchDirective(line string) (skipAll bool, names []string, ok bool) {
	const (
		llgo1 = "//llgo:"
		llgo2 = "// llgo:"
		go1   = "//go:"
	)
	if strings.HasPrefix(line, go1) {
		return false, nil, false
	}
	var tail string
	switch {
	case strings.HasPrefix(line, llgo1):
		tail = line[len(llgo1):]
	case strings.HasPrefix(line, llgo2):
		tail = line[len(llgo2):]
	default:
		return false, nil, false
	}
	switch {
	case tail == "skipall":
		return true, nil, true
	case strings.HasPrefix(tail, "skip "):
		return false, strings.Fields(tail[len("skip "):]), true
	default:
		return false, nil, false
	}
}

func parseSourcePatchAnnotationDirective(line string) (name, marker string, ok bool) {
	const (
		llgo1 = "//llgo:annotate "
		llgo2 = "// llgo:annotate "
	)
	var tail string
	switch {
	case strings.HasPrefix(line, llgo1):
		tail = line[len(llgo1):]
	case strings.HasPrefix(line, llgo2):
		tail = line[len(llgo2):]
	default:
		return "", "", false
	}
	fields := strings.Fields(tail)
	if len(fields) < 2 {
		return "", "", false
	}
	marker = strings.Join(fields[1:], " ")
	if !validSourcePatchAnnotationMarker(marker) {
		return "", "", false
	}
	return fields[0], marker, true
}

func validSourcePatchAnnotationMarker(marker string) bool {
	if marker == "coro noblock" {
		return true
	}
	if marker == "" || marker[0] < 'a' || marker[0] > 'z' {
		return false
	}
	for index := 1; index < len(marker); index++ {
		ch := marker[index]
		if ch < 'a' || ch > 'z' {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func sourcePatchAnnotationDirective(marker string) string {
	if marker == "coro noblock" {
		return "//llgo:coro noblock"
	}
	return "//llgo:" + marker
}

func sanitizeSourcePatchDirectiveLines(src []byte) []byte {
	out := slices.Clone(src)
	lines := bytes.SplitAfter(out, []byte{'\n'})
	changed := false
	markDirective := func(line []byte, prefix []byte) bool {
		idx := bytes.Index(line, prefix)
		if idx < 0 {
			return false
		}
		line[idx+len(prefix)-1] = '_'
		return true
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(string(line))
		_, _, annotation := parseSourcePatchAnnotationDirective(trimmed)
		if _, _, ok := parseSourcePatchDirective(trimmed); !ok && !annotation {
			continue
		}
		switch {
		case markDirective(line, []byte("//llgo:")):
			changed = true
		case markDirective(line, []byte("// llgo:")):
			changed = true
		}
	}
	if !changed {
		return src
	}
	return out
}

type sourcePatchAnnotationInsertion struct {
	offset int
	text   string
}

func annotateSourcePatchFile(src []byte, annotations map[string][]string) ([]byte, bool, map[string]int, error) {
	counts := make(map[string]int)
	if len(annotations) == 0 {
		return src, false, counts, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, false, nil, err
	}
	tokFile := fset.File(file.Pos())
	if tokFile == nil {
		return nil, false, nil, fmt.Errorf("missing file positions")
	}
	var insertions []sourcePatchAnnotationInsertion
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := declPatchKeyFunc(function)
		markers := annotations[name]
		if len(markers) == 0 {
			continue
		}
		counts[name]++
		existing := make(map[string]struct{})
		if function.Doc != nil {
			for _, comment := range function.Doc.List {
				existing[strings.TrimSpace(comment.Text)] = struct{}{}
			}
		}
		var text strings.Builder
		for _, marker := range markers {
			directive := sourcePatchAnnotationDirective(marker)
			if _, present := existing[directive]; present {
				continue
			}
			text.WriteString(directive)
			text.WriteByte('\n')
		}
		if text.Len() != 0 {
			insertions = append(insertions, sourcePatchAnnotationInsertion{
				offset: tokFile.Offset(function.Type.Pos()),
				text:   text.String(),
			})
		}
	}
	if len(insertions) == 0 {
		return src, false, counts, nil
	}
	slices.SortFunc(insertions, func(left, right sourcePatchAnnotationInsertion) int {
		return right.offset - left.offset
	})
	out := slices.Clone(src)
	for _, insertion := range insertions {
		if insertion.offset < 0 || insertion.offset > len(out) {
			return nil, false, nil, fmt.Errorf("annotation insertion offset %d is outside source", insertion.offset)
		}
		next := make([]byte, 0, len(out)+len(insertion.text))
		next = append(next, out[:insertion.offset]...)
		next = append(next, insertion.text...)
		next = append(next, out[insertion.offset:]...)
		out = next
	}
	return out, true, counts, nil
}

func buildInjectedSourcePatchFile(filename string, src []byte) []byte {
	sanitized := sanitizeSourcePatchDirectiveLines(src)
	var out bytes.Buffer
	out.WriteString("//line ")
	out.WriteString(filepath.ToSlash(filename))
	out.WriteString(":1\n")
	out.Write(sanitized)
	return out.Bytes()
}

func filterSourcePatchFile(src []byte, skips map[string]struct{}) ([]byte, bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, false, err
	}
	tokFile := fset.File(file.Pos())
	if tokFile == nil {
		return nil, false, fmt.Errorf("missing file positions")
	}
	changed := false
	var spans []sourcePatchSpan
	decls := file.Decls[:0]
	for _, decl := range file.Decls {
		filteredDecl, removed, rmSpans, err := filterSourcePatchDecl(tokFile, decl, skips)
		if err != nil {
			return nil, false, err
		}
		if removed {
			changed = true
		}
		spans = append(spans, rmSpans...)
		if filteredDecl != nil {
			decls = append(decls, filteredDecl)
		}
	}
	if !changed {
		return src, false, nil
	}
	file.Decls = decls
	spans = append(spans, collectUnusedImportSpans(tokFile, file)...)
	if len(spans) == 0 {
		return src, false, nil
	}
	return commentSourcePatchSpans(src, spans), true, nil
}

type sourcePatchSpan struct {
	start int
	end   int
}

func filterSourcePatchDecl(tokFile *token.File, decl ast.Decl, skips map[string]struct{}) (ast.Decl, bool, []sourcePatchSpan, error) {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		if _, ok := skips[declPatchKeyFunc(decl)]; ok {
			return nil, true, nodeAndCommentsSpans(tokFile, decl), nil
		}
		return decl, false, nil, nil
	case *ast.GenDecl:
		switch decl.Tok {
		case token.TYPE, token.VAR, token.CONST:
		default:
			return decl, false, nil, nil
		}
		specs := decl.Specs[:0]
		removedAny := false
		var removedSpans []sourcePatchSpan
		for _, spec := range decl.Specs {
			filteredSpec, removed, spans := filterSourcePatchSpec(tokFile, spec, skips)
			if removed {
				removedAny = true
			}
			removedSpans = append(removedSpans, spans...)
			if filteredSpec != nil {
				specs = append(specs, filteredSpec)
			}
		}
		if len(specs) == 0 {
			return nil, removedAny, nodeAndCommentsSpans(tokFile, decl), nil
		}
		if !removedAny {
			return decl, false, nil, nil
		}
		out := *decl
		out.Specs = specs
		return &out, true, removedSpans, nil
	default:
		return decl, false, nil, nil
	}
}

func filterSourcePatchSpec(tokFile *token.File, spec ast.Spec, skips map[string]struct{}) (ast.Spec, bool, []sourcePatchSpan) {
	switch spec := spec.(type) {
	case *ast.TypeSpec:
		if _, ok := skips[spec.Name.Name]; ok {
			return nil, true, nodeAndCommentsSpans(tokFile, spec)
		}
		return spec, false, nil
	case *ast.ValueSpec:
		keep := make([]*ast.Ident, 0, len(spec.Names))
		removed := false
		for _, name := range spec.Names {
			if _, ok := skips[name.Name]; ok {
				removed = true
				continue
			}
			keep = append(keep, name)
		}
		if !removed {
			return spec, false, nil
		}
		if len(keep) == 0 {
			return nil, true, nodeAndCommentsSpans(tokFile, spec)
		}
		if len(keep) != len(spec.Names) {
			// Keep the transformation simple and deterministic. Mixed multi-name specs
			// can be split later if we need them in real patches.
			return nil, true, nodeAndCommentsSpans(tokFile, spec)
		}
		out := *spec
		out.Names = keep
		return &out, true, nil
	default:
		return spec, false, nil
	}
}

func collectUnusedImportSpans(tokFile *token.File, file *ast.File) []sourcePatchSpan {
	var spans []sourcePatchSpan
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		removable := make([]*ast.ImportSpec, 0, len(gen.Specs))
		keptCount := 0
		for _, spec := range gen.Specs {
			imp := spec.(*ast.ImportSpec)
			if imp.Name != nil && imp.Name.Name == "_" {
				keptCount++
				continue
			}
			if astutil.UsesImport(file, strings.Trim(imp.Path.Value, `"`)) {
				keptCount++
				continue
			}
			removable = append(removable, imp)
		}
		if len(removable) == 0 {
			continue
		}
		if keptCount == 0 || !gen.Lparen.IsValid() || len(gen.Specs) == 1 {
			spans = append(spans, nodeAndCommentsSpans(tokFile, gen)...)
			continue
		}
		for _, imp := range removable {
			spans = append(spans, nodeAndCommentsSpans(tokFile, imp)...)
		}
	}
	return spans
}

func nodeAndCommentsSpans(tokFile *token.File, node ast.Node) []sourcePatchSpan {
	spans := []sourcePatchSpan{nodeSpan(tokFile, node)}
	switch node := node.(type) {
	case *ast.FuncDecl:
		spans = append(spans, commentGroupsToSpans(tokFile, compactCommentGroups(node.Doc))...)
	case *ast.GenDecl:
		spans = append(spans, commentGroupsToSpans(tokFile, compactCommentGroups(node.Doc))...)
		for _, spec := range node.Specs {
			spans = append(spans, commentGroupsToSpans(tokFile, collectSpecComments(spec))...)
		}
	case *ast.ImportSpec:
		spans = append(spans, commentGroupsToSpans(tokFile, compactCommentGroups(node.Doc, node.Comment))...)
	default:
		if spec, ok := node.(ast.Spec); ok {
			spans = append(spans, commentGroupsToSpans(tokFile, collectSpecComments(spec))...)
		}
	}
	return spans
}

func commentGroupsToSpans(tokFile *token.File, groups []*ast.CommentGroup) []sourcePatchSpan {
	out := make([]sourcePatchSpan, 0, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}
		out = append(out, nodeSpan(tokFile, group))
	}
	return out
}

func nodeSpan(tokFile *token.File, node ast.Node) sourcePatchSpan {
	return sourcePatchSpan{
		start: tokFile.Offset(node.Pos()),
		end:   tokFile.Offset(node.End()),
	}
}

func commentSourcePatchSpans(src []byte, spans []sourcePatchSpan) []byte {
	if len(spans) == 0 {
		return slices.Clone(src)
	}
	out := make([]byte, 0, len(src)+len(spans)*4)
	slices.SortFunc(spans, func(a, b sourcePatchSpan) int {
		switch {
		case a.start < b.start:
			return -1
		case a.start > b.start:
			return 1
		case a.end < b.end:
			return -1
		case a.end > b.end:
			return 1
		default:
			return 0
		}
	})
	merged := make([]sourcePatchSpan, 0, len(spans))
	for _, span := range spans {
		if span.start < 0 {
			span.start = 0
		}
		if span.end > len(src) {
			span.end = len(src)
		}
		if span.start >= span.end {
			continue
		}
		if len(merged) == 0 || span.start > merged[len(merged)-1].end {
			merged = append(merged, span)
			continue
		}
		if span.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = span.end
		}
	}
	cursor := 0
	for _, span := range merged {
		if span.start > cursor {
			out = append(out, src[cursor:span.start]...)
		}
		out = appendCommentedSourcePatchSpan(out, src[span.start:span.end])
		cursor = span.end
	}
	out = append(out, src[cursor:]...)
	return out
}

func appendCommentedSourcePatchSpan(dst, src []byte) []byte {
	lines := bytes.SplitAfter(src, []byte{'\n'})
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		newline := len(line)
		if line[newline-1] == '\n' {
			newline--
		}
		body := line[:newline]
		suffix := line[newline:]
		if len(bytes.TrimSpace(body)) == 0 {
			dst = append(dst, line...)
			continue
		}
		indent := 0
		for indent < len(body) && (body[indent] == ' ' || body[indent] == '\t') {
			indent++
		}
		dst = append(dst, body[:indent]...)
		dst = append(dst, '/', '/')
		if indent < len(body) {
			dst = append(dst, ' ')
			dst = append(dst, body[indent:]...)
		}
		dst = append(dst, suffix...)
	}
	return dst
}

func collectSpecComments(spec ast.Spec) []*ast.CommentGroup {
	switch spec := spec.(type) {
	case *ast.TypeSpec:
		return compactCommentGroups(spec.Doc, spec.Comment)
	case *ast.ValueSpec:
		return compactCommentGroups(spec.Doc, spec.Comment)
	default:
		return nil
	}
}

func compactCommentGroups(groups ...*ast.CommentGroup) []*ast.CommentGroup {
	var out []*ast.CommentGroup
	for _, group := range groups {
		if group != nil {
			out = append(out, group)
		}
	}
	return out
}

func declPatchKeys(decl ast.Decl) []string {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		return []string{declPatchKeyFunc(decl)}
	case *ast.GenDecl:
		var out []string
		for _, spec := range decl.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				out = append(out, spec.Name.Name)
			case *ast.ValueSpec:
				for _, name := range spec.Names {
					out = append(out, name.Name)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func declPatchKeyFunc(decl *ast.FuncDecl) string {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return decl.Name.Name
	}
	return recvPatchKey(decl.Recv.List[0].Type) + "." + decl.Name.Name
}

func recvPatchKey(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.ParenExpr:
		return recvPatchKey(expr.X)
	case *ast.StarExpr:
		return "(*" + recvPatchKey(expr.X) + ")"
	case *ast.IndexExpr:
		return recvPatchKey(expr.X)
	case *ast.IndexListExpr:
		return recvPatchKey(expr.X)
	case *ast.SelectorExpr:
		return expr.Sel.Name
	default:
		// Source patches are expected to use ordinary named receiver types.
		// Treat unsupported receiver forms as a fail-fast patch authoring error
		// instead of silently leaving the original declaration active.
		panic(fmt.Sprintf("unhandled expression type in recvPatchKey: %T", expr))
	}
}
