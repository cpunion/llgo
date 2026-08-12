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

package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/goplus/llgo/internal/filecheck"
	"github.com/goplus/llgo/internal/llgen"
	"github.com/goplus/mod"
	"golang.org/x/mod/modfile"
)

type resolvedTarget struct {
	sourceFile string
	genTarget  string
	pkgDir     string
	modulePath string
	pkgPath    string
}

type irProgram struct {
	globals []irGlobal
	funcs   []irFunction
}

type irGlobal struct {
	symbol string
	line   string
}

type irFunction struct {
	symbol string
	lines  []string
}

var (
	defineQuotedRE   = regexp.MustCompile(`^define\b.* @"([^"]+)"\(`)
	definePlainRE    = regexp.MustCompile(`^define\b.* @([^\s(]+)\(`)
	globalQuotedRE   = regexp.MustCompile(`^@"([^"]+)"\s*=`)
	globalPlainRE    = regexp.MustCompile(`^@([A-Za-z0-9$._-]+)\s*=`)
	globalRefRE      = regexp.MustCompile(`@"([^"]+)"|@([A-Za-z0-9$._-]+)`)
	checkLineRE      = regexp.MustCompile(`^\s*//\s*CHECK(?:-[A-Z0-9]+)*:`)
	checkDirectiveRE = regexp.MustCompile(`^\s*//\s*(CHECK(?:-[A-Z0-9]+)*):\s?(.*?)(?:\r?\n)?$`)
	symbolLineRE     = regexp.MustCompile(`(?m)^\s*//\s*SYMBOL(?:-[A-Z]+)?:`)
	debugMetaRE      = regexp.MustCompile(`, ![A-Za-z0-9_.-]+ ![0-9]+`)
	closureEnvRE     = regexp.MustCompile(`(\s)(?:nest|swiftself)(\s)`)
	testCasePathRE   = regexp.MustCompile(`"[^"]*/cl/_test[^/"]*/[^/".]+`)
	symbolHashRE     = regexp.MustCompile(`\$[-A-Za-z0-9_]{43}`)
	cgoHashRE        = regexp.MustCompile(`(_cgo_)[0-9a-f]+(_Cfunc_)`)
	numericGlobalRE  = regexp.MustCompile(`@\d+\b`)
	metadataIDRE     = regexp.MustCompile(`!\d+\b`)
	sigJumpRE        = regexp.MustCompile(`@(?:__)?(sig(?:set|long)jmp)\b`)
	plainJumpRE      = regexp.MustCompile(`@_*((?:set|long)jmp)\b`)
	jmpBufAllocaRE   = regexp.MustCompile(`alloca i8, i64 (?:196|200), align 1`)
	pthreadTypeRE    = regexp.MustCompile(`/runtime/internal/clite/pthread/sync\.([A-Za-z0-9_]+)"`)
	numericNameRE    = regexp.MustCompile(`^\d+$`)
)

type pthreadOpaqueSize struct {
	sizes *regexp.Regexp
	want  string
}

var pthreadOpaqueSizes = map[string]pthreadOpaqueSize{
	"MutexAttr":  {regexp.MustCompile(`\[(?:4|8|16) x i8\]`), `[{{(4|8|16)}} x i8]`},
	"RWLockAttr": {regexp.MustCompile(`\[(?:8|16|24) x i8\]`), `[{{(8|16|24)}} x i8]`},
	"CondAttr":   {regexp.MustCompile(`\[(?:4|8|16) x i8\]`), `[{{(4|8|16)}} x i8]`},
	"Once":       {regexp.MustCompile(`\[(?:4|16) x i8\]`), `[{{(4|16)}} x i8]`},
	"Mutex":      {regexp.MustCompile(`\[(?:40|48|64) x i8\]`), `[{{(40|48|64)}} x i8]`},
	"RWLock":     {regexp.MustCompile(`\[(?:56|192|200) x i8\]`), `[{{(56|192|200)}} x i8]`},
	"Cond":       {regexp.MustCompile(`\[(?:40|48) x i8\]`), `[{{(40|48)}} x i8]`},
}

func generateFile(target resolvedTarget, force bool) error {
	data, err := os.ReadFile(target.sourceFile)
	if err != nil {
		return err
	}
	ir, err := genIR(target.genTarget)
	if err != nil {
		return err
	}
	if !force {
		updated, changed, err := updateSourceChecks(string(data), target.sourceFile, target.pkgPath, target.modulePath, ir)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		return writeFileAtomically(target.sourceFile, []byte(updated), 0644)
	}
	cleaned := stripCheckDirectives(string(data))
	updated, err := rewriteSource(cleaned, target.sourceFile, target.pkgPath, target.modulePath, ir)
	if err != nil {
		return err
	}
	formatted, err := format.Source([]byte(updated))
	if err != nil {
		return fmt.Errorf("%s: gofmt failed: %w", target.sourceFile, err)
	}
	return writeFileAtomically(target.sourceFile, formatted, 0644)
}

