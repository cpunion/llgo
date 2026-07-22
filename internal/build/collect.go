/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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

package build

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/env"
	"github.com/goplus/llgo/internal/packages"
	intllvm "github.com/goplus/llgo/internal/xtool/llvm"
	gopackages "golang.org/x/tools/go/packages"
)

// collectFingerprint collects all inputs and generates fingerprint for a package.
func (c *context) collectFingerprint(pkg *aPackage) error {
	if pkg.Manifest != "" && pkg.Fingerprint != "" {
		return nil
	}
	if c.fingerprinting == nil {
		c.fingerprinting = make(map[string]bool)
	}
	if c.fingerprinting[pkg.ID] {
		return fmt.Errorf("fingerprint cycle detected for %s", pkg.ID)
	}
	c.fingerprinting[pkg.ID] = true
	defer delete(c.fingerprinting, pkg.ID)

	m := newManifestBuilder()

	// Env section
	c.collectEnvInputs(m)

	// Common section
	c.collectCommonInputs(m)

	// Package section
	if err := c.collectPackageInputs(m, pkg); err != nil {
		return err
	}

	// Dependency section
	if err := c.collectDependencyInputs(m, pkg); err != nil {
		return err
	}

	pkg.Manifest = m.Build()
	pkg.Fingerprint = m.Fingerprint()
	return nil
}

// collectEnvInputs collects environment-related inputs.
func (c *context) collectEnvInputs(m *manifestBuilder) {
	m.env.Goos = c.buildConf.Goos
	m.env.Goarch = c.buildConf.Goarch
	if c.hasNonDefaultLLVMConfig() {
		m.env.LlvmTriple = c.crossCompile.LLVMTarget
	}
	m.env.LlgoVersion = env.Version()
	m.env.LlgoCompilerHash = c.buildConf.CompilerHash
	m.env.GoVersion = runtime.Version()
	m.env.LlvmVersion = c.getLLVMVersion()

	// Environment variables that affect build
	envVars := []string{
		llgoDebug,
		llgoDbgSyms,
		llgoFuncInfo,
		llgoTrace,
		llgoOptimize,
		llgoWasmRuntime,
		llgoWasiThreads,
		llgoStdioNobuf,
		llgoFullRpath,
	}
	for _, envVar := range envVars {
		if v := os.Getenv(envVar); v != "" {
			m.env.Vars = m.env.Vars.Add(envVar, v)
		}
	}
}

// collectCommonInputs collects common build configuration inputs.
func (c *context) collectCommonInputs(m *manifestBuilder) {
	m.common.AbiMode = fmt.Sprintf("%d", c.buildConf.AbiMode)
	if c.buildConf.Tags != "" {
		m.common.BuildTags = strings.Split(c.buildConf.Tags, ",")
	}
	m.common.Target = c.buildConf.Target
	m.common.RuntimeGC = c.crossCompile.GC
	if c.hasNonDefaultLLVMConfig() {
		m.common.LLVMCPU = c.crossCompile.CPU
		m.common.LLVMFeatures = c.crossCompile.Features
	}
	m.common.TargetABI = c.crossCompile.TargetABI
	m.common.GoGlobalDCE = c.buildConf.goGlobalDCEEnabled()
	if c.coroPlanDigest != "" {
		metadata := c.coroPlanMetadata
		m.common.CoroPlanDigest = c.coroPlanDigest
		m.common.CoroABI = metadata.CoroABI
		m.common.CoroSchedulerABI = metadata.SchedulerABI
		m.common.CoroPanicABI = metadata.PanicABI
		m.common.CoroFuncRepABI = metadata.FuncRepABI
		m.common.CoroFrameRetentionABI = metadata.FrameRetentionABI
		m.common.CoroLoweringFactsSchema = metadata.LoweringFactsSchema
		m.common.CoroLoweringFactsDigest = metadata.LoweringFactsDigest
		m.common.CoroTargetTriple = metadata.TargetTriple
		m.common.CoroTargetCPU = metadata.TargetCPU
		m.common.CoroTargetFeatures = metadata.TargetFeatures
		m.common.CoroTargetABI = metadata.TargetABI
		m.common.CoroPointerBits = metadata.PointerBits
		m.common.CoroEndianness = metadata.Endianness
		m.common.CoroDataLayout = metadata.DataLayout
	}

	// Compiler configuration
	if c.crossCompile.CC != "" {
		m.common.CC = c.crossCompile.CC
	}
	if len(c.crossCompile.CCFLAGS) > 0 {
		m.common.CCFlags = append([]string(nil), c.crossCompile.CCFLAGS...)
	}
	if len(c.crossCompile.CFLAGS) > 0 {
		m.common.CFlags = append([]string(nil), c.crossCompile.CFLAGS...)
	}
	if len(c.crossCompile.LDFLAGS) > 0 {
		m.common.LDFlags = append([]string(nil), c.crossCompile.LDFLAGS...)
	}
	if c.crossCompile.Linker != "" {
		m.common.Linker = c.crossCompile.Linker
	}

	// Extra files from target configuration
	if len(c.crossCompile.ExtraFiles) > 0 {
		extraList, err := digestFiles(c.crossCompile.ExtraFiles)
		if err == nil && len(extraList) > 0 {
			m.common.ExtraFiles = extraList
		}
	}
}

