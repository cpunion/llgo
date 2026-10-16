package cltest

import "testing"

func TestCompileIRRuntimeTypesUseProductionSources(t *testing.T) {
	pkg, err := compileIRRuntimeTypes()
	if err != nil || pkg == nil {
		t.Fatalf("production runtime types: %v", err)
	}
	for _, name := range []string{
		"CallerFrame", "coroObserveGCV1", "coroDetachGCNotificationV1",
		"coroCancelDomainParkV1", "newSynctestChan",
	} {
		if pkg.Scope().Lookup(name) == nil {
			t.Errorf("production runtime is missing %s", name)
		}
	}
	if again, err := compileIRRuntimeTypes(); err != nil || again != pkg {
		t.Fatal("runtime type cache did not retain the immutable production package")
	}
}