type sourceEdit struct {
	start int
	end   int
	text  string
}

type checkGroup struct {
	start int
	end   int
	text  string
}

type checkDirective struct {
	kind    string
	pattern string
}

type updateContext struct {
	fn       *irFunction
	nextLine int
	canNext  bool
}

// updateSourceChecks regenerates continuous anchor + NEXT/EMPTY snapshots in
// place. Other CHECK forms express hand-written test intent: they are kept
// verbatim and must still pass the final whole-file FileCheck validation.
func updateSourceChecks(src, srcPath, _, modulePath, ir string) (string, bool, error) {
	groups := sourceCheckGroups(src)
	if len(groups) == 0 {
		if symbolLineRE.MatchString(src) {
			return src, false, nil
		}
		return "", false, fmt.Errorf("%s: no CHECK directives; use -force to initialize IR checks", srcPath)
	}
	prog := parseIR(ir)
	functionChecks := indexFunctionChecks(prog.funcs, modulePath)

	var edits []sourceEdit
	var context updateContext
	for _, group := range groups {
		directives, err := parseCheckDirectives(group.text)
		if err != nil {
			return "", false, fmt.Errorf("%s: %w", srcPath, err)
		}
		fn, hasDefinition, err := findFunctionForCheckGroup(group.text, prog.funcs, functionChecks)
		if err != nil {
			return "", false, fmt.Errorf("%s: %w", srcPath, err)
		}
		if hasDefinition {
			context = updateContext{fn: &fn, nextLine: 1, canNext: len(directives) == 1}
		}
		if !isContinuousSnapshot(directives) {
			context = advanceManualContext(context, directives, hasDefinition)
			continue
		}

		lines, start, end, implicit, err := resolveSnapshotRange(group.text, directives, hasDefinition, context, ir, fn)
		if err != nil {
			return "", false, fmt.Errorf("%s: %w; use -force to regenerate all IR checks", srcPath, err)
		}
		generated := buildRangeChecks(lines[start:end+1], modulePath, directives[0], implicit)
		if len(generated) == 0 {
			return "", false, fmt.Errorf("%s: no checks generated for continuous snapshot", srcPath)
		}
		indent := indentAt(src, group.start)
		text := formatDirectiveBlock(indent, generated)
		text = preserveTrailingNewlines(text, group.text)
		if text != group.text {
			edits = append(edits, sourceEdit{start: group.start, end: group.end, text: text})
		}
		if context.fn != nil && sameLineSlice(lines, context.fn.lines) {
			context.nextLine = end + 1
			context.canNext = true
		} else {
			context.canNext = false
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	updated := src
	for _, edit := range edits {
		updated = updated[:edit.start] + edit.text + updated[edit.end:]
	}
	if err := matchCheckText(updated, ir); err != nil {
		return "", false, fmt.Errorf("%s: CHECK validation failed and cannot be safely updated: %w; use -force to regenerate all IR checks", srcPath, err)
	}
	return updated, updated != src, nil
}

func advanceManualContext(context updateContext, directives []checkDirective, hasDefinition bool) updateContext {
	if context.fn == nil {
		context.canNext = false
		return context
	}
	start := 0
	if context.canNext {
		start = context.nextLine
	}
	first := 0
	if hasDefinition {
		first = 1
		start = 1
	}
	matched := false
	for _, directive := range directives[first:] {
		if directive.kind != "CHECK" && directive.kind != "CHECK-LABEL" {
			context.canNext = false
			return context
		}
		lines := matchingDirectiveLines(directive, context.fn.lines, start)
		if len(lines) == 0 {
			context.canNext = false
			return context
		}
		start = lines[0] + 1
		matched = true
	}
	if matched || (hasDefinition && len(directives) == 1) {
		context.nextLine = start
		context.canNext = true
		return context
	}
	context.canNext = false
	return context
}

func sameLineSlice(a, b []string) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

func parseCheckDirectives(group string) ([]checkDirective, error) {
	var directives []checkDirective
	for _, line := range strings.SplitAfter(group, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		match := checkDirectiveRE.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("invalid CHECK directive %q", strings.TrimSpace(line))
		}
		directives = append(directives, checkDirective{kind: match[1], pattern: match[2]})
	}
	return directives, nil
}

func isContinuousSnapshot(directives []checkDirective) bool {
	if len(directives) < 2 || strings.Contains(directives[0].pattern, "[[") {
		return false
	}
	first := directives[0].kind
	if first != "CHECK" && first != "CHECK-LABEL" && first != "CHECK-NEXT" {
		return false
	}
	for _, directive := range directives[1:] {
		if (directive.kind != "CHECK-NEXT" && directive.kind != "CHECK-EMPTY") || strings.Contains(directive.pattern, "[[") {
			return false
		}
	}
	return true
}

func resolveSnapshotRange(group string, directives []checkDirective, hasDefinition bool, context updateContext, ir string, fn irFunction) ([]string, int, int, bool, error) {
	implicit := directives[0].kind == "CHECK-NEXT"
	if implicit {
		if context.fn == nil || !context.canNext || context.nextLine >= len(context.fn.lines) {
			return nil, 0, 0, false, errors.New("CHECK-NEXT snapshot has no recoverable preceding anchor")
		}
		lines := context.fn.lines
		start := context.nextLine
		if snapshotEndsFunction(directives) {
			return lines, start, len(lines) - 1, true, nil
		}
		if end, ok := matchSnapshotAt(group, directives, lines, start, true); ok {
			return lines, start, end, true, nil
		}
		end, err := recoverSnapshotEnd(directives, lines, start)
		if err != nil {
			return nil, 0, 0, false, err
		}
		return lines, start, end, true, nil
	}

	lines := splitIRLines(ir)
	if hasDefinition {
		lines = fn.lines
		if snapshotEndsFunction(directives) {
			return lines, 0, len(lines) - 1, false, nil
		}
		if end, ok := matchSnapshotAt(group, directives, lines, 0, false); ok {
			return lines, 0, end, false, nil
		}
		end, err := recoverSnapshotEnd(directives, lines, 0)
		if err != nil {
			return nil, 0, 0, false, err
		}
		return lines, 0, end, false, nil
	}
	if context.fn != nil {
		lines = context.fn.lines
	}
	minStart := 0
	if context.canNext {
		minStart = context.nextLine
	}
	starts := matchingSnapshotWindows(group, directives, lines, minStart)
	if len(starts) == 1 || (context.canNext && len(starts) > 1) {
		return lines, starts[0], starts[0] + len(directives) - 1, false, nil
	}
	if len(starts) > 1 {
		return nil, 0, 0, false, fmt.Errorf("continuous snapshot matches %d IR ranges", len(starts))
	}
	start, end, err := recoverSnapshotBounds(directives, lines, minStart, context.canNext)
	if err != nil {
		return nil, 0, 0, false, err
	}
	return lines, start, end, false, nil
}

func snapshotEndsFunction(directives []checkDirective) bool {
	for i := len(directives) - 1; i >= 0; i-- {
		if directives[i].kind == "CHECK-EMPTY" {
			continue
		}
		return strings.TrimSpace(directives[i].pattern) == "}"
	}
	return false
}

func matchSnapshotAt(group string, directives []checkDirective, lines []string, start int, implicit bool) (int, bool) {
	count := len(directives)
	if start < 0 || start+count > len(lines) {
		return 0, false
	}
	checks := group
	if implicit {
		checks = replaceFirstDirectiveKind(group, "CHECK")
	}
	input := strings.Join(lines[start:start+count], "\n") + "\n"
	return start + count - 1, matchCheckText(checks, input) == nil
}

func matchingSnapshotWindows(group string, directives []checkDirective, lines []string, minStart int) []int {
	var matches []int
	for start := minStart; start+len(directives) <= len(lines); start++ {
		if _, ok := matchSnapshotAt(group, directives, lines, start, false); ok {
			matches = append(matches, start)
		}
	}
	return matches
}

func recoverSnapshotBounds(directives []checkDirective, lines []string, minStart int, ordered bool) (int, int, error) {
	if directives[len(directives)-1].kind == "CHECK-EMPTY" {
		return 0, 0, errors.New("failed snapshot ends in CHECK-EMPTY and has no stable end anchor")
	}
	starts := matchingDirectiveLines(directives[0], lines, minStart)
	if len(starts) == 0 || (!ordered && len(starts) != 1) {
		return 0, 0, fmt.Errorf("snapshot start anchor matches %d IR lines", len(starts))
	}
	end, err := recoverSnapshotEndWithOrder(directives, lines, starts[0], ordered)
	if err != nil {
		return 0, 0, err
	}
	return starts[0], end, nil
}

func recoverSnapshotEnd(directives []checkDirective, lines []string, start int) (int, error) {
	return recoverSnapshotEndWithOrder(directives, lines, start, false)
}

func recoverSnapshotEndWithOrder(directives []checkDirective, lines []string, start int, ordered bool) (int, error) {
	last := directives[len(directives)-1]
	if last.kind == "CHECK-EMPTY" {
		return 0, errors.New("failed snapshot ends in CHECK-EMPTY and has no stable end anchor")
	}
	ends := matchingDirectiveLines(last, lines, start+1)
	var after []int
	for _, end := range ends {
		after = append(after, end)
	}
	if len(after) == 0 || (!ordered && len(after) != 1) {
		return 0, fmt.Errorf("snapshot end anchor matches %d IR lines after its start", len(after))
	}
	return after[0], nil
}

func matchingDirectiveLines(directive checkDirective, lines []string, minStart int) []int {
	if directive.kind == "CHECK-EMPTY" {
		return nil
	}
	check := "// CHECK: " + directive.pattern + "\n"
	var matches []int
	for i := minStart; i < len(lines); i++ {
		line := lines[i]
		if matchCheckText(check, line+"\n") == nil {
			matches = append(matches, i)
		}
	}
	return matches
}

func replaceFirstDirectiveKind(group, kind string) string {
	loc := checkDirectiveRE.FindStringSubmatchIndex(group)
	if loc == nil {
		return group
	}
	return group[:loc[2]] + kind + group[loc[3]:]
}

func buildRangeChecks(lines []string, modulePath string, first checkDirective, implicit bool) []string {
	checks := make([]string, 0, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			checks = append(checks, "// CHECK-EMPTY:")
			continue
		}
		if i == 0 && !implicit {
			pattern := first.pattern
			if len(matchingDirectiveLines(first, lines[:1], 0)) == 0 {
				pattern = generalizeIRLine(line, modulePath)
				if strings.HasPrefix(line, "define ") {
					pattern = generalizeDefineLine(line, modulePath)
				}
			}
			checks = append(checks, "// "+first.kind+": "+pattern)
			continue
		}
		checks = append(checks, "// CHECK-NEXT: "+generalizeIRLine(line, modulePath))
	}
	return checks
}

