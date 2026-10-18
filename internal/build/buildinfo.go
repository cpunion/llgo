package build

import (
	"encoding/binary"
	"runtime"
	"runtime/debug"
	"sort"

	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	gopackages "golang.org/x/tools/go/packages"
)

// These delimiters are the Go module-info format consumed by runtime/debug
// and debug/buildinfo. The native record uses the pointer-free Go 1.18 format.
const (
	buildInfoStart         = "\x30\x77\xaf\x0c\x92\x74\x08\x02\x41\xe1\xc1\x07\xe6\xd6\x18\xe6"
	buildInfoEnd           = "\xf9\x32\x43\x31\x86\x18\x20\x72\x00\x82\x42\x10\x41\x16\xd8\xf2"
	buildInfoSymbol        = "__llgo_buildinfo"
	buildInfoRuntimeSymbol = "__llgo_buildinfo.modinfo"
)

func linkedBuildInfo(conf *Config, root *packages.Package, linked []Package) string {
	info := debug.BuildInfo{Path: root.PkgPath}
	module := func(m *gopackages.Module) debug.Module {
		version := m.Version
		if version == "" && m.Main {
			version = "(devel)"
		}
		return debug.Module{Path: m.Path, Version: version}
	}
	convert := func(m *gopackages.Module) debug.Module {
		result := module(m)
		if m.Replace != nil {
			replacement := module(m.Replace)
			result.Replace = &replacement
		}
		return result
	}
	if root.Module != nil {
		info.Main = convert(root.Module)
	}
	seen := make(map[string]bool)
	for _, pkg := range linked {
		if pkg == nil || pkg.Package == nil || pkg.Module == nil {
			continue
		}
		m := pkg.Module
		if m.Path == info.Main.Path || seen[m.Path] {
			continue
		}
		seen[m.Path] = true
		dep := convert(m)
		info.Deps = append(info.Deps, &dep)
	}
	sort.Slice(info.Deps, func(i, j int) bool { return info.Deps[i].Path < info.Deps[j].Path })
	mode := conf.BuildMode
	if mode == "" {
		mode = BuildModeExe
	}
	info.Settings = []debug.BuildSetting{
		{Key: "-buildmode", Value: string(mode)},
		{Key: "-compiler", Value: "llgo"},
		{Key: "GOARCH", Value: conf.Goarch},
		{Key: "GOOS", Value: conf.Goos},
	}
	// The package loader supplies paths, versions and replacements, but no
	// module sums or VCS attestations. Do not manufacture those fields.
	return buildInfoStart + info.String() + buildInfoEnd
}

func buildInfoVersion(conf *Config) string {
	if value, ok := conf.GlobalRewrites["runtime"]["buildVersion"]; ok {
		return value
	}
	return runtime.Version()
}

func encodeBuildInfo(version, modinfo string, pointerSize int, bigEndian bool) []byte {
	data := make([]byte, 32)
	copy(data, "\xff Go buildinf:")
	data[14] = byte(pointerSize)
	data[15] = 2
	if bigEndian {
		data[15] |= 1
	}
	for _, value := range []string{version, modinfo} {
		var length [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(length[:], uint64(len(value)))
		data = append(data, length[:n]...)
		data = append(data, value...)
	}
	for len(data)%16 != 0 {
		data = append(data, 0)
	}
	return data
}

func emitBuildInfo(ctx *context, pkg llssa.Package, modinfo string, withRuntime bool) {
	if modinfo == "" {
		return
	}
	if withRuntime {
		pkg.AddGlobalString(buildInfoRuntimeSymbol, modinfo)
		pkg.Module().NamedGlobal(buildInfoRuntimeSymbol).SetGlobalConstant(true)
	}

	var section string
	switch ctx.buildConf.Goos {
	case "darwin", "ios":
		section = "__DATA,__go_buildinfo"
	case "windows":
		// debug/buildinfo searches the first writable PE data section.
		section = ".data"
	case "linux", "android", "freebsd", "netbsd", "openbsd", "dragonfly", "solaris", "illumos":
		section = ".go.buildinfo"
	default:
		// Wasm and bare-metal images have no debug/buildinfo file reader.
		return
	}
	data := encodeBuildInfo(buildInfoVersion(ctx.buildConf), modinfo,
		ctx.prog.PointerSize(), ctx.prog.TargetData().ByteOrder() == llvm.BigEndian)
	mod := pkg.Module()
	initial := mod.Context().ConstString(string(data), false)
	global := llvm.AddGlobal(mod, initial.Type(), buildInfoSymbol)
	global.SetInitializer(initial)
	global.SetLinkage(llvm.InternalLinkage)
	global.SetAlignment(16)
	global.SetSection(section)
	if err := pkg.RetainGlobal(buildInfoSymbol); err != nil {
		panic(err)
	}
}