// collectPackageInputs collects package-specific inputs.
func (c *context) collectPackageInputs(m *manifestBuilder, pkg *aPackage) error {
	p := pkg.Package

	m.pkg.PkgPath = p.PkgPath
	m.pkg.PkgID = p.ID

	// Go source files
	goFilesList, err := digestFilesWithOverlay(p.GoFiles, c.conf.Overlay)
	if err != nil {
		return fmt.Errorf("digest go files: %w", err)
	}
	m.pkg.GoFiles = goFilesList

	// Alt package files (if any)
	if pkg.AltPkg != nil {
		altList, err := digestFilesWithOverlay(pkg.AltPkg.GoFiles, c.conf.Overlay)
		if err != nil {
			return fmt.Errorf("digest alt go files: %w", err)
		}
		m.pkg.AltGoFiles = altList
	}

	// Other files (C, assembly, etc.)
	otherFiles := append([]string{}, p.OtherFiles...)
	sfiles, err := pkgSFiles(c, p)
	if err != nil {
		return fmt.Errorf("list sfiles: %w", err)
	}
	otherFiles = append(otherFiles, sfiles...)
	if len(otherFiles) > 0 {
		otherList, err := digestFilesWithOverlay(otherFiles, c.conf.Overlay)
		if err != nil {
			return fmt.Errorf("digest other files: %w", err)
		}
		m.pkg.OtherFiles = otherList
	}

	// Rewrite vars
	if len(pkg.rewriteVars) > 0 {
		rewrites := make(map[string]string, len(pkg.rewriteVars))
		for k, v := range pkg.rewriteVars {
			rewrites[k] = v
		}
		m.pkg.RewriteVars = m.pkg.RewriteVars.AddMap(rewrites)
	}

	// Add metadata fields if available (for cache saving)
	// (LINK_ARGS/NEED_RT/NEED_PY_INIT are appended later in saveToCache)

	return nil
}

// collectDependencyInputs adds dependency fingerprints/versions into manifest.
func (c *context) collectDependencyInputs(m *manifestBuilder, pkg *aPackage) error {
	if len(pkg.Imports) == 0 {
		return nil
	}

	deps := make([]*packages.Package, 0, len(pkg.Imports))
	for _, dep := range pkg.Imports {
		if dep == nil || dep.ID == pkg.ID {
			continue
		}
		deps = append(deps, dep)
	}

	sort.Slice(deps, func(i, j int) bool { return deps[i].ID < deps[j].ID })

	for _, dep := range deps {
		depEntry, err := c.dependencyFingerprint(dep)
		if err != nil {
			return err
		}
		m.deps = append(m.deps, depEntry)
	}

	return nil
}

func (c *context) dependencyFingerprint(dep *packages.Package) (depEntry, error) {
	entry := depEntry{ID: dep.ID}
	if v := moduleVersion(dep.Module); v != "" {
		entry.Version = v
		return entry, nil
	}

	if c.pkgByID != nil {
		if aDep, ok := c.pkgByID[dep.ID]; ok {
			if aDep.Fingerprint == "" {
				if err := c.collectFingerprint(aDep); err != nil {
					return entry, fmt.Errorf("collect fingerprint for %s: %w", dep.ID, err)
				}
			}
			entry.Fingerprint = aDep.Fingerprint
			return entry, nil
		}
	}

	temp := &aPackage{Package: dep}
	if err := c.collectFingerprint(temp); err != nil {
		return entry, fmt.Errorf("collect fingerprint for %s: %w", dep.ID, err)
	}
	entry.Fingerprint = temp.Fingerprint
	return entry, nil
}