func preserveTrailingNewlines(generated, original string) string {
	want := len(original) - len(strings.TrimRight(original, "\n"))
	return strings.TrimRight(generated, "\n") + strings.Repeat("\n", want)
}

func sourceCheckGroups(src string) []checkGroup {
	lineStarts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	var groups []checkGroup
	for line := 0; line < len(lineStarts); {
		lineEnd := len(src)
		if line+1 < len(lineStarts) {
			lineEnd = lineStarts[line+1]
		}
		if !checkLineRE.MatchString(strings.TrimRight(src[lineStarts[line]:lineEnd], "\r\n")) {
			line++
			continue
		}
		startLine := line
		for line < len(lineStarts) {
			lineEnd = len(src)
			if line+1 < len(lineStarts) {
				lineEnd = lineStarts[line+1]
			}
			if !checkLineRE.MatchString(strings.TrimRight(src[lineStarts[line]:lineEnd], "\r\n")) {
				break
			}
			line++
		}
		blockEnd := line
		groupStart := startLine
		for current := startLine + 1; current < blockEnd; current++ {
			currentEnd := len(src)
			if current+1 < len(lineStarts) {
				currentEnd = lineStarts[current+1]
			}
			match := checkDirectiveRE.FindStringSubmatch(strings.TrimRight(src[lineStarts[current]:currentEnd], "\r\n"))
			if match != nil && match[1] != "CHECK-NEXT" && match[1] != "CHECK-EMPTY" {
				groups = append(groups, checkGroup{
					start: lineStarts[groupStart],
					end:   lineStarts[current],
					text:  src[lineStarts[groupStart]:lineStarts[current]],
				})
				groupStart = current
			}
		}
		end := len(src)
		if blockEnd < len(lineStarts) {
			end = lineStarts[blockEnd]
		}
		groups = append(groups, checkGroup{
			start: lineStarts[groupStart],
			end:   end,
			text:  src[lineStarts[groupStart]:end],
		})
	}
	return groups
}

