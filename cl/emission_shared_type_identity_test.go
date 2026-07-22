//go:build !llgo

package cl

import (
	"go/types"
	"testing"
)

func TestEmissionUniverseSharedTypesPackageHasVariantSetIdentity(t *testing.T) {
	pkg := types.NewPackage("example.com/emission/sharedtypes", "sharedtypes")
	ordinary := &preparedEmissionPackage{
		identity: "example.com/emission/sharedtypes",
		pkgPath:  pkg.Path(),
	}
	testVariant := &preparedEmissionPackage{
		identity: "example.com/emission/sharedtypes [example.com/emission/sharedtypes.test]",
		pkgPath:  pkg.Path(),
	}
	u := &EmissionUniverse{
		typesDup: map[*types.Package]bool{pkg: true},
		typeOwners: map[*types.Package]map[*preparedEmissionPackage]none{
			pkg: {testVariant: {}, ordinary: {}},
		},
	}
	key, err := u.FunctionIDConfig().CanonicalPackageKey(pkg)
	if err != nil {
		t.Fatal(err)
	}
	want := framedEmissionKey(
		"cl-emission-shared-package-type-v1",
		pkg.Path(),
		ordinary.identity,
		testVariant.identity,
	)
	if key != want {
		t.Fatalf("shared package type key = %q; want stable variant-set key %q", key, want)
	}
}

func TestEmissionUniverseSharedTypesPackageRequiresCompleteOwners(t *testing.T) {
	pkg := types.NewPackage("example.com/emission/incompletesharedtypes", "incompletesharedtypes")
	u := &EmissionUniverse{
		typesDup:   map[*types.Package]bool{pkg: true},
		typeOwners: map[*types.Package]map[*preparedEmissionPackage]none{pkg: {}},
	}
	if _, err := u.FunctionIDConfig().CanonicalPackageKey(pkg); err == nil {
		t.Fatal("incomplete shared package type ownership was accepted")
	}
}
