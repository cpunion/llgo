package build

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func sizeWasmUint(n uint64) []byte { return binary.AppendUvarint(nil, n) }
func sizeWasmName(s string) []byte { return append(sizeWasmUint(uint64(len(s))), s...) }
func sizeWasmSection(id byte, data []byte) []byte {
	return append(append([]byte{id}, sizeWasmUint(uint64(len(data)))...), data...)
}

func sizeWasmFixture(withNames bool) []byte {
	raw := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	raw = append(raw, sizeWasmSection(1, []byte{1, 0x60, 0, 0})...)
	imports := append([]byte{2}, sizeWasmName("env")...)
	imports = append(imports, sizeWasmName("call")...)
	imports = append(imports, 0, 0) // function, type 0
	imports = append(imports, sizeWasmName("env")...)
	imports = append(imports, sizeWasmName("memory")...)
	imports = append(imports, 2, 3, 2, 4) // shared memory: min 2, max 4 pages
	raw = append(raw, sizeWasmSection(2, imports)...)
	raw = append(raw, sizeWasmSection(3, []byte{1, 0})...)
	raw = append(raw, sizeWasmSection(10, []byte{1, 2, 0, 0x0b})...)
	// Active and passive data, with exactly 3 + 2 stored bytes.
	raw = append(raw, sizeWasmSection(11, []byte{2, 0, 0x41, 0, 0x0b, 3, 'a', 'b', 'c', 1, 2, 'd', 'e'})...)
	if withNames {
		functions := append([]byte{1, 1}, sizeWasmName("main.(*T).Method")...)
		name := append(sizeWasmName("name"), sizeWasmSection(1, functions)...)
		raw = append(raw, sizeWasmSection(0, name)...)
	}
	return raw
}

func TestWasmSizeFinalEncoding(t *testing.T) {
	for _, names := range []bool{false, true} {
		raw := sizeWasmFixture(names)
		report, err := collectWasmSize("app.wasm", raw, nil, "full")
		if err != nil {
			t.Fatal(err)
		}
		if report.Total.Code != 2 || report.Total.Data != 5 || report.Total.BSS != 0 {
			t.Fatalf("payload: %+v", report.Total)
		}
		owner := "(unknown function 1)"
		if names {
			owner = "main.(*T).Method"
		}
		if report.Modules[owner] == nil || report.Modules[owner].Code != 2 {
			t.Fatalf("function attribution: %+v", report.Modules)
		}
		w := report.Wasm
		if got := report.Total.Code + report.Total.Data + w.CustomBytes + w.StructureBytes; got != uint64(len(raw)) {
			t.Fatalf("file accounting = %d, want %d", got, len(raw))
		}
		var sections uint64 = 8
		for _, s := range w.Sections {
			sections += s.Size
		}
		if sections != uint64(len(raw)) {
			t.Fatalf("section accounting = %d", sections)
		}
		if len(w.Memories) != 1 || !w.Memories[0].Imported || !w.Memories[0].Shared || w.Memories[0].InitialBytes != 2*65536 || *w.Memories[0].MaximumBytes != 4*65536 {
			t.Fatalf("memory: %+v", w.Memories)
		}
		var output bytes.Buffer
		if err := emitJSONReport(&output, report); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), `"ram"`) || strings.Contains(output.String(), `"flash"`) {
			t.Fatalf("Wasm reservations reported as RAM/Flash: %s", output.String())
		}
	}
}

func TestWasmSizeMemory64AndImports(t *testing.T) {
	imports := []byte{3}
	for _, kind := range []byte{1, 3, 4} {
		imports = append(imports, sizeWasmName("env")...)
		imports = append(imports, sizeWasmName("item")...)
		imports = append(imports, kind)
		switch kind {
		case 1:
			imports = append(imports, 0x70, 1, 0, 1) // funcref table
		case 3:
			imports = append(imports, 0x63, 0x70, 0) // immutable nullable funcref global
		case 4:
			imports = append(imports, 0, 0) // exception tag
		}
	}
	raw := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	raw = append(raw, sizeWasmSection(2, imports)...)
	raw = append(raw, sizeWasmSection(5, []byte{1, 5, 2, 4})...)
	// Explicit memory index, i64.const offset and zero stored data.
	raw = append(raw, sizeWasmSection(11, []byte{1, 2, 0, 0x42, 0, 0x0b, 0})...)
	report, err := collectWasmSize("64.wasm", raw, nil, "module")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Wasm.Memories) != 1 || !report.Wasm.Memories[0].Memory64 || report.Wasm.Memories[0].Imported {
		t.Fatalf("memory64: %+v", report.Wasm.Memories)
	}
}