func indexFunctionChecks(funcs []irFunction, modulePath string) map[string][]*irFunction {
	checks := make(map[string][]*irFunction, len(funcs))
	for i := range funcs {
		if len(funcs[i].lines) == 0 {
			continue
		}
		line := generalizeDefineLine(funcs[i].lines[0], modulePath)
		checks[line] = append(checks[line], &funcs[i])
	}
	return checks
}

func findFunctionForCheckGroup(group string, funcs []irFunction, functionChecks map[string][]*irFunction) (irFunction, bool, error) {
	var definition string
	for _, line := range strings.Split(group, "\n") {
		idx := strings.Index(line, "define ")
		if idx < 0 {
			continue
		}
		definition = line[idx:]
		break
	}
	if definition == "" {
		return irFunction{}, false, nil
	}
	if matched := functionChecks[definition]; len(matched) != 0 {
		if len(matched) != 1 {
			return irFunction{}, false, fmt.Errorf("function CHECK matches both %q and %q", matched[0].symbol, matched[1].symbol)
		}
		return *matched[0], true, nil
	}

	// Hand-written definition checks can intentionally be looser than litgen's
	// output. Retain FileCheck matching as a compatibility fallback for them.
	definitionCheck := "// CHECK: " + definition + "\n"
	var matched *irFunction
	for i := range funcs {
		if len(funcs[i].lines) == 0 || matchCheckText(definitionCheck, strings.Join(funcs[i].lines, "\n")) != nil {
			continue
		}
		if matched != nil {
			return irFunction{}, false, fmt.Errorf("function CHECK matches both %q and %q", matched.symbol, funcs[i].symbol)
		}
		matched = &funcs[i]
	}
	if matched != nil {
		return *matched, true, nil
	}

	// A source or ABI change can alter the signature while the function symbol
	// remains stable. Match just that symbol before giving up on the snapshot.
	symbolPattern, ok := definitionSymbolToken(definition)
	if ok {
		for i := range funcs {
			if len(funcs[i].lines) == 0 {
				continue
			}
			actual, found := definitionSymbolToken(funcs[i].lines[0])
			if !found || matchCheckText("// CHECK: "+symbolPattern+"\n", actual+"\n") != nil {
				continue
			}
			if matched != nil {
				return irFunction{}, false, fmt.Errorf("function symbol CHECK matches both %q and %q", matched.symbol, funcs[i].symbol)
			}
			matched = &funcs[i]
		}
		if matched != nil {
			return *matched, true, nil
		}
	}
	return irFunction{}, false, fmt.Errorf("function CHECK does not match current IR: %s", strings.TrimSpace(definitionCheck))
}

