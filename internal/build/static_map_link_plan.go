package build

import (
	"bytes"
	stdcontext "context"
	"debug/macho"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type staticMapLimitedOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *staticMapLimitedOutput) Write(data []byte) (int, error) {
	n := len(data)
	if n > b.limit-b.Len() {
		b.overflow = true
		data = data[:b.limit-b.Len()]
	}
	_, _ = b.Buffer.Write(data)
	return n, nil
}

func staticMapCommandOutput(base *exec.Cmd, args []string, limit int) (string, error) {
	deadline, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(deadline, base.Path, args...)
	// Clang++ and ld64.lld may be symlinks to generic dispatcher binaries.
	// Pin the executable bytes without changing the argv[0] driver flavor.
	cmd.Args[0] = base.Args[0]
	cmd.Dir, cmd.Env = base.Dir, slices.Clone(base.Env)
	output := &staticMapLimitedOutput{limit: limit}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("static map link inspection: %w", err)
	}
	if output.overflow {
		return "", fmt.Errorf("static map link inspection exceeded output budget")
	}
	return output.String(), nil
}

// Clang's -### printer quotes every job argument. Accept only that complete
// grammar, rather than interpreting shell expansions or response files.
func staticMapParseQuotedJob(line string) ([]string, error) {
	if len(line) > 1<<20 {
		return nil, fmt.Errorf("oversized linker job")
	}
	var args []string
	for line = strings.TrimSpace(line); line != ""; line = strings.TrimSpace(line) {
		if line[0] != '"' {
			return nil, fmt.Errorf("unquoted linker job argument")
		}
		end := 1
		for ; end < len(line); end++ {
			if line[end] == '\\' {
				end++
				continue
			}
			if line[end] == '"' {
				break
			}
		}
		if end >= len(line) {
			return nil, fmt.Errorf("unterminated linker job argument")
		}
		arg, err := strconv.Unquote(line[:end+1])
		if err != nil || strings.ContainsRune(arg, 0) || strings.HasPrefix(arg, "@") {
			return nil, fmt.Errorf("unsupported linker job argument")
		}
		args = append(args, arg)
		line = line[end+1:]
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			return nil, fmt.Errorf("unseparated linker job arguments")
		}
	}
	return args, nil
}

func staticMapDriverJob(driver *exec.Cmd) (*exec.Cmd, error) {
	text, err := staticMapCommandOutput(driver, append([]string{"-###"}, driver.Args[1:]...), 1<<20)
	if err != nil {
		return nil, err
	}
	var jobs [][]string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "\"") {
			continue
		}
		args, err := staticMapParseQuotedJob(line)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, args)
	}
	if len(jobs) != 1 || len(jobs[0]) < 2 || !filepath.IsAbs(jobs[0][0]) || filepath.Base(jobs[0][0]) != "ld64.lld" {
		return nil, fmt.Errorf("static map proof requires one explicit ld64.lld job")
	}
	job := exec.Command(jobs[0][0], jobs[0][1:]...)
	job.Dir, job.Env = driver.Dir, slices.Clone(driver.Env)
	job.Stdin, job.Stdout, job.Stderr = driver.Stdin, driver.Stdout, driver.Stderr
	return job, nil
}

type staticMapLDPlan struct {
	args        []string
	probeArgs   []string
	outputIndex int
	libraries   map[int]string
}

func staticMapLibraryNameValid(name string) bool {
	if name == "" || len(name) > 256 {
		return false
	}
	for _, char := range name {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("_+.-", char)) {
			return false
		}
	}
	return true
}