func moduleVersion(mod *gopackages.Module) string {
	if mod == nil {
		return ""
	}
	if mod.Replace != nil {
		// replace to local path (Version empty) should not use version for fingerprint
		if mod.Replace.Version != "" {
			return mod.Replace.Version
		}
		return ""
	}
	return mod.Version
}

// getLLVMVersion returns the cached LLVM version or detects it.
func (c *context) getLLVMVersion() string {
	if c.llvmVersion != "" {
		return c.llvmVersion
	}
	c.llvmVersion = detectLLVMVersion(c)
	return c.llvmVersion
}

// detectLLVMVersion detects LLVM version from clang --version.
func detectLLVMVersion(ctx *context) string {
	// Get compiler path from cross compile config
	cc := ctx.crossCompile.CC
	if cc == "" {
		cc = "clang"
	}
	versionCmd := exec.Command(cc, "--version")
	output, err := versionCmd.Output()
	if err != nil {
		return ""
	}
	line := string(output)
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	return strings.TrimSpace(line)
}

// targetTriple returns the target triple for cache directory.
func (c *context) targetTriple() string {
	llvmTarget := c.crossCompile.LLVMTarget
	if !c.hasNonDefaultLLVMConfig() {
		// Preserve the legacy cache namespace for ordinary GOOS/GOARCH builds.
		// Their resolved LLVM defaults are deterministic inputs of the compiler
		// version, while named targets need their explicit triple and ABI here.
		llvmTarget = ""
	}
	return targetTriple(
		c.buildConf.Goos,
		c.buildConf.Goarch,
		llvmTarget,
		c.crossCompile.TargetABI,
	)
}

func (c *context) hasNonDefaultLLVMConfig() bool {
	if c.buildConf.Target != "" {
		return true
	}
	requested := c.crossCompile
	if requested.LLVMTarget == "" && requested.CPU == "" && requested.Features == "" && requested.TargetABI == "" {
		return false
	}
	defaults := intllvm.GetTargetSpec(c.buildConf.Goos, c.buildConf.Goarch, "")
	return requested.LLVMTarget != defaults.Triple || requested.CPU != defaults.CPU ||
		requested.Features != defaults.Features || requested.TargetABI != ""
}

// targetTriple returns the target triple string for cache directory
func targetTriple(goos, goarch, llvmTarget, targetABI string) string {
	triple := llvmTarget
	if triple == "" {
		triple = fmt.Sprintf("%s-%s", goarch, goos)
	}
	if targetABI != "" {
		triple = triple + "-" + targetABI
	}
	return triple
}

// ensureCacheManager creates cacheManager if not exists.
func (c *context) ensureCacheManager() *cacheManager {
	if c.cacheManager == nil {
		c.cacheManager = newCacheManager()
	}
	return c.cacheManager
}

