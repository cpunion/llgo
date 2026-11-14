package build

import (
	"slices"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/crosscompile"
	"github.com/xgo-dev/llgo/internal/lto"
	"github.com/xgo-dev/llgo/internal/optlevel"
)

func elfLTOContext() *context {
	return &context{
		buildConf: &Config{Goos: "linux", Goarch: "amd64", BuildMode: BuildModeExe, OptLevel: optlevel.O3, LTO: lto.Full},
		crossCompile: crosscompile.Export{
			CC: "clang", CXX: "clang++",
			Toolchain: crosscompile.NativeToolchain{ObjectFormat: crosscompile.ObjectFormatELF, Linker: crosscompile.LinkerFlavorELFLLD},
		},
	}
}

func TestELFFullLTOAlignmentScope(t *testing.T) {
	t.Setenv("CCFLAGS", "")
	t.Setenv("LDFLAGS", "")
	for _, test := range []struct {
		name   string
		change func(*context)
	}{
		{"default", nil},
		{"O0", func(c *context) { c.buildConf.OptLevel = optlevel.O0 }},
		{"O2", func(c *context) { c.buildConf.OptLevel = optlevel.O2 }},
		{"Os", func(c *context) { c.buildConf.OptLevel = optlevel.Os }},
		{"thin", func(c *context) { c.buildConf.LTO = lto.Thin }},
		{"off", func(c *context) { c.buildConf.LTO = lto.Off }},
		{"arm64", func(c *context) { c.buildConf.Goarch = "arm64" }},
		{"darwin", func(c *context) { c.buildConf.Goos = "darwin" }},
		{"shared", func(c *context) { c.buildConf.BuildMode = BuildModeCShared }},
		{"COFF", func(c *context) { c.crossCompile.Toolchain.ObjectFormat = crosscompile.ObjectFormatCOFF }},
		{"external linker", func(c *context) { c.buildConf.LinkOptions.ExternalLinker = "other-clang" }},
		{"direct linker", func(c *context) { c.crossCompile.Linker = "ld.lld" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := elfLTOContext()
			if test.change != nil {
				test.change(ctx)
			}
			args := ctx.linker().LinkArguments("input.o")
			got := slices.ContainsFunc(args, func(arg string) bool { return strings.Contains(arg, "-align-") })
			if want := test.change == nil; got != want {
				t.Fatalf("alignment defaults present = %v, want %v: %q", got, want, args)
			}
		})
	}
}

func TestELFFullLTOAlignmentPrecedence(t *testing.T) {
	for _, cxx := range []bool{false, true} {
		ctx := elfLTOContext()
		if !cxx {
			ctx.crossCompile.CXX = ""
		}
		t.Setenv("CCFLAGS", "-Wl,-mllvm,-align-all-functions=4")
		t.Setenv("LDFLAGS", "-Wl,-mllvm,-align-loops=16")
		ctx.crossCompile.CCArgs = []string{"--driver-mode=gcc"}
		ctx.crossCompile.CXXArgs = []string{"--driver-mode=g++"}
		beforeCC, beforeCXX := slices.Clone(ctx.crossCompile.CCArgs), slices.Clone(ctx.crossCompile.CXXArgs)
		args := ctx.linker().LinkArguments("-Wl,-mllvm,-align-loops=8", "input.o")
		for _, pair := range [][2]string{
			{"-Wl,-mllvm,-align-all-functions=6", "-Wl,-mllvm,-align-all-functions=4"},
			{"-Wl,-mllvm,-align-loops=32", "-Wl,-mllvm,-align-loops=16"},
			{"-Wl,-mllvm,-align-loops=16", "-Wl,-mllvm,-align-loops=8"},
		} {
			first, second := slices.Index(args, pair[0]), slices.Index(args, pair[1])
			if first < 0 || second <= first {
				t.Fatalf("option precedence %q before %q: %q", pair[0], pair[1], args)
			}
		}
		if !slices.Equal(ctx.crossCompile.CCArgs, beforeCC) || !slices.Equal(ctx.crossCompile.CXXArgs, beforeCXX) {
			t.Fatal("link defaults mutated the shared compiler configuration")
		}
	}
}