func staticMapParseLDPlan(args []string, sources map[string]bool) (*staticMapLDPlan, error) {
	plan := &staticMapLDPlan{args: slices.Clone(args), outputIndex: -1, libraries: make(map[int]string)}
	seen := make(map[string]bool)
	arch, platform, dynamic, noexports := 0, 0, 0, 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.ContainsRune(arg, 0) || strings.HasPrefix(arg, "@") {
			return nil, fmt.Errorf("unsupported linker argument")
		}
		switch arg {
		case "-demangle", "-dead_strip", "-S", "--error-limit=0", "--icf=none", "--lto-O2":
			plan.probeArgs = append(plan.probeArgs, arg)
		case "-dynamic":
			dynamic++
			plan.probeArgs = append(plan.probeArgs, arg)
		case "-no_exported_symbols":
			noexports++
			plan.probeArgs = append(plan.probeArgs, arg)
		case "-arch", "-syslibroot", "-o", "-rpath", "-mllvm", "-L":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("missing linker option value")
			}
			value := args[i+1]
			if value == "" || strings.ContainsAny(value, "\x00\r\n") || strings.HasPrefix(value, "@") {
				return nil, fmt.Errorf("invalid linker option value")
			}
			if arg == "-arch" {
				arch++
				if value != "arm64" {
					return nil, fmt.Errorf("unsupported map proof architecture")
				}
			}
			if arg == "-mllvm" && value != "-enable-linkonceodr-outlining" {
				return nil, fmt.Errorf("unproved linker LLVM option")
			}
			if arg == "-syslibroot" && !filepath.IsAbs(value) {
				return nil, fmt.Errorf("relative SDK root")
			}
			if arg == "-o" {
				if plan.outputIndex >= 0 {
					return nil, fmt.Errorf("multiple linker outputs")
				}
				plan.outputIndex = i + 1
			} else {
				plan.probeArgs = append(plan.probeArgs, arg, value)
			}
			i++
		case "-platform_version":
			if i+3 >= len(args) || args[i+1] != "macos" {
				return nil, fmt.Errorf("unsupported map proof platform")
			}
			for _, version := range args[i+2 : i+4] {
				if version == "" || len(version) > 32 || strings.Trim(version, "0123456789.") != "" {
					return nil, fmt.Errorf("unsupported platform version")
				}
			}
			platform++
			plan.probeArgs = append(plan.probeArgs, args[i:i+4]...)
			i += 3
		default:
			switch {
			case strings.HasPrefix(arg, "-L") && len(arg) > 2:
				plan.probeArgs = append(plan.probeArgs, arg)
			case strings.HasPrefix(arg, "-l") && staticMapLibraryNameValid(arg[2:]):
				plan.libraries[i] = arg[2:]
				plan.probeArgs = append(plan.probeArgs, arg)
			case filepath.IsAbs(arg):
				if sources[arg] {
					if seen[arg] {
						return nil, fmt.Errorf("duplicate application input")
					}
					seen[arg] = true
				} else {
					switch filepath.Ext(arg) {
					case ".a", ".o", ".dylib", ".tbd":
						plan.probeArgs = append(plan.probeArgs, arg)
					default:
						return nil, fmt.Errorf("unproved driver-selected native input")
					}
				}
			default:
				return nil, fmt.Errorf("unproved linker option %q", arg)
			}
		}
	}
	if arch != 1 || platform != 1 || dynamic != 1 || noexports != 1 || plan.outputIndex < 0 || len(seen) != len(sources) {
		return nil, fmt.Errorf("incomplete native executable/no-export/input contract")
	}
	return plan, nil
}

type staticMapFileSnapshot struct {
	Path, RealPath, SHA256 string
}

func staticMapSnapshotFile(path string) (staticMapFileSnapshot, error) {
	if !filepath.IsAbs(path) {
		return staticMapFileSnapshot{}, fmt.Errorf("relative static map input")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return staticMapFileSnapshot{}, err
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxStaticMapNativeInputBytes {
		return staticMapFileSnapshot{}, fmt.Errorf("unbounded or nonregular static map input")
	}
	hash, err := digestFile(real)
	return staticMapFileSnapshot{path, real, hash}, err
}

func (s staticMapFileSnapshot) verify() error {
	current, err := staticMapSnapshotFile(s.Path)
	if err != nil || current != s {
		return fmt.Errorf("static map linker input changed: %s", s.Path)
	}
	return nil
}

// Inventory the complete library-only trace, including every member of the
// implicit compiler runtime. Unknown lines cannot prove an absent reference.
func staticMapLibraryTrace(text, stub string) (map[string]staticMapFileSnapshot, map[string]bool, error) {
	files := make(map[string]staticMapFileSnapshot)
	mentions := make(map[string]bool)
	stubSeen := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == stub {
			stubSeen = true
			continue
		}
		path := line
		if strings.HasSuffix(line, ")") {
			if pos := strings.LastIndex(line, "("); pos >= 0 {
				path = line[:pos]
				if filepath.Ext(path) != ".a" && filepath.Ext(path) != ".tbd" {
					return nil, nil, fmt.Errorf("unproved native trace member")
				}
			}
		}
		if _, seen := files[path]; seen {
			continue
		}
		snapshot, err := staticMapSnapshotFile(path)
		if err != nil {
			return nil, nil, err
		}
		switch filepath.Ext(path) {
		case ".a":
			inventory, err := inspectStaticMapNativeArchive(path, nil)
			if err != nil || inventory.SHA256 != snapshot.SHA256 {
				return nil, nil, fmt.Errorf("unproved static library: %s", path)
			}
			for _, symbol := range inventory.Mentions {
				mentions[symbol] = true
			}
		case ".o":
			data, err := staticMapReadNativeInput(path)
			if err != nil {
				return nil, nil, err
			}
			symbols, err := staticMapMachOObjectMentions(data)
			if err != nil || digestBytes(data) != snapshot.SHA256 {
				return nil, nil, fmt.Errorf("unproved driver object")
			}
			for _, symbol := range symbols {
				mentions[symbol] = true
			}
		case ".dylib", ".tbd":
			// The frozen final executable publishes no symbols to dyld.
			// Its Go references independently prove that no map address escapes.
			data, err := staticMapReadNativeInput(path)
			if err != nil || digestBytes(data) != snapshot.SHA256 || !staticMapDynamicInput(data, filepath.Ext(path)) {
				return nil, nil, fmt.Errorf("unproved dynamic library input")
			}
		default:
			return nil, nil, fmt.Errorf("unproved native library trace input")
		}
		files[path] = snapshot
	}
	if !stubSeen || len(files) == 0 {
		return nil, nil, fmt.Errorf("incomplete native library trace")
	}
	return files, mentions, nil
}