func definitionSymbolToken(definition string) (string, bool) {
	start := strings.IndexByte(definition, '@')
	if start < 0 {
		return "", false
	}
	inQuote := false
	regexDepth := 0
	for i := start + 1; i < len(definition); i++ {
		switch {
		case i+1 < len(definition) && definition[i:i+2] == "{{":
			regexDepth++
			i++
		case regexDepth > 0 && i+1 < len(definition) && definition[i:i+2] == "}}":
			regexDepth--
			i++
		case regexDepth == 0 && definition[i] == '"' && !isEscapedQuote(definition, i):
			inQuote = !inQuote
		case regexDepth == 0 && !inQuote && definition[i] == '(':
			return definition[start:i], true
		}
	}
	return "", false
}

func matchCheckText(checks, ir string) error {
	tmp, err := os.CreateTemp("", "llgo-litgen-check-*.go")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(checks); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return filecheck.Match(path, ir)
}

func resolveTarget(sourceFile, genTarget string) (resolvedTarget, error) {
	pkgDir := filepath.Dir(sourceFile)
	root, goMod, err := mod.FindGoMod(pkgDir)
	if err != nil {
		return resolvedTarget{}, err
	}
	modulePath, err := readModulePath(goMod)
	if err != nil {
		return resolvedTarget{}, err
	}
	pkgPath, err := packagePath(modulePath, root, pkgDir)
	if err != nil {
		return resolvedTarget{}, err
	}
	return resolvedTarget{
		sourceFile: sourceFile,
		genTarget:  genTarget,
		pkgDir:     pkgDir,
		modulePath: modulePath,
		pkgPath:    pkgPath,
	}, nil
}

func genIR(target string) (ret string, err error) {
	setupLLGoRoot()
	defer func() {
		if r := recover(); r != nil {
			switch v := r.(type) {
			case error:
				err = fmt.Errorf("llgen failed for %s: %w", target, v)
			case string:
				err = fmt.Errorf("llgen failed for %s: %s", target, v)
			default:
				_, _ = os.Stderr.Write(debug.Stack())
				panic(r)
			}
		}
	}()
	return llgen.GenFrom(target), nil
}

func readModulePath(goMod string) (string, error) {
	data, err := os.ReadFile(goMod)
	if err != nil {
		return "", err
	}
	modulePath := modfile.ModulePath(data)
	if modulePath == "" {
		return "", fmt.Errorf("%s: module directive not found", goMod)
	}
	return modulePath, nil
}

func packagePath(modulePath, root, pkgDir string) (string, error) {
	rel, err := filepath.Rel(root, pkgDir)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return modulePath, nil
	}
	return path.Join(modulePath, filepath.ToSlash(rel)), nil
}

