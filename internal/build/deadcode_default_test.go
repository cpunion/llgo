package build

import (
	"testing"

	"github.com/xgo-dev/llgo/internal/lto"
	"github.com/xgo-dev/llgo/internal/optlevel"
	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
)

func TestSemanticDeadcodeDefaultPreservesProducerBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Config)
		want bool
	}{
		{"full executable", func(*Config) {}, true},
		{"ordinary staged executable", func(c *Config) { c.LTO = lto.Off }, false},
		{"O3 staged executable", func(c *Config) { c.LTO = lto.Off; c.OptLevel = optlevel.O3 }, false},
		{"explicit ordinary opt in", func(c *Config) { c.LTO = lto.Off; c.DeadcodeDrop = true }, true},
		{"explicit opt out", func(c *Config) { c.DeadcodeDropSet = true }, false},
		{"c archive", func(c *Config) { c.BuildMode = BuildModeCArchive }, false},
		{"c shared", func(c *Config) { c.BuildMode = BuildModeCShared }, false},
		{"generation", func(c *Config) { c.Mode = ModeGen }, false},
		{"package archive", func(c *Config) { c.Mode = ModeGen; c.PackageCompileOnly = true }, false},
		{"importcfg without full method summary", func(c *Config) { c.ImportCfg = "external.importcfg" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := &Config{Mode: ModeBuild, LTO: lto.Full, OptLevel: optlevel.O3, DisableGoGlobalDCE: true}
			test.edit(input)
			before := *input
			resolved, err := resolveBuildConfig(input)
			if err != nil {
				t.Fatal(err)
			}
			if got := resolved.deadcodeDropEnabled(); got != test.want {
				t.Fatalf("semantic DCE = %v, want %v", got, test.want)
			}
			if input.DeadcodeDrop != before.DeadcodeDrop || input.DeadcodeDropSet != before.DeadcodeDropSet || input.BuildMode != before.BuildMode {
				t.Fatal("resolving the build mutated caller configuration")
			}
		})
	}
}

func TestSemanticDeadcodeMissingSummaryKeepsOriginalSlots(t *testing.T) {
	program := llssa.NewProgram(nil)
	defer program.Dispose()
	source := program.NewPackage("pkg", "pkg")
	addMethodTypeGlobal(source.Module(), "_llgo_pkg.T")
	summary := buildDeadcodeMeta(t)
	defer summary.Close()
	entry := program.NewPackage("entry_main", "entry_main")
	originalSource, originalEntry := source.String(), entry.String()
	pkgs := []Package{
		&aPackage{Package: &packages.Package{PkgPath: "pkg"}, LPkg: source, Meta: summary},
		// A separately produced package may have effect records without a full
		// method-use summary. Its absent calls are not proof of dead methods.
		&aPackage{Package: &packages.Package{PkgPath: "external"}},
	}
	if err := applyDeadcodeDropOverrides(nil, pkgs, &aPackage{LPkg: entry}, false, false); err != nil {
		t.Fatal(err)
	}
	if source.String() != originalSource || entry.String() != originalEntry {
		t.Fatal("incomplete linked summaries changed method slots or emitted overrides")
	}
	if _, complete, err := linkedPackageMetas(pkgs[:1]); err != nil || !complete {
		t.Fatalf("complete source summary = %v, %v", complete, err)
	}
	if _, _, err := linkedPackageMetas([]Package{nil}); err == nil {
		t.Fatal("malformed linked package silently treated as optional metadata")
	}
}
