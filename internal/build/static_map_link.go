package build

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"

	"github.com/xgo-dev/llgo/cl"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func staticMapNativeCandidateEnabled(conf *Config, debug bool) bool {
	return conf != nil && runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" &&
		conf.Goos == "darwin" && conf.Goarch == "arm64" && conf.Target == "" &&
		conf.Mode != ModeGen && conf.BuildMode == BuildModeExe && !debug &&
		!conf.PackageCompileOnly && !conf.CompileOnly && conf.ImportCfg == "" && !conf.GenLL &&
		(isSizeOptLevel(conf.OptLevel) || conf.LinkOptions.OmitSymbolTable) &&
		!conf.ltoEnabled() && !conf.goGlobalDCEEnabled() && !conf.DeadcodeDropSet && !conf.deadcodeDropEnabled() &&
		conf.ModuleHook == nil && os.Getenv("LLGO_AR") == ""
}

func staticMapEligibleFacts(facts []cl.StaticMapInitializer) []cl.StaticMapInitializer {
	var result []cl.StaticMapInitializer
	for _, fact := range facts {
		if fact.GoUnread && fact.ValueCellsDefined && fact.ConstructorID != "" && fact.GateSymbol != "" {
			result = append(result, fact)
		}
	}
	return result
}

func staticMapOmitSymbols(facts []cl.StaticMapInitializer, producers map[string]cl.StaticMapInitProducer, mentions map[string]bool) []string {
	var result []string
	for _, fact := range staticMapEligibleFacts(facts) {
		want := cl.StaticMapInitProducer{Version: 1, PackageIdentity: fact.PackageIdentity, PhysicalSymbol: fact.PhysicalSymbol,
			ConstructorID: fact.ConstructorID, GateSymbol: fact.GateSymbol, Entries: fact.Entries}
		if cl.ValidateStaticMapInitProducer(want) != nil || producers[fact.GateSymbol] != want ||
			mentions[fact.PhysicalSymbol] || mentions["_"+fact.PhysicalSymbol] ||
			mentions[fact.GateSymbol] || mentions["_"+fact.GateSymbol] {
			continue
		}
		result = append(result, fact.GateSymbol)
	}
	sort.Strings(result)
	return slices.Compact(result)
}

func staticMapCommandDigest(cmd *exec.Cmd) string {
	data, _ := json.Marshal(struct {
		Path, Dir string
		Args, Env []string
	}{cmd.Path, cmd.Dir, cmd.Args, cmd.Env})
	return digestBytes(data)
}

type staticMapLinkCertificate struct {
	commandSHA256 string
	files         []staticMapFileSnapshot
}

func (c *staticMapLinkCertificate) verify(cmd *exec.Cmd) error {
	if c.commandSHA256 == "" || staticMapCommandDigest(cmd) != c.commandSHA256 {
		return fmt.Errorf("static map final command changed")
	}
	for _, file := range c.files {
		if err := file.verify(); err != nil {
			return err
		}
	}
	return nil
}

type staticMapPreparedLink struct {
	command     *exec.Cmd
	certificate staticMapLinkCertificate
	omitted     int
	cleanup     func()
}