func TestWasmSizeTable64LimitsAreElements(t *testing.T) {
	imports := append([]byte{1}, sizeWasmName("env")...)
	imports = append(imports, sizeWasmName("table")...)
	imports = append(imports, 1, 0x70, 5, 0)
	imports = append(imports, sizeWasmUint(1<<63)...)
	raw := append([]byte{0, 'a', 's', 'm', 1, 0, 0, 0}, sizeWasmSection(2, imports)...)
	if report, err := collectWasmSize("table.wasm", raw, nil, "module"); err != nil || len(report.Wasm.Memories) != 0 {
		t.Fatalf("table limits must not be converted to memory bytes: %v", err)
	}
}

func FuzzWasmSizeEncoding(f *testing.F) {
	f.Add(sizeWasmFixture(true))
	f.Add(sizeWasmFixture(false))
	f.Fuzz(func(t *testing.T, raw []byte) {
		report, err := collectWasmSize("fuzz.wasm", raw, nil, "full")
		if err != nil {
			return
		}
		if got := report.Total.Code + report.Total.Data + report.Wasm.CustomBytes + report.Wasm.StructureBytes; got != uint64(len(raw)) {
			t.Fatalf("size accounting: %d != %d", got, len(raw))
		}
	})
}

func TestWasmSizeReaderSignedBoundaries(t *testing.T) {
	for _, tc := range []struct {
		bits uint
		data []byte
		bad  bool
	}{
		{32, []byte{0x80, 0x80, 0x80, 0x80, 0x78}, false},
		{32, []byte{0xff, 0xff, 0xff, 0xff, 0x07}, false},
		{32, []byte{0x80, 0x80, 0x80, 0x80, 0x08}, true},
		{64, []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f}, false},
		{64, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0}, false},
		{64, []byte{0x80}, true},
	} {
		r := wasmSizeReader{data: tc.data}
		r.signed(tc.bits)
		if (r.done() != nil) != tc.bad {
			t.Fatalf("signed%d %x: %v", tc.bits, tc.data, r.done())
		}
	}
}

func TestWasmSizeMalformedAndOptionalNames(t *testing.T) {
	header := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	for _, tail := range [][]byte{
		{10, 5, 1}, {10, 0x80}, {10, 0xff, 0xff, 0xff, 0xff, 0x10},
		sizeWasmSection(10, []byte{1, 3, 0}),
		sizeWasmSection(11, []byte{1, 3}),
		sizeWasmSection(11, []byte{1, 0, 0xff}),
		sizeWasmSection(5, []byte{1, 8}),
		sizeWasmSection(0, []byte{2, 'a'}),
		append(sizeWasmSection(5, []byte{0}), sizeWasmSection(5, []byte{0})...),
	} {
		if _, err := collectWasmSize("bad.wasm", append(bytes.Clone(header), tail...), nil, "module"); err == nil {
			t.Fatalf("accepted malformed tail %x", tail)
		}
	}
	// Optional debug names cannot make an otherwise measurable module fail.
	badNames := append(sizeWasmName("name"), []byte{1, 3, 1}...)
	raw := append(sizeWasmFixture(false), sizeWasmSection(0, badNames)...)
	report, err := collectWasmSize("names.wasm", raw, nil, "module")
	if err != nil || len(report.Warnings) != 1 || report.Total.Code != 2 {
		t.Fatalf("optional names = %+v, %v", report, err)
	}
}

