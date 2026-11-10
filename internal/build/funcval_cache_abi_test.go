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

package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/packages"
)

func TestFuncvalABICacheIdentityWithEmptyCompilerHash(t *testing.T) {
	t.Setenv(llgoBuildCache, "1")
	cm := &cacheManager{root: t.TempDir()}
	const pkgPath = "example.invalid/funcvalcache"
	inputs := func() (*context, *manifestBuilder) {
		ctx := &context{
			buildConf:    &Config{Goos: "linux", Goarch: "amd64"},
			llvmVersion:  "test",
			goVersion:    "go1.27.3",
			cacheManager: cm,
		}
		m := newManifestBuilder()
		ctx.collectEnvInputs(m)
		ctx.collectCommonInputs(m)
		m.pkg.PkgPath = pkgPath
		if ctx.buildConf.CompilerHash != "" || m.env.LlgoCompilerHash != "" {
			t.Fatal("regression must exercise the API's empty compiler hash")
		}
		return ctx, m
	}
	newPackage := func(m *manifestBuilder) *aPackage {
		return &aPackage{
			Package: &packages.Package{
				ID: pkgPath, PkgPath: pkgPath, Name: "funcvalcache",
			},
			Manifest: m.Build(), Fingerprint: m.Fingerprint(),
		}
	}

	ctx, current := inputs()
	manifest := current.Build()
	// Simulate the legacy two-word identity by removing only the ABI field,
	// keeping source, environment, compiler hash, and other common inputs equal.
	legacyLines := make([]string, 0)
	abiFields := 0
	for _, line := range strings.Split(manifest, "\n") {
		if strings.TrimSpace(line) == "GO_FUNC_VALUE_ABI: descriptor-v1" {
			abiFields++
			continue
		}
		legacyLines = append(legacyLines, line)
	}
	if abiFields != 1 {
		t.Errorf("collected common inputs have %d descriptor ABI fields, want 1:\n%s", abiFields, manifest)
	}
	legacyManifest := strings.Join(legacyLines, "\n")

	t.Run("rejects-old-two-word-archive", func(t *testing.T) {
		paths := cm.PackagePaths(ctx.targetTriple(), pkgPath, digestBytes([]byte(legacyManifest)))
		if err := cm.EnsureDir(paths); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.Archive, []byte("old two-word archive"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := writeManifest(paths.Manifest, legacyManifest); err != nil {
			t.Fatal(err)
		}
		pkg := newPackage(current)
		if ctx.tryLoadFromCache(pkg) || pkg.CacheHit || pkg.ArchiveFile != "" {
			t.Fatalf("empty compiler hash reused a pre-descriptor archive: %q", pkg.ArchiveFile)
		}
		if _, err := os.Stat(paths.Archive); err != nil {
			t.Fatalf("ABI invalidation removed the old cache entry: %v", err)
		}
	})

	t.Run("reuses-stable-descriptor-archive", func(t *testing.T) {
		fresh, again := inputs()
		if again.Build() != manifest || again.Fingerprint() != current.Fingerprint() {
			t.Fatal("identical API inputs produced unstable cache identities")
		}
		archive := filepath.Join(t.TempDir(), "current.a")
		const contents = "current descriptor archive"
		if err := os.WriteFile(archive, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		built := newPackage(current)
		built.ArchiveFile = archive
		if err := ctx.saveToCache(built); err != nil {
			t.Fatal(err)
		}
		cached := newPackage(again)
		if !fresh.tryLoadFromCache(cached) || !cached.CacheHit || cached.ArchiveFile != built.ArchiveFile {
			t.Fatalf("current descriptor archive was not reused: %q, want %q", cached.ArchiveFile, built.ArchiveFile)
		}
		got, err := os.ReadFile(cached.ArchiveFile)
		if err != nil || string(got) != contents {
			t.Fatalf("cached archive = %q, %v; want %q", got, err, contents)
		}
	})
}
