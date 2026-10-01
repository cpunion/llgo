package build

import (
	"bytes"
	"fmt"
	"go/build"
	"os/exec"
	"slices"
	"strings"

	"github.com/xgo-dev/llgo/internal/asmselect"
)

// nativeASMSelectionOverlay changes only the build-constraint headers of
// explicitly selected external packages. The subsequent single package-driver
// load selects their Go declarations, assembly files and imports together,
// while every other package retains LLGo's normal global build tags.
func nativeASMSelectionOverlay(commands commandEnv, conf *Config, flags []string, goroot string) (map[string][]byte, error) {
	if len(conf.NativeASMPackages) == 0 {
		return nil, nil
	}
	context := build.Default
	context.GOROOT, context.GOOS, context.GOARCH, context.Compiler = goroot, conf.Goos, conf.Goarch, "gc"
	context.ToolTags = slices.Clone(conf.toolTags)
	context.BuildTags = nil
	var err error
	context.ReleaseTags, err = releaseTagsForGoVersion(conf.sourceGoVersion)
	if err != nil {
		return nil, err
	}
	// The explicit native profile removes LLGo implementation/fallback tags
	// only when matching these external directories, never from go list's
	// global flags or from standard-library/runtime source selection.
	var effectiveTags []string
	for i := 0; i < len(flags); i++ {
		if flags[i] == "-tags" && i+1 < len(flags) {
			i++
			effectiveTags = splitSourcePatchBuildTags(flags[i])
		}
		if strings.HasPrefix(flags[i], "-tags=") {
			effectiveTags = splitSourcePatchBuildTags(strings.TrimPrefix(flags[i], "-tags="))
		}
	}
	for _, tag := range effectiveTags {
		if tag != "llgo" && tag != "purego" && tag != "math_big_pure_go" {
			context.BuildTags = append(context.BuildTags, tag)
		}
	}
	cmd := commands.configure(exec.Command("go", "env", "CGO_ENABLED"))
	value, err := cmd.Output()
	if err != nil {
		return nil, sourceGoConfigError("resolve native assembly cgo context", err)
	}
	context.CgoEnabled = strings.TrimSpace(string(value)) == "1"
	result := make(map[string][]byte)
	for _, name := range conf.NativeASMPackages {
		if _, err := asmselect.ParsePackages(name); err != nil {
			return nil, err
		}
		args := []string{"list", "-find", "-f", "{{.ImportPath}}\n{{.Dir}}\n{{.Standard}}\n{{with .Module}}{{.Path}}{{end}}"}
		args = append(args, flags...)
		args = append(args, name)
		cmd := commands.configure(exec.Command("go", args...))
		output, err := cmd.Output()
		if err != nil {
			return nil, sourceGoConfigError("locate explicit native assembly package "+name, err)
		}
		info := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
		if len(info) != 4 || info[0] != name || info[1] == "" || info[2] != "true" && info[2] != "false" {
			return nil, fmt.Errorf("invalid exact native assembly package metadata for %s", name)
		}
		if err := asmselect.ValidatePackage(name, info[3], info[2] == "true"); err != nil {
			return nil, err
		}
		overlay, err := asmselect.HeaderOverlay(info[1], context)
		if err != nil {
			return nil, fmt.Errorf("select native assembly package %s: %w", name, err)
		}
		for filename, data := range overlay {
			if existing, ok := conf.Overlay[filename]; ok && !bytes.Equal(existing, data) {
				return nil, fmt.Errorf("native assembly selection conflicts with an existing source overlay: %s", filename)
			}
			result[filename] = data
		}
	}
	return result, nil
}