func rewriteSource(src, srcPath, pkgPath, modulePath, ir string) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, srcPath, src, parser.ParseComments)
	if err != nil {
		return "", err
	}
	prog := parseIR(ir)
	anchors, topPos := collectAnchors(src, fset, file)
	injections := make(map[int][]string)

	if globals := buildGlobalChecks(prog, modulePath); len(globals) != 0 {
		injections[topPos] = append(injections[topPos], formatDirectiveBlock(indentAt(src, topPos), globals))
	}
	eof := len(src)
	lastOffset := topPos
	for _, fn := range prog.funcs {
		if shouldSkipFunctionCheck(fn.symbol) {
			continue
		}
		lines := buildFunctionChecks(fn, modulePath)
		if len(lines) == 0 {
			continue
		}
		offset := eof
		if name, ok := trimPkgPrefix(fn.symbol, pkgPath); ok {
			if pos, found := anchors[name]; found {
				offset = pos
			}
		}
		if offset < lastOffset {
			offset = lastOffset
		}
		injections[offset] = append(injections[offset], formatDirectiveBlock(indentAt(src, offset), lines))
		lastOffset = offset
	}
	return applyInjections(src, injections), nil
}

func collectAnchors(src string, fset *token.FileSet, file *ast.File) (map[string]int, int) {
	anchors := make(map[string]int)
	counts := make(map[string]int)
	topPos := topInsertPos(src, fset, file)
	if initPos, ok := syntheticInitPos(src, fset, file); ok {
		anchors["init"] = initPos
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name := inPkgFuncName(d)
			anchors[name] = declInsertPos(src, fset, d.Pos(), d.Doc)
			collectFuncLitAnchors(src, fset, d.Body, name, anchors, counts, declDocAnchors(src, fset, d))
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			collectFuncLitAnchors(src, fset, d, "init", anchors, counts, declDocAnchors(src, fset, d))
		}
	}
	return anchors, topPos
}

func topInsertPos(src string, fset *token.FileSet, file *ast.File) int {
	for _, decl := range file.Decls {
		// Insert before declaration docs so compiler directives stay attached.
		switch d := decl.(type) {
		case *ast.FuncDecl:
			return declInsertPos(src, fset, d.Pos(), d.Doc)
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			return declInsertPos(src, fset, d.Pos(), d.Doc)
		}
	}
	return len(src)
}

func syntheticInitPos(src string, fset *token.FileSet, file *ast.File) (int, bool) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == "init" {
				return declInsertPos(src, fset, d.Pos(), d.Doc), true
			}
		case *ast.GenDecl:
			if d.Tok != token.IMPORT {
				return declInsertPos(src, fset, d.Pos(), d.Doc), true
			}
		}
	}
	return 0, false
}

func declInsertPos(src string, fset *token.FileSet, pos token.Pos, doc *ast.CommentGroup) int {
	if doc != nil {
		pos = doc.Pos()
	}
	return lineStart(src, offsetOf(fset, pos))
}

func declDocAnchors(src string, fset *token.FileSet, decl ast.Decl) map[int]int {
	var anchors map[int]int
	add := func(pos token.Pos, doc *ast.CommentGroup) {
		if doc == nil {
			return
		}
		if anchors == nil {
			anchors = make(map[int]int)
		}
		anchors[lineStart(src, offsetOf(fset, pos))] = declInsertPos(src, fset, pos, doc)
	}
	switch d := decl.(type) {
	case *ast.FuncDecl:
		add(d.Pos(), d.Doc)
	case *ast.GenDecl:
		add(d.Pos(), d.Doc)
		for _, spec := range d.Specs {
			if s, ok := spec.(*ast.ValueSpec); ok {
				add(s.Pos(), s.Doc)
			}
		}
	}
	return anchors
}

func collectFuncLitAnchors(src string, fset *token.FileSet, node ast.Node, parent string, anchors map[string]int, counts map[string]int, docAnchors map[int]int) {
	if isNilNode(node) {
		return
	}
	var walk func(ast.Node, string)
	walk = func(root ast.Node, current string) {
		if isNilNode(root) {
			return
		}
		ast.Inspect(root, func(n ast.Node) bool {
			lit, ok := n.(*ast.FuncLit)
			if !ok {
				return true
			}
			counts[current]++
			name := fmt.Sprintf("%s$%d", current, counts[current])
			pos := lineStart(src, offsetOf(fset, lit.Pos()))
			// Keep declaration docs attached when a closure shares the declaration line.
			if docPos, ok := docAnchors[pos]; ok {
				pos = docPos
			}
			anchors[name] = pos
			walk(lit.Body, name)
			return false
		})
	}
	walk(node, parent)
}