func staticMapPinLibraries(plan *staticMapLDPlan, files map[string]staticMapFileSnapshot) ([]string, error) {
	args := slices.Clone(plan.args)
	for index, name := range plan.libraries {
		match := ""
		for path, snapshot := range files {
			base := filepath.Base(path)
			if base != "lib"+name+".a" && base != "lib"+name+".dylib" && base != "lib"+name+".tbd" {
				continue
			}
			if match != "" && match != snapshot.RealPath {
				return nil, fmt.Errorf("ambiguous native library resolution")
			}
			match = snapshot.RealPath
		}
		if match == "" {
			return nil, fmt.Errorf("native library resolution is missing: %s", name)
		}
		args[index] = match
	}
	return args, nil
}

func staticMapProbeLibraries(job *exec.Cmd, plan *staticMapLDPlan, stub, output string) (map[string]staticMapFileSnapshot, map[string]bool, error) {
	args := append(slices.Clone(plan.probeArgs), "-all_load", "-t", stub, "-o", output)
	text, err := staticMapCommandOutput(job, args, 8<<20)
	if err != nil {
		return nil, nil, err
	}
	return staticMapLibraryTrace(text, stub)
}

func staticMapSameFiles(a, b map[string]staticMapFileSnapshot) bool {
	// A pinned symlink may be printed using its canonical path. Compare the
	// exact resolved file set and contents, not the diagnostic spelling.
	resolved := make(map[string]string)
	for _, file := range a {
		resolved[file.RealPath] = file.SHA256
	}
	other := make(map[string]string)
	for _, file := range b {
		other[file.RealPath] = file.SHA256
	}
	if len(resolved) != len(other) {
		return false
	}
	for path, hash := range resolved {
		if other[path] != hash {
			return false
		}
	}
	return true
}

func staticMapDynamicInput(data []byte, extension string) bool {
	if extension == ".tbd" {
		var stub struct {
			Version int             `json:"tapi_tbd_version"`
			Library json.RawMessage `json:"main_library"`
		}
		if json.Unmarshal(data, &stub) == nil && stub.Version == 5 && len(stub.Library) != 0 && string(stub.Library) != "null" {
			return true
		}
		// Older Apple TextAPI stubs have a mandatory YAML type marker. This
		// format cannot contain native object code; ld64 validates its schema.
		return bytes.HasPrefix(data, []byte("--- !tapi-tbd\n")) || bytes.HasPrefix(data, []byte("--- !tapi-tbd-v3\n")) || bytes.HasPrefix(data, []byte("--- !tapi-tbd-v2\n"))
	}
	slice, err := staticMapArm64Slice(data)
	if err != nil {
		return false
	}
	object, err := macho.NewFile(bytes.NewReader(slice))
	if err != nil {
		return false
	}
	defer object.Close()
	return object.Type == macho.TypeDylib && object.Cpu == macho.CpuArm64 && object.SubCpu == 0
}
