package runtime

import "sort"

type altPkgMode uint8

const (
	altPkgReplace altPkgMode = iota + 1
	altPkgAdditive
)

type altPkgSpec struct {
	mode    altPkgMode
	gooses  map[string]struct{}
	goarchs map[string]struct{}
}

func (s altPkgSpec) enabledFor(goos, goarch string) bool {
	return targetEnabled(s.gooses, goos) && targetEnabled(s.goarchs, goarch)
}

func targetEnabled(values map[string]struct{}, value string) bool {
	if len(values) == 0 {
		return true
	}
	_, ok := values[value]
	return ok
}

func SkipToBuild(pkgPath string) bool {
	if _, ok := altPkgs[pkgPath]; ok {
		return false
	}
	return pkgPath == "unsafe"
}

func HasAltPkg(path string) (b bool) {
	_, b = altPkgs[path]
	return
}

func HasAltPkgForTarget(path, goos, goarch string) bool {
	spec, ok := altPkgs[path]
	return ok && spec.enabledFor(goos, goarch)
}

func HasAdditiveAltPkg(path string) bool {
	return altPkgs[path].mode == altPkgAdditive
}

func HasAdditiveAltPkgForTarget(path, goos, goarch string) bool {
	spec, ok := altPkgs[path]
	return ok && spec.mode == altPkgAdditive && spec.enabledFor(goos, goarch)
}

var altPkgs = map[string]altPkgSpec{
	"internal/abi":            {mode: altPkgReplace},
	"internal/runtime/atomic": {mode: altPkgReplace, goarchs: map[string]struct{}{"arm": {}}},
	"internal/reflectlite":    {mode: altPkgReplace},
	"internal/runtime/maps":   {mode: altPkgReplace},
	"internal/runtime/sys":    {mode: altPkgAdditive},
	"internal/syscall/unix":   {mode: altPkgAdditive, gooses: map[string]struct{}{"darwin": {}}},
	"reflect":                 {mode: altPkgReplace},
	"runtime":                 {mode: altPkgReplace},
	"sync/atomic":             {mode: altPkgReplace},
	"sync":                    {mode: altPkgReplace},
	"syscall/js":              {mode: altPkgReplace},
	"syscall":                 {mode: altPkgReplace},
	"unique":                  {mode: altPkgReplace},
	"golang.org/x/sys/unix":   {mode: altPkgReplace},
}

func HasSourcePatchPkg(path string) bool {
	_, ok := sourcePatchPkgs[path]
	return ok
}

func SourcePatchPkgPaths() []string {
	paths := make([]string, 0, len(sourcePatchPkgs))
	for path := range sourcePatchPkgs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func SourcePatchReplacesAsmForGOARCH(path, goarch string) bool {
	goarchs, ok := sourcePatchAsmPkgs[path]
	if !ok {
		return false
	}
	_, ok = goarchs[goarch]
	return ok
}

var sourcePatchPkgs = map[string]struct{}{
	"crypto/internal/constanttime":   {},
	"internal/bytealg":               {},
	"internal/chacha8rand":           {},
	"internal/poll":                  {},
	"internal/runtime/atomic":        {},
	"internal/runtime/syscall/linux": {},
	"internal/sync":                  {},
	"iter":                           {},
	"net":                            {},
	"runtime":                        {},
	"runtime/metrics":                {},
	"syscall":                        {},
	"time":                           {},
}

var sourcePatchAsmPkgs = map[string]map[string]struct{}{
	"internal/bytealg":        {"wasm": {}},
	"internal/chacha8rand":    {"wasm": {}},
	"internal/runtime/atomic": {"wasm": {}},
}