func isNilNode(node ast.Node) bool {
	if node == nil {
		return true
	}
	v := reflect.ValueOf(node)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func inPkgFuncName(fn *ast.FuncDecl) string {
	name := fn.Name.Name
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		return "(*" + recvTypeName(star.X) + ")." + name
	}
	return recvTypeName(recv) + "." + name
}

func recvTypeName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return recvTypeName(v.X) + "." + v.Sel.Name
	case *ast.IndexExpr:
		return recvTypeName(v.X)
	case *ast.IndexListExpr:
		return recvTypeName(v.X)
	default:
		return ""
	}
}

func parseIR(ir string) irProgram {
	lines := splitIRLines(ir)
	var prog irProgram
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "define ") {
			j := i + 1
			for j < len(lines) {
				if lines[j] == "}" {
					j++
					break
				}
				j++
			}
			block := append([]string(nil), lines[i:j]...)
			prog.funcs = append(prog.funcs, irFunction{
				symbol: extractDefineSymbol(line),
				lines:  block,
			})
			i = j - 1
			continue
		}
		if symbol, ok := extractGlobalSymbol(line); ok {
			prog.globals = append(prog.globals, irGlobal{symbol: symbol, line: line})
		}
	}
	return prog
}

func splitIRLines(ir string) []string {
	ir = strings.ReplaceAll(ir, "\r\n", "\n")
	ir = strings.TrimSuffix(ir, "\n")
	if ir == "" {
		return nil
	}
	return strings.Split(ir, "\n")
}

func extractDefineSymbol(line string) string {
	if m := defineQuotedRE.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	if m := definePlainRE.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

func extractGlobalSymbol(line string) (string, bool) {
	if m := globalQuotedRE.FindStringSubmatch(line); m != nil {
		return m[1], true
	}
	if m := globalPlainRE.FindStringSubmatch(line); m != nil {
		return m[1], true
	}
	return "", false
}

func buildGlobalChecks(prog irProgram, modulePath string) []string {
	defs := make(map[string]string, len(prog.globals))
	order := make([]string, 0, len(prog.globals))
	for _, g := range prog.globals {
		if !shouldCheckGlobal(g.symbol) {
			continue
		}
		defs[g.symbol] = g.line
		order = append(order, g.symbol)
	}
	if len(defs) == 0 {
		return nil
	}
	needed := make(map[string]bool)
	for _, fn := range prog.funcs {
		if shouldSkipFunctionCheck(fn.symbol) {
			continue
		}
		for _, line := range fn.lines[1:] {
			for _, ref := range collectRefs(line) {
				if _, ok := defs[ref]; ok {
					needed[ref] = true
				}
			}
		}
	}
	var lines []string
	for _, symbol := range order {
		if !needed[symbol] {
			continue
		}
		lines = append(lines, "// CHECK: {{^}}"+generalizeIRLine(defs[symbol], modulePath)+"{{$}}")
	}
	return lines
}

func buildFunctionChecks(fn irFunction, modulePath string) []string {
	if len(fn.lines) == 0 {
		return nil
	}
	lines := make([]string, 0, len(fn.lines))
	lines = append(lines, "// CHECK-LABEL: "+generalizeDefineLine(fn.lines[0], modulePath))
	for _, line := range fn.lines[1:] {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "// CHECK-EMPTY:")
			continue
		}
		lines = append(lines, "// CHECK-NEXT: "+generalizeIRLine(line, modulePath))
	}
	return lines
}

func generalizeDefineLine(line, modulePath string) string {
	line = scrubIRLine(line)
	if idx := strings.LastIndex(line, " {"); idx >= 0 {
		head := line[:idx]
		if sigEnd := strings.LastIndex(head, ")"); sigEnd >= 0 {
			line = head[:sigEnd+1] + "{{.*}}" + line[idx:]
		}
	}
	return generalizeSymbolPaths(line, modulePath)
}

func generalizeIRLine(line, modulePath string) string {
	return generalizeSymbolPaths(scrubIRLine(line), modulePath)
}

func scrubIRLine(line string) string {
	// Escape source IR syntax before adding FileCheck regexes below.
	line = strings.ReplaceAll(line, "[[", `{{\[\[}}`)
	line = debugMetaRE.ReplaceAllString(line, "")
	line = generalizeClosureEnvAttrs(line)
	line = symbolHashRE.ReplaceAllString(line, `$${{[-A-Za-z0-9_]+}}`)
	line = cgoHashRE.ReplaceAllString(line, `${1}{{[0-9a-f]+}}${2}`)
	line = numericGlobalRE.ReplaceAllString(line, `@{{[0-9]+}}`)
	line = metadataIDRE.ReplaceAllString(line, `!{{[0-9]+}}`)
	line = generalizePlatformIR(line)
	return strings.TrimRight(line, " \t")
}