func staticMapProofObject(c *context, symbols []string, stub bool) (string, error) {
	program := llssa.NewProgram(newLLSSATarget(c.buildConf, c.crossCompile))
	defer program.Dispose()
	mod := program.NewPackage("static_map_link", "static_map_link").Module()
	mod.SetTarget(program.TargetSpec().Triple)
	mod.SetDataLayout(program.DataLayout())
	ctx := mod.Context()
	if stub {
		// LLVM's verbatim-name prefix fixes the native Darwin C entry name.
		function := llvm.AddFunction(mod, "\x01_main", llvm.FunctionType(ctx.Int32Type(), nil, false))
		builder := ctx.NewBuilder()
		defer builder.Dispose()
		builder.SetInsertPointAtEnd(ctx.AddBasicBlock(function, "entry"))
		builder.CreateRet(llvm.ConstInt(ctx.Int32Type(), 0, false))
	} else {
		for _, symbol := range symbols {
			global := llvm.AddGlobal(mod, ctx.Int8Type(), symbol)
			global.SetInitializer(llvm.ConstInt(ctx.Int8Type(), 0, false))
		}
	}
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", "llgo-static-map-*.o")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	if err := emitNativeObjectFile(program.TargetMachine(), mod, name); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func (c *context) prepareStaticMapLink(driver *exec.Cmd, inputs []string) (_ *staticMapPreparedLink, failure error) {
	if !c.staticMapInitGatesEnabled() || !c.staticMapConsumerFactsFrozen || len(c.staticMapLinkPackages) == 0 ||
		len(staticMapEligibleFacts(c.staticMapConsumerFacts)) == 0 {
		return nil, nil
	}
	if driver == nil || driver.Env == nil || !filepath.IsAbs(driver.Dir) {
		return nil, fmt.Errorf("static map driver environment was not frozen")
	}
	compiler, err := c.staticMapInitCompilerIdentity()
	if err != nil {
		return nil, err
	}
	snapshots := make(map[string]staticMapFileSnapshot)
	addFile := func(path string) (staticMapFileSnapshot, error) {
		if file, ok := snapshots[path]; ok {
			return file, nil
		}
		file, err := staticMapSnapshotFile(path)
		if err == nil {
			snapshots[path] = file
		}
		return file, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	self, err := addFile(executable)
	if err != nil || self.SHA256 != compiler {
		return nil, fmt.Errorf("static map compiler identity changed")
	}
	if _, err := addFile(driver.Path); err != nil {
		return nil, err
	}
	sources := make(map[string]bool)
	for _, path := range inputs {
		if sources[path] || !filepath.IsAbs(path) {
			return nil, fmt.Errorf("ambiguous static map application input")
		}
		sources[path] = true
		if _, err := addFile(path); err != nil {
			return nil, err
		}
	}
	producers := make(map[string]cl.StaticMapInitProducer)
	mentions := make(map[string]bool)
	archives := make(map[string]bool)
	for _, pkg := range c.staticMapLinkPackages {
		if pkg.ArchiveFile == "" {
			continue
		}
		path := pkg.ArchiveFile
		if !sources[path] || archives[path] {
			return nil, fmt.Errorf("unbound static map package archive")
		}
		data, err := encodeStaticMapInitFragment(pkg, compiler, path)
		if err != nil {
			return nil, err
		}
		fragment, err := decodeStaticMapInitFragment(data)
		if err != nil || !fragment.NativeInputsClosed || fragment.ArchiveSHA256 != snapshots[path].SHA256 {
			return nil, fmt.Errorf("static map package input closure is unproved: %s", pkg.PkgPath)
		}
		archives[path] = true
		for _, producer := range fragment.Producers {
			if _, duplicate := producers[producer.GateSymbol]; duplicate {
				return nil, fmt.Errorf("duplicate static map constructor producer")
			}
			producers[producer.GateSymbol] = producer
		}
		for _, symbol := range fragment.NativeMentions {
			mentions[symbol] = true
		}
	}
	for path := range sources {
		if archives[path] {
			continue
		}
		data, err := staticMapReadNativeInput(path)
		if err != nil || digestBytes(data) != snapshots[path].SHA256 {
			return nil, fmt.Errorf("static map extra native input changed")
		}
		symbols, err := staticMapMachOObjectMentions(data)
		if err != nil {
			return nil, err
		}
		for _, symbol := range symbols {
			mentions[symbol] = true
		}
	}
	job, err := staticMapDriverJob(driver)
	if err != nil {
		return nil, err
	}
	linker, err := addFile(job.Path)
	if err != nil {
		return nil, err
	}
	job.Path = linker.RealPath
	plan, err := staticMapParseLDPlan(job.Args[1:], sources)
	if err != nil {
		return nil, err
	}
	stub, err := staticMapProofObject(c, nil, true)
	if err != nil {
		return nil, err
	}
	defer os.Remove(stub)
	probeOutput := stub + ".linked"
	defer os.Remove(probeOutput)
	files, libraryMentions, err := staticMapProbeLibraries(job, plan, stub, probeOutput)
	if err != nil {
		return nil, err
	}
	pinned, err := staticMapPinLibraries(plan, files)
	if err != nil {
		return nil, err
	}
	pinnedPlan, err := staticMapParseLDPlan(pinned, sources)
	if err != nil || len(pinnedPlan.libraries) != 0 {
		return nil, fmt.Errorf("static map libraries were not pinned")
	}
	pinnedFiles, _, err := staticMapProbeLibraries(job, pinnedPlan, stub, probeOutput)
	if err != nil || !staticMapSameFiles(files, pinnedFiles) {
		return nil, fmt.Errorf("pinned static map native library closure changed")
	}
	for _, file := range files {
		if previous, ok := snapshots[file.Path]; ok && previous != file {
			return nil, fmt.Errorf("inconsistent static map input snapshots")
		}
		snapshots[file.Path] = file
	}
	for _, file := range pinnedFiles {
		snapshots[file.Path] = file
	}
	for symbol := range libraryMentions {
		mentions[symbol] = true
	}
	omit := staticMapOmitSymbols(c.staticMapConsumerFacts, producers, mentions)
	if len(omit) == 0 {
		return nil, nil
	}
	override, err := staticMapProofObject(c, omit, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		if failure != nil {
			os.Remove(override)
		}
	}()
	if _, err := addFile(override); err != nil {
		return nil, err
	}
	// The override has only strong mutable byte definitions, without foreign
	// code or automatic-library metadata. It is direct, never archived/cached.
	for i, arg := range pinned {
		if file, ok := snapshots[arg]; ok {
			pinned[i] = file.RealPath
		}
	}
	job.Args = append([]string{job.Args[0]}, append(pinned, override)...)
	certificate := staticMapLinkCertificate{commandSHA256: staticMapCommandDigest(job)}
	for _, file := range snapshots {
		certificate.files = append(certificate.files, file)
	}
	if err := certificate.verify(job); err != nil {
		return nil, err
	}
	return &staticMapPreparedLink{job, certificate, len(omit), func() { os.Remove(override) }}, nil
}
