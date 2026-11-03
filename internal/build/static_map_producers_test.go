//go:build !llgo

package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/cl"
	"github.com/xgo-dev/llgo/internal/crosscompile"
	"github.com/xgo-dev/llgo/internal/meta"
	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
)

func staticMapProducerFixture(identity string) cl.StaticMapInitProducer {
	id := strings.Repeat("a", 64)
	return cl.StaticMapInitProducer{Version: 1, PackageIdentity: identity, PhysicalSymbol: identity + ".M",
		ConstructorID: id, GateSymbol: "__llgo_static_map_init_live_v1_" + id, Entries: 2}
}

func TestStaticMapInitProducerManifestOnlyCapabilities(t *testing.T) {
	base := newManifestBuilder().Fingerprint()
	for _, kind := range []string{"map-schema", "compiler-bytes", "method-schema"} {
		t.Run(kind, func(t *testing.T) {
			manifest := newManifestBuilder()
			switch kind {
			case "map-schema":
				manifest.common.StaticMapInitFragments = staticMapInitFragmentVersion
			case "compiler-bytes":
				manifest.common.StaticMapCompilerSHA256 = strings.Repeat("a", 64)
			case "method-schema":
				manifest.common.MethodTypeFragments = methodTypeFragmentVersion
			}
			decoded, err := decodeManifest(manifest.Build())
			if err != nil || decoded.Common == nil || manifest.Fingerprint() == base {
				t.Fatalf("capability disappeared from the manifest: %s, %v", manifest.Build(), err)
			}
		})
	}
}

func TestStaticMapInitProducerFragmentBinding(t *testing.T) {
	dir := t.TempDir()
	archive, sidecar := filepath.Join(dir, "pkg.a"), filepath.Join(dir, "pkg.staticmaps")
	if err := os.WriteFile(archive, []byte("one exact producer archive"), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := &aPackage{Package: &packages.Package{ID: "example.com/pkg", PkgPath: "example.com/pkg"},
		Fingerprint: strings.Repeat("b", 64), StaticMapInitCaptured: true,
		StaticMapInitProducers: []cl.StaticMapInitProducer{staticMapProducerFixture("example.com/pkg")}}
	compiler := strings.Repeat("c", 64)
	data, err := encodeStaticMapInitFragment(pkg, compiler, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, data, 0600); err != nil {
		t.Fatal(err)
	}
	load := func(hash, owner, fingerprint, compilerID string) (*staticMapInitFragment, error) {
		return readCachedStaticMapInitProducers(sidecar, hash, archive, owner, fingerprint, compilerID)
	}
	fragment, err := load(digestBytes(data), pkg.ID, pkg.Fingerprint, compiler)
	if err != nil || len(fragment.Producers) != 1 || fragment.Producers[0] != pkg.StaticMapInitProducers[0] {
		t.Fatalf("valid producer fragment: %+v, %v", fragment, err)
	}
	for _, name := range []string{"missing-checksum", "wrong-checksum", "wrong-owner", "wrong-source", "wrong-compiler", "changed-archive", "missing-sidecar"} {
		t.Run(name, func(t *testing.T) {
			hash, owner, fingerprint, compilerID := digestBytes(data), pkg.ID, pkg.Fingerprint, compiler
			switch name {
			case "missing-checksum":
				hash = ""
			case "wrong-checksum":
				hash = strings.Repeat("d", 64)
			case "wrong-owner":
				owner += "/other"
			case "wrong-source":
				fingerprint = strings.Repeat("d", 64)
			case "wrong-compiler":
				compilerID = strings.Repeat("d", 64)
			case "changed-archive":
				if err := os.WriteFile(archive, []byte("different archive"), 0600); err != nil {
					t.Fatal(err)
				}
				defer os.WriteFile(archive, []byte("one exact producer archive"), 0600)
			case "missing-sidecar":
				if err := os.Remove(sidecar); err != nil {
					t.Fatal(err)
				}
				defer os.WriteFile(sidecar, data, 0600)
			}
			if _, err := load(hash, owner, fingerprint, compilerID); err == nil {
				t.Fatal("accepted an unbound producer fragment")
			}
		})
	}
	for _, name := range []string{"unknown-fields", "duplicate-fields", "duplicate-producer", "wrong-producer-owner", "uppercase-id", "zero-version", "null-producers", "oversize"} {
		t.Run(name, func(t *testing.T) {
			var changed staticMapInitFragment
			if err := json.Unmarshal(data, &changed); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "duplicate-producer":
				changed.Producers = append(changed.Producers, changed.Producers[0])
			case "wrong-producer-owner":
				changed.Producers[0].PackageIdentity += "/other"
			case "uppercase-id":
				changed.Producers[0].ConstructorID = strings.ToUpper(changed.Producers[0].ConstructorID)
				changed.Producers[0].GateSymbol = "__llgo_static_map_init_live_v1_" + changed.Producers[0].ConstructorID
			case "zero-version":
				changed.Version = 0
			case "null-producers":
				changed.Producers = nil
			}
			corrupt, _ := json.Marshal(changed)
			if name == "unknown-fields" {
				corrupt = append(corrupt[:len(corrupt)-1], []byte(",\"Unknown\":1}")...)
			} else if name == "duplicate-fields" {
				corrupt = append(corrupt[:len(corrupt)-1], []byte(",\"Version\":1}")...)
			} else if name == "oversize" {
				corrupt = []byte(strings.Repeat("x", maxStaticMapInitFragmentBytes+1))
			}
			if err := os.WriteFile(sidecar, corrupt, 0600); err != nil {
				t.Fatal(err)
			}
			defer os.WriteFile(sidecar, data, 0600)
			// A fresh checksum cannot bless structurally invalid metadata.
			if _, err := load(digestBytes(corrupt), pkg.ID, pkg.Fingerprint, compiler); err == nil {
				t.Fatal("accepted a malformed producer with an updated checksum")
			}
		})
	}
}