func generalizeSymbolPaths(line, modulePath string) string {
	line = testCasePathRE.ReplaceAllString(line, `"{{.*}}`)
	return generalizeModulePath(line, modulePath)
}

func generalizePlatformIR(line string) string {
	line = sigJumpRE.ReplaceAllString(line, `@{{(__)?}}${1}`)
	line = plainJumpRE.ReplaceAllString(line, `@{{_*}}${1}`)
	line = jmpBufAllocaRE.ReplaceAllString(line, `alloca i8, i64 {{(196|200)}}, align 1`)
	if match := pthreadTypeRE.FindStringSubmatch(line); match != nil {
		if opaque, ok := pthreadOpaqueSizes[match[1]]; ok {
			return opaque.sizes.ReplaceAllString(line, opaque.want)
		}
	}
	return line
}

func generalizeClosureEnvAttrs(line string) string {
	var b strings.Builder
	start := 0
	inQuote := false
	for i := 0; i < len(line); i++ {
		if line[i] != '"' || isEscapedQuote(line, i) {
			continue
		}
		if !inQuote {
			b.WriteString(closureEnvRE.ReplaceAllString(line[start:i], `${1}{{(nest|swiftself)}}${2}`))
			b.WriteByte('"')
			start = i + 1
			inQuote = true
			continue
		}
		b.WriteString(line[start : i+1])
		start = i + 1
		inQuote = false
	}
	if inQuote {
		b.WriteString(line[start:])
	} else {
		b.WriteString(closureEnvRE.ReplaceAllString(line[start:], `${1}{{(nest|swiftself)}}${2}`))
	}
	return b.String()
}

func generalizeModulePath(line, modulePath string) string {
	if modulePath == "" {
		return line
	}
	var b strings.Builder
	start := 0
	inQuote := false
	for i := 0; i < len(line); i++ {
		if line[i] != '"' || isEscapedQuote(line, i) {
			continue
		}
		if !inQuote {
			b.WriteString(line[start : i+1])
			start = i + 1
			inQuote = true
			continue
		}
		b.WriteString(strings.ReplaceAll(line[start:i], modulePath, "{{.*}}"))
		b.WriteByte('"')
		start = i + 1
		inQuote = false
	}
	b.WriteString(line[start:])
	return b.String()
}

func isEscapedQuote(line string, idx int) bool {
	backslashes := 0
	for i := idx - 1; i >= 0 && line[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func shouldCheckGlobal(symbol string) bool {
	return numericNameRE.MatchString(symbol)
}

func collectRefs(line string) []string {
	matches := globalRefRE.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return nil
	}
	refs := make([]string, 0, len(matches))
	for _, m := range matches {
		if m[1] != "" {
			refs = append(refs, m[1])
			continue
		}
		if m[2] != "" {
			refs = append(refs, m[2])
		}
	}
	return refs
}

func shouldSkipFunctionCheck(symbol string) bool {
	return strings.HasSuffix(symbol, "/runtime/internal/runtime.memequal32") ||
		strings.HasSuffix(symbol, "/runtime/internal/runtime.memequalptr") ||
		strings.HasSuffix(symbol, "/runtime/internal/runtime.strequal")
}

func trimPkgPrefix(symbol, pkgPath string) (string, bool) {
	prefix := pkgPath + "."
	if strings.HasPrefix(symbol, prefix) {
		return strings.TrimPrefix(symbol, prefix), true
	}
	return "", false
}

func applyInjections(src string, injections map[int][]string) string {
	if len(injections) == 0 {
		return src
	}
	offsets := make([]int, 0, len(injections))
	for offset := range injections {
		offsets = append(offsets, offset)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(offsets)))
	out := src
	for _, offset := range offsets {
		block := strings.Join(injections[offset], "")
		out = out[:offset] + block + out[offset:]
	}
	return out
}

func formatDirectiveBlock(indent string, lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(indent)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.String()
}

func stripCheckDirectives(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.SplitAfter(src, "\n")
	if len(lines) == 0 {
		return src
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\n")
		if checkLineRE.MatchString(trimmed) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "")
}

func lineStart(src string, offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > len(src) {
		return len(src)
	}
	for offset > 0 && src[offset-1] != '\n' {
		offset--
	}
	return offset
}

func indentAt(src string, offset int) string {
	if offset >= len(src) {
		return ""
	}
	start := lineStart(src, offset)
	end := start
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	return src[start:end]
}

func offsetOf(fset *token.FileSet, pos token.Pos) int {
	return fset.PositionFor(pos, false).Offset
}

func writeFileAtomically(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".litgen-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
