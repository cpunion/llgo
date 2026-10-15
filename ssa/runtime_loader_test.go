//go:build !llgo

package ssa

import (
	"errors"
	"fmt"
	"go/types"
	"sync"
	"testing"
)

// RuntimeTypeLoaderForTest is installed by the external test package, which
// may depend on the production build pipeline without making ssa import build.
// No test-only runtime declarations or production injection API are needed.
var RuntimeTypeLoaderForTest func() (*types.Package, error)

var cachedRuntimeTypesForTest = sync.OnceValues(func() (*types.Package, error) {
	return validateRuntimeTypeLoaderForTest(RuntimeTypeLoaderForTest)
})

func validateRuntimeTypeLoaderForTest(loader func() (*types.Package, error)) (*types.Package, error) {
	if loader == nil {
		return nil, errors.New("SSA runtime type loader was not installed")
	}
	pkg, err := loader()
	if err != nil {
		return nil, fmt.Errorf("load production runtime types: %w", err)
	}
	if pkg == nil || pkg.Path() != PkgRuntime || !pkg.Complete() {
		return nil, errors.New("production pipeline did not provide complete runtime types")
	}
	return pkg, nil
}

func RuntimeTypesForTest(t testing.TB) *types.Package {
	t.Helper()
	pkg, err := cachedRuntimeTypesForTest()
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestProductionRuntimeTypeLoader(t *testing.T) {
	pkg := RuntimeTypesForTest(t)
	if pkg != RuntimeTypesForTest(t) || pkg.Scope().Lookup("AllocZ") == nil {
		t.Fatal("runtime types were not cached from the complete production package")
	}
}

func TestRuntimeTypeLoaderRejectsMissingOrFailedAuthority(t *testing.T) {
	failure := errors.New("source selection failed")
	if _, err := validateRuntimeTypeLoaderForTest(func() (*types.Package, error) { return nil, failure }); !errors.Is(err, failure) {
		t.Fatalf("loader discarded its production failure: %v", err)
	}
	for _, loader := range []func() (*types.Package, error){
		nil,
		func() (*types.Package, error) { return nil, nil },
		func() (*types.Package, error) { return types.NewPackage("other", "other"), nil },
		func() (*types.Package, error) { return types.NewPackage(PkgRuntime, "runtime"), nil },
	} {
		if _, err := validateRuntimeTypeLoaderForTest(loader); err == nil {
			t.Fatal("missing, incorrect, or incomplete runtime authority was accepted")
		}
	}
}