func TestStaticMapInitProducerEmptyCaptureSurvivesSourceRelease(t *testing.T) {
	program := llssa.NewProgram(nil)
	pkg := &aPackage{Package: &packages.Package{ID: "example.com/empty", PkgPath: "example.com/empty"},
		LPkg: program.NewPackage("empty", "example.com/empty"), Fingerprint: strings.Repeat("a", 64)}
	if err := captureStaticMapInitProducers(pkg); err != nil {
		t.Fatal(err)
	}
	pkg.LPkg = nil
	program.Dispose()
	archive := filepath.Join(t.TempDir(), "empty.a")
	if err := os.WriteFile(archive, []byte("empty captured archive"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := encodeStaticMapInitFragment(pkg, strings.Repeat("b", 64), archive)
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := decodeStaticMapInitFragment(data)
	if err != nil || fragment.Producers == nil || len(fragment.Producers) != 0 {
		t.Fatalf("empty capture: %+v %v", fragment, err)
	}
	pkg.StaticMapInitCaptured = false
	if _, err := encodeStaticMapInitFragment(pkg, strings.Repeat("b", 64), archive); err == nil {
		t.Fatal("missing capture was treated as a proved empty producer set")
	}
	if err := captureStaticMapInitProducers(pkg); err == nil {
		t.Fatal("captured a released package module")
	}
}

func TestStaticMapInitProducerProductionCacheRepair(t *testing.T) {
	t.Setenv("LLGO_BUILD_CACHE", "on")
	dir := t.TempDir()
	previous := cacheRootFunc
	cacheRootFunc = func() string { return dir }
	defer func() { cacheRootFunc = previous }()
	ctx := &context{conf: &packages.Config{}, buildConf: &Config{Goos: "darwin", Goarch: "arm64"},
		crossCompile:    crosscompile.Export{LLVMTarget: "arm64-apple-darwin"},
		frontendOptions: cl.Options{CanonicalStaticMapInits: true}}
	activateCoroPackageCacheTest(t, ctx)
	archive := filepath.Join(dir, "producer.a")
	if err := os.WriteFile(archive, []byte("cache archive fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := &aPackage{Package: &packages.Package{ID: "example.com/cache", PkgPath: "example.com/cache", Name: "cache"},
		ArchiveFile: archive, StaticMapInitCaptured: true,
		StaticMapInitProducers: []cl.StaticMapInitProducer{staticMapProducerFixture("example.com/cache")}}
	setCoroPackageCacheManifest(t, ctx, pkg)
	if err := ctx.saveToCache(pkg); err != nil {
		t.Fatal(err)
	}
	load := func() *aPackage {
		return &aPackage{Package: pkg.Package, Fingerprint: pkg.Fingerprint, Manifest: pkg.Manifest}
	}
	loaded := load()
	if !ctx.tryLoadFromCache(loaded) || !loaded.StaticMapInitCaptured || len(loaded.StaticMapInitProducers) != 1 {
		t.Fatal("valid producer cache did not load")
	}
	paths := ctx.ensureCacheManager().PackagePaths(ctx.targetTriple(), pkg.PkgPath, pkg.Fingerprint)
	for _, name := range []string{"missing-sidecar", "corrupt-sidecar", "changed-archive"} {
		t.Run(name, func(t *testing.T) {
			switch name {
			case "missing-sidecar":
				if err := os.Remove(paths.StaticMaps); err != nil {
					t.Fatal(err)
				}
			case "corrupt-sidecar":
				if err := os.WriteFile(paths.StaticMaps, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed-archive":
				if err := os.WriteFile(paths.Archive, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if bad := load(); ctx.tryLoadFromCache(bad) || bad.CacheHit || bad.StaticMapInitCaptured {
				t.Fatal("invalid cache producer did not require recompilation")
			}
			if err := ctx.saveToCache(pkg); err != nil {
				t.Fatal(err)
			}
			if !ctx.tryLoadFromCache(load()) {
				t.Fatal("recompiled producer cache was not repaired")
			}
		})
	}
	base := newManifestBuilder()
	ctx.collectCommonInputs(base)
	ctx.frontendOptions.CanonicalStaticMapInits = false
	without := newManifestBuilder()
	ctx.collectCommonInputs(without)
	if base.Fingerprint() == without.Fingerprint() {
		t.Fatal("canonical gate capability did not separate the cache fingerprint")
	}
}

func TestStaticMapInitProducerMethodTypesCacheMissKeepsFreshCapture(t *testing.T) {
	t.Setenv("LLGO_BUILD_CACHE", "on")
	dir := t.TempDir()
	previous := cacheRootFunc
	cacheRootFunc = func() string { return dir }
	defer func() { cacheRootFunc = previous }()
	ctx := &context{conf: &packages.Config{},
		buildConf:       &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH, BuildMode: BuildModeExe},
		crossCompile:    crosscompile.Export{LLVMTarget: "arm64-apple-darwin"},
		frontendOptions: cl.Options{CanonicalStaticMapInits: true}}
	activateCoroPackageCacheTest(t, ctx)
	if !ctx.buildConf.stagedMethodDCEEnabled() {
		t.Fatal("fixture requires both cache fragment contracts")
	}
	archive := filepath.Join(dir, "producer.a")
	if err := os.WriteFile(archive, []byte("retained native archive"), 0600); err != nil {
		t.Fatal(err)
	}
	methodTypes, err := json.Marshal(methodTypeFragment{Version: methodTypeFragmentVersion, Unavailable: true})
	if err != nil {
		t.Fatal(err)
	}
	packageMeta, err := meta.NewBuilder().Build()
	if err != nil {
		t.Fatal(err)
	}
	defer packageMeta.Close()
	pkg := &aPackage{Package: &packages.Package{ID: "example.com/cache-miss", PkgPath: "example.com/cache-miss", Name: "cachemiss"},
		ArchiveFile: archive, Meta: packageMeta, MethodTypeFragment: methodTypes, StaticMapInitCaptured: true,
		StaticMapInitProducers:  []cl.StaticMapInitProducer{staticMapProducerFixture("example.com/cache-miss")},
		StaticMapInitOwnObjects: []staticMapObjectDigest{{SHA256: digestBytes([]byte("old native object")), Bytes: 17}}}
	setCoroPackageCacheManifest(t, ctx, pkg)
	load := func() *aPackage {
		return &aPackage{Package: pkg.Package, Fingerprint: pkg.Fingerprint, Manifest: pkg.Manifest}
	}
	paths := ctx.ensureCacheManager().PackagePaths(ctx.targetTriple(), pkg.PkgPath, pkg.Fingerprint)
	for _, name := range []string{"missing-methodtypes", "corrupt-methodtypes"} {
		t.Run(name, func(t *testing.T) {
			if err := ctx.saveToCache(pkg); err != nil {
				t.Fatal(err)
			}
			valid := load()
			if !ctx.tryLoadFromCache(valid) || !valid.StaticMapInitCaptured || len(valid.StaticMapInitOwnObjects) != 1 || len(valid.MethodTypeFragment) == 0 {
				t.Fatal("valid coexisting fragments did not load")
			}
			if valid.Meta != nil {
				defer valid.Meta.Close()
			}
			if name == "missing-methodtypes" {
				if err := os.Remove(paths.MethodTypes); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(paths.MethodTypes, []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
			bad := load()
			if ctx.tryLoadFromCache(bad) || bad.CacheHit {
				t.Fatal("invalid method types did not reject the cache entry")
			}
			if bad.StaticMapInitCaptured || len(bad.StaticMapInitProducers) != 0 || len(bad.StaticMapInitOwnObjects) != 0 || len(bad.MethodTypeFragment) != 0 {
				t.Fatal("cache miss published partial producer state")
			}
			program := llssa.NewProgram(nil)
			defer program.Dispose()
			bad.LPkg = program.NewPackage("cachemiss", pkg.PkgPath)
			if err := captureStaticMapInitProducers(bad); err != nil {
				t.Fatal(err)
			}
			if err := recordStaticMapGoObject(bad, "", []byte("fresh native object")); err != nil {
				t.Fatalf("recompile after cache miss: %v", err)
			}
		})
	}
}