// canUsePackageCache reports whether the current compilation's emitted IR is
// fully represented by the package fingerprint. Active coroutine lowering is
// fail-closed until a complete plan/ABI/target record has been installed.
func (c *context) canUsePackageCache() bool {
	if c.buildConf == nil || !c.buildConf.EnableCoroEntryResolution {
		return true
	}
	if c.clCompilation == nil || c.coroPlan == nil || c.clCompilation.CoroPlan != c.coroPlan ||
		c.coroEmission == nil || c.clCompilation.EmissionUniverse != c.coroEmission || c.coroPlanDigest == "" ||
		c.clCompilation.CoroPlanDigest != c.coroPlanDigest {
		return false
	}
	decoded, err := hex.DecodeString(c.coroPlanDigest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != c.coroPlanDigest {
		return false
	}
	metadata := c.coroPlanMetadata
	return c.clCompilation.EnableCoroEntryResolution &&
		c.clCompilation.EnableCoroPhysicalABI == c.buildConf.EnableCoroPhysicalABI &&
		c.clCompilation.EnableCoroChildAwait == c.buildConf.EnableCoroChildAwait &&
		c.clCompilation.EnableCoroPlainDispatch == c.buildConf.EnableCoroPlainDispatch &&
		c.clCompilation.EnableCoroExplicitStatusPanicABI == c.buildConf.EnableCoroExplicitStatusPanicABI &&
		c.clCompilation.EnableCoroClosedStaticSpawn == c.buildConf.EnableCoroClosedStaticSpawn &&
		c.clCompilation.EnableCoroProgramBootstrapRun == c.buildConf.EnableCoroProgramBootstrapRun &&
		c.clCompilation.EnableCoroChannel == c.buildConf.EnableCoroChannel &&
		c.clCompilation.EnableCoroWorker == c.buildConf.EnableCoroWorker &&
		c.clCompilation.CoroABI == metadata.CoroABI &&
		c.clCompilation.SchedulerABI == metadata.SchedulerABI &&
		c.clCompilation.PanicABI == metadata.PanicABI &&
		c.clCompilation.FuncRepABI == metadata.FuncRepABI &&
		c.clCompilation.CoroFrameRetentionABI == metadata.FrameRetentionABI &&
		c.clCompilation.CoroLoweringFacts.Schema == metadata.LoweringFactsSchema &&
		c.clCompilation.CoroLoweringFactsDigest == metadata.LoweringFactsDigest &&
		c.coroLoweringFacts.Schema == metadata.LoweringFactsSchema &&
		c.coroLoweringFactsDigest == metadata.LoweringFactsDigest &&
		metadata.CoroABI == activeCoroABIVersion(c.buildConf) &&
		metadata.SchedulerABI == activeCoroSchedulerABIVersion(c.buildConf) &&
		metadata.PanicABI == activeCoroPanicABIVersion(c.buildConf) &&
		metadata.FuncRepABI == activeCoroFuncRepABIVersion(c.buildConf) &&
		metadata.LoweringFactsSchema == coro.LoweringFactsSchema && metadata.LoweringFactsDigest != "" &&
		metadata.TargetTriple != "" && metadata.PointerBits > 0 &&
		(metadata.Endianness == "little" || metadata.Endianness == "big") &&
		metadata.DataLayout != ""
}

func activeCoroCacheManifestMatches(content string, pkg *aPackage) bool {
	if pkg == nil || pkg.Manifest == "" {
		return false
	}
	actual, err := decodeManifest(content)
	if err != nil {
		return false
	}
	expected, err := decodeManifest(pkg.Manifest)
	if err != nil {
		return false
	}
	actual.Metadata = nil
	expected.Metadata = nil
	actualText, err := buildManifestYAML(actual)
	if err != nil {
		return false
	}
	expectedText, err := buildManifestYAML(expected)
	return err == nil && actualText == expectedText && digestBytes([]byte(expectedText)) == pkg.Fingerprint
}

// tryLoadFromCache attempts to load a package from cache.
// Returns true if cache hit, false otherwise.
func (c *context) tryLoadFromCache(pkg *aPackage) bool {
	if !c.canUsePackageCache() || !cacheEnabled() {
		return false
	}

	// Main packages are intentionally not written to the build cache because
	// each executable's entry module is linked against the current main archive.
	// Do not load stale main archives that may exist from older cache layouts.
	if pkg.Name == "main" {
		return false
	}

	// Skip cache when force rebuild is enabled
	if c.buildConf.ForceRebuild {
		return false
	}

	if pkg.Fingerprint == "" {
		return false
	}

	cm := c.ensureCacheManager()
	paths := cm.PackagePaths(c.targetTriple(), pkg.PkgPath, pkg.Fingerprint)

	// Check if archive file exists
	if _, err := os.Stat(paths.Archive); err != nil {
		return false
	}

	// Read metadata from manifest
	content, err := readManifest(paths.Manifest)
	if err != nil {
		return false
	}
	if c.buildConf != nil && c.buildConf.EnableCoroEntryResolution && !activeCoroCacheManifestMatches(content, pkg) {
		return false
	}

	// Parse metadata from manifest [Package] section (INI format)
	meta, err := parseManifestMetadata(content)
	if err != nil {
		return false
	}

	// Use the .a archive directly for linking (no extraction needed)
	pkg.ArchiveFile = paths.Archive
	pkg.LinkArgs = meta.LinkArgs
	pkg.NeedRt = meta.NeedRt
	pkg.NeedPyInit = meta.NeedPyInit
	pkg.CoroRootAnchorV1 = meta.CoroRootAnchorV1
	pkg.CacheHit = true

	return true
}

// parseManifestMetadata extracts metadata from manifest content.
// It supports the new YAML format and falls back to the legacy INI layout for
// backward compatibility with existing cache entries.
func parseManifestMetadata(content string) (*cacheArchiveMetadata, error) {
	meta := &cacheArchiveMetadata{}
	if data, err := decodeManifest(content); err == nil {
		if data.Metadata != nil {
			meta.LinkArgs = append([]string(nil), data.Metadata.LinkArgs...)
			meta.NeedRt = data.Metadata.NeedRt
			meta.NeedPyInit = data.Metadata.NeedPyInit
			meta.CoroRootAnchorV1 = data.Metadata.CoroRootAnchorV1
		}
		return meta, nil
	}

	return parseManifestMetadataLegacy(content, meta)
}

func parseManifestMetadataLegacy(content string, meta *cacheArchiveMetadata) (*cacheArchiveMetadata, error) {
	// Find Package section
	idx := strings.Index(content, "[Package]\n")
	if idx == -1 {
		return meta, nil
	}

	// Extract Package section content (until next section or end)
	pkgSection := content[idx+len("[Package]\n"):]
	if nextIdx := strings.Index(pkgSection, "\n["); nextIdx != -1 {
		pkgSection = pkgSection[:nextIdx]
	}

	// Parse key-value pairs in Package section
	lines := strings.Split(pkgSection, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " = ", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

		switch key {
		case "LINK_ARGS":
			if value != "" {
				meta.LinkArgs = strings.Fields(value)
			}
		case "NEED_RT":
			meta.NeedRt = value == "true"
		case "NEED_PY_INIT":
			meta.NeedPyInit = value == "true"
		case "CORO_ROOT_ANCHOR_V1":
			meta.CoroRootAnchorV1 = value
		}
	}

	return meta, nil
}

// cacheArchiveMetadata holds metadata about a cached archive.
type cacheArchiveMetadata struct {
	LinkArgs         []string
	NeedRt           bool
	NeedPyInit       bool
	CoroRootAnchorV1 string
}

// saveToCache saves a built package to cache.
func (c *context) saveToCache(pkg *aPackage) error {
	if !c.canUsePackageCache() || !cacheEnabled() {
		return nil
	}

	if pkg.Fingerprint == "" || pkg.Manifest == "" {
		return nil
	}

	// Don't cache main packages
	if pkg.Name == "main" {
		return nil
	}

	cm := c.ensureCacheManager()
	paths := cm.PackagePaths(c.targetTriple(), pkg.PkgPath, pkg.Fingerprint)

	// Ensure directory exists
	if err := cm.EnsureDir(paths); err != nil {
		return err
	}

	// If ArchiveFile is already set (from normalizeToArchive), copy it to cache
	if pkg.ArchiveFile != "" {
		if err := copyFileAtomic(pkg.ArchiveFile, paths.Archive); err != nil {
			return err
		}
	} else if len(pkg.ObjFiles) > 0 {
		// Otherwise, create archive from object files
		if err := c.createArchiveFile(paths.Archive, pkg.ObjFiles); err != nil {
			return err
		}
	} else {
		return nil
	}

	// Append metadata to existing manifest (pkg.Manifest was built in collectFingerprint).
	manifestContent := pkg.Manifest
	if manifestContent == "" {
		return fmt.Errorf("package %s missing manifest for fingerprint %s", pkg.PkgPath, pkg.Fingerprint)
	}

	data, err := decodeManifest(manifestContent)
	if err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}

	meta := &manifestMetadata{
		LinkArgs:         append([]string(nil), pkg.LinkArgs...),
		NeedRt:           pkg.NeedRt,
		NeedPyInit:       pkg.NeedPyInit,
		CoroRootAnchorV1: pkg.CoroRootAnchorV1,
	}
	if len(meta.LinkArgs) == 0 && !meta.NeedRt && !meta.NeedPyInit && meta.CoroRootAnchorV1 == "" {
		data.Metadata = nil
	} else {
		data.Metadata = meta
	}

	manifestWithMeta, err := buildManifestYAML(data)
	if err != nil {
		return err
	}

	// Write manifest with metadata
	if err := writeManifest(paths.Manifest, manifestWithMeta); err != nil {
		return err
	}

	return nil
}

// copyFileAtomic copies src to dst using a temp file for atomicity.
func copyFileAtomic(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, in); err != nil {
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}

	cleanup = false
	return nil
}