func TestFinalSizeEmscriptenArtifacts(t *testing.T) {
	dir := t.TempDir()
	out := &OutFmtDetails{Out: filepath.Join(dir, "app.mjs"), PCLN: filepath.Join(dir, "app.pclntab")}
	raw := sizeWasmFixture(true)
	for path, data := range map[string][]byte{out.Out: []byte("javascript"), filepath.Join(dir, "app.wasm"): raw, out.PCLN: []byte("pcln")} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	conf := &Config{Mode: ModeBuild, SizeReport: true, SizeFormat: "json", Goos: "js", Goarch: "wasm", DebugArtifactMode: DebugArtifactNone}
	var output bytes.Buffer
	if err := reportBuildOutputs(conf, out, nil, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Version   int
		Stage     string
		Binary    string
		FileSize  uint64 `json:"file_size"`
		Artifacts []Artifact
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != 1 || payload.Stage != "final" || !strings.HasSuffix(payload.Binary, "app.wasm") || payload.FileSize != uint64(len(raw)) || len(payload.Artifacts) != 3 {
		t.Fatalf("final report: %s", output.String())
	}
	if err := os.Remove(out.PCLN); err != nil {
		t.Fatal(err)
	}
	if err := reportBuildOutputs(conf, out, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("missing final artifact must fail report")
	}
}

type sizeFailWriter struct{}

func (sizeFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestSizeReportErrorsAndConfiguration(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		if err := writeSizeReport(sizeFailWriter{}, &sizeReport{}, format); err == nil {
			t.Fatalf("%s write error ignored", format)
		}
	}
	if err := ensureSizeReporting(&Config{SizeReport: true, SizeFormat: "invalid"}); err == nil {
		t.Fatal("invalid format accepted")
	}
	if err := ensureSizeReporting(&Config{SizeReport: true, SizeLevel: "invalid"}); err == nil {
		t.Fatal("invalid level accepted")
	}
	conf := &Config{SizeReport: true, SizeFormat: "JSON", SizeLevel: "FULL"}
	if err := ensureSizeReporting(conf); err != nil || conf.SizeFormat != "json" || conf.SizeLevel != "full" {
		t.Fatalf("normalization: %+v %v", conf, err)
	}
}

func TestFinalSizeTextAndReadErrors(t *testing.T) {
	report, err := collectWasmSize("app.wasm", sizeWasmFixture(true), nil, "full")
	if err != nil {
		t.Fatal(err)
	}
	report.Format = "wasm"
	report.Warnings = []string{"optional names unavailable"}
	report.Artifacts = []Artifact{{Path: "app.wasm", Format: "wasm", Role: ArtifactRoleDeployment, Size: 99}}
	var output bytes.Buffer
	if err := writeSizeReport(&output, report, "text"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"encoded function bodies", "main.(*T).Method", "Memory 0: initial=131072 maximum=262144", "shared=true", "Artifact: 99", "optional names unavailable"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("text missing %q: %s", want, output.String())
		}
	}
	if err := writeSizeReport(io.Discard, report, "bad"); err == nil {
		t.Fatal("unknown report format accepted")
	}
	conf := &Config{Mode: ModeRun, SizeReport: true}
	if err := reportBuildOutputs(conf, &OutFmtDetails{}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	conf.Mode = ModeBuild
	if err := reportBuildOutputs(conf, &OutFmtDetails{Out: filepath.Join(t.TempDir(), "missing")}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("missing binary accepted")
	}
	short := filepath.Join(t.TempDir(), "short")
	if err := os.WriteFile(short, []byte{0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := collectBinarySize(short, nil, "full"); err == nil {
		t.Fatal("truncated header accepted")
	}
	if _, err := collectBinarySize(t.TempDir(), nil, "full"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory size report = %v", err)
	}
}

func TestWasmSizeReadBounds(t *testing.T) {
	raw := sizeWasmFixture(false)
	for _, tc := range []struct {
		name string
		size int64
		bad  bool
	}{
		{"exact", int64(len(raw)), false},
		{"shrunken", int64(len(raw)) + 1, true},
		{"grown", int64(len(raw)) - 1, true},
		{"negative", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readWasmSizeBytes(bytes.NewReader(raw), tc.size)
			if (err != nil) != tc.bad {
				t.Fatalf("read %d bytes: %v", tc.size, err)
			}
			if !tc.bad && !bytes.Equal(got, raw) {
				t.Fatal("artifact bytes changed")
			}
		})
	}
	if strconv.IntSize == 32 {
		if _, err := readWasmSizeBytes(bytes.NewReader(raw), 1<<32); err == nil {
			t.Fatal("file size outside host int range accepted")
		}
	}
}

func TestSizeReportPreservesMethodNames(t *testing.T) {
	for raw, want := range map[string]string{"pkg.(*T).Method (123)": "pkg.(*T).Method", "pkg.(T).Method": "pkg.(T).Method", "__text (5F)": "__text", "name (not an index)": "name (not an index)"} {
		if got := parseNameField(raw); got != want {
			t.Fatalf("%q -> %q, want %q", raw, got, want)
		}
	}
}

func TestCollectFinalSizeRealBinary(t *testing.T) {
	path := os.Getenv("LLGO_SIZE_REPORT_BIN")
	if path == "" {
		t.Skip("set LLGO_SIZE_REPORT_BIN for a final-artifact smoke test")
	}
	report, err := collectBinarySize(path, nil, "full")
	if err != nil {
		t.Fatal(err)
	}
	if report.Wasm != nil && report.Total.Code+report.Total.Data+report.Wasm.StructureBytes+report.Wasm.CustomBytes != report.FileSize {
		t.Fatal("file size does not close")
	}
	var output bytes.Buffer
	if err := emitJSONReport(&output, report); err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("LLGO_SIZE_REPORT_JSON"); path != "" {
		if err := os.WriteFile(path, output.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
