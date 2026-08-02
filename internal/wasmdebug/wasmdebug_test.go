package wasmdebug

import (
	"bytes"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/debugabi"
)

func appendSection(dst []byte, id byte, payload []byte) []byte {
	dst = append(dst, id)
	dst = appendULEB32(dst, uint32(len(payload)))
	return append(dst, payload...)
}

func appendName(dst []byte, name string) []byte {
	dst = appendULEB32(dst, uint32(len(name)))
	return append(dst, name...)
}

func debugFixture() []byte {
	module := append([]byte(nil), wasmHeader...)
	module = appendCustomSection(module, "producers", []byte("LLGo"))
	module = appendSection(module, 1, []byte{0x01, 0x60, 0x00, 0x00})
	module = appendCustomSection(module, ".debug_info", []byte{1, 2, 3})
	module = appendSection(module, 10, []byte{0x01, 0x02, 0x00, 0x0b})
	module = appendCustomSection(module, ".debug_line", []byte{4, 5, 6})
	module = appendCustomSection(module, externalDebugInfo, appendULEB32(nil, 0))
	return module
}

func TestExternalize(t *testing.T) {
	record := debugabi.NewRecord(2, 4, debugabi.ByteOrderLittle)
	sidecar, err := SetDebuggerRecord(debugFixture(), record)
	if err != nil {
		t.Fatal(err)
	}
	url := strings.Repeat("debug-", 24) + ".wasm"
	main, err := Externalize(sidecar, url)
	if err != nil {
		t.Fatal(err)
	}
	if has, err := HasDWARF(sidecar); err != nil || !has {
		t.Fatalf("sidecar HasDWARF = %v, %v", has, err)
	}
	if has, err := HasDWARF(main); err != nil || has {
		t.Fatalf("main HasDWARF = %v, %v", has, err)
	}
	gotURL, ok, err := ExternalURL(main)
	if err != nil || !ok || gotURL != url {
		t.Fatalf("ExternalURL = %q, %v, %v; want %q", gotURL, ok, err, url)
	}

	originalSections, err := parse(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	mainSections, err := parse(main)
	if err != nil {
		t.Fatal(err)
	}
	var originalStandard, mainStandard []byte
	for _, section := range originalSections {
		if section.id != 0 {
			originalStandard = append(originalStandard, section.raw...)
		}
	}
	for _, section := range mainSections {
		if section.id != 0 {
			mainStandard = append(mainStandard, section.raw...)
		}
	}
	if !bytes.Equal(originalStandard, mainStandard) {
		t.Fatal("externalization changed standard WebAssembly sections")
	}
	if countCustom(mainSections, externalDebugInfo) != 1 {
		t.Fatal("externalization did not replace the old URL section")
	}
	if countCustom(mainSections, "producers") != 1 {
		t.Fatal("externalization removed an unrelated custom section")
	}
	if got, ok, err := DebuggerRecord(main); err != nil || !ok || got != record {
		t.Fatalf("main DebuggerRecord = %+v, %v, %v", got, ok, err)
	}
	if got, ok, err := DebuggerRecord(sidecar); err != nil || !ok || got != record {
		t.Fatalf("sidecar DebuggerRecord = %+v, %v, %v", got, ok, err)
	}
}

func countCustom(sections []section, name string) int {
	count := 0
	for _, section := range sections {
		if section.id == 0 && section.name == name {
			count++
		}
	}
	return count
}

func TestExternalizeErrors(t *testing.T) {
	withoutDWARF := appendSection(append([]byte(nil), wasmHeader...), 1, []byte{0})
	tests := []struct {
		name   string
		module []byte
		url    string
	}{
		{name: "empty URL", module: debugFixture()},
		{name: "invalid URL", module: debugFixture(), url: string([]byte{0xff})},
		{name: "invalid header", module: []byte("not wasm"), url: "app.debug.wasm"},
		{name: "truncated size", module: append(append([]byte(nil), wasmHeader...), 0, 0x80), url: "app.debug.wasm"},
		{name: "oversized section", module: append(append([]byte(nil), wasmHeader...), 1, 2, 0), url: "app.debug.wasm"},
		{name: "no DWARF", module: withoutDWARF, url: "app.debug.wasm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Externalize(tt.module, tt.url); err == nil {
				t.Fatal("Externalize succeeded")
			}
		})
	}
}

func TestExternalURLValidation(t *testing.T) {
	base := append([]byte(nil), wasmHeader...)
	validContent := appendULEB32(nil, 4)
	validContent = append(validContent, "a.wm"...)
	tests := []struct {
		name   string
		module []byte
		ok     bool
		url    string
	}{
		{name: "absent", module: base},
		{name: "valid", module: appendCustomSection(base, externalDebugInfo, validContent), ok: true, url: "a.wm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, ok, err := ExternalURL(tt.module)
			if err != nil || ok != tt.ok || url != tt.url {
				t.Fatalf("ExternalURL = %q, %v, %v", url, ok, err)
			}
		})
	}

	duplicate := appendCustomSection(appendCustomSection(base, externalDebugInfo, validContent), externalDebugInfo, validContent)
	if _, _, err := ExternalURL(duplicate); err == nil {
		t.Fatal("ExternalURL accepted duplicate sections")
	}
	trailing := append(append([]byte(nil), validContent...), 0)
	if _, _, err := ExternalURL(appendCustomSection(base, externalDebugInfo, trailing)); err == nil {
		t.Fatal("ExternalURL accepted trailing data")
	}
}

func TestHasDWARFRejectsMalformedCustomSection(t *testing.T) {
	module := append([]byte(nil), wasmHeader...)
	module = appendSection(module, 0, []byte{2, 'x'})
	if _, err := HasDWARF(module); err == nil {
		t.Fatal("HasDWARF accepted a truncated custom-section name")
	}
}

func TestDebuggerRecordValidation(t *testing.T) {
	base := append([]byte(nil), wasmHeader...)
	record := debugabi.NewRecord(1, 4, debugabi.ByteOrderLittle)
	module, err := SetDebuggerRecord(base, record)
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := SetDebuggerRecord(module, debugabi.NewRecord(2, 4, debugabi.ByteOrderLittle))
	if err != nil {
		t.Fatal(err)
	}
	if sections, err := parse(replaced); err != nil || countCustom(sections, debugabi.WasmSectionName) != 1 {
		t.Fatalf("replacement custom sections = %v, %v", sections, err)
	}
	if got, ok, err := DebuggerRecord(replaced); err != nil || !ok || got.CABIMode != 2 {
		t.Fatalf("DebuggerRecord = %+v, %v, %v", got, ok, err)
	}
	if got, ok, err := DebuggerRecord(base); err != nil || ok || got != (debugabi.Record{}) {
		t.Fatalf("absent DebuggerRecord = %+v, %v, %v", got, ok, err)
	}

	invalid := appendCustomSection(base, debugabi.WasmSectionName, []byte("invalid"))
	if _, _, err := DebuggerRecord(invalid); err == nil {
		t.Fatal("DebuggerRecord accepted invalid content")
	}
	valid, err := record.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	duplicate := appendCustomSection(appendCustomSection(base, debugabi.WasmSectionName, valid), debugabi.WasmSectionName, valid)
	if _, _, err := DebuggerRecord(duplicate); err == nil {
		t.Fatal("DebuggerRecord accepted duplicate sections")
	}
	if _, err := SetDebuggerRecord([]byte("not wasm"), record); err == nil {
		t.Fatal("SetDebuggerRecord accepted an invalid module")
	}
	if _, _, err := DebuggerRecord([]byte("not wasm")); err == nil {
		t.Fatal("DebuggerRecord accepted an invalid module")
	}
	if _, err := SetDebuggerRecord(base, debugabi.Record{}); err == nil {
		t.Fatal("SetDebuggerRecord accepted an invalid record")
	}
}

func TestImportedMemories(t *testing.T) {
	imports := appendULEB32(nil, 3)
	imports = appendName(imports, "wasi_snapshot_preview1")
	imports = appendName(imports, "fd_write")
	imports = append(imports, 0)
	imports = appendULEB32(imports, 7)
	imports = appendName(imports, "env")
	imports = appendName(imports, "memory")
	imports = append(imports, 2)
	imports = appendULEB32(imports, 3) // maximum + shared, wasm32
	imports = appendULEB32(imports, 1024)
	imports = appendULEB32(imports, 1024)
	imports = appendName(imports, "host")
	imports = appendName(imports, "memory64")
	imports = append(imports, 2)
	imports = appendULEB32(imports, 5)                      // maximum + memory64
	imports = append(imports, 0x80, 0x80, 0x80, 0x80, 0x10) // 2^32
	imports = append(imports, 0x81, 0x80, 0x80, 0x80, 0x10) // 2^32 + 1

	module := appendSection(append([]byte(nil), wasmHeader...), 2, imports)
	got, err := ImportedMemories(module)
	if err != nil {
		t.Fatal(err)
	}
	want := []MemoryImport{
		{Module: "env", Name: "memory", Minimum: 1024, Maximum: 1024, HasMax: true, Shared: true},
		{Module: "host", Name: "memory64", Minimum: 1 << 32, Maximum: 1<<32 + 1, HasMax: true, Memory64: true},
	}
	if len(got) != len(want) {
		t.Fatalf("ImportedMemories() = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("ImportedMemories()[%d] = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func TestImportedMemoriesValidation(t *testing.T) {
	valid := appendULEB32(nil, 1)
	valid = appendName(valid, "env")
	valid = appendName(valid, "memory")
	valid = append(valid, 2)
	valid = appendULEB32(valid, 3)
	valid = appendULEB32(valid, 1)
	valid = appendULEB32(valid, 2)

	tests := [][]byte{
		append(valid, 0),
		append(append([]byte(nil), valid[:len(valid)-1]...), 0),
		append(append([]byte(nil), valid[:len(valid)-2]...), 1, 0),
	}
	for index, imports := range tests {
		module := appendSection(append([]byte(nil), wasmHeader...), 2, imports)
		if _, err := ImportedMemories(module); err == nil {
			t.Errorf("ImportedMemories accepted malformed fixture %d", index)
		}
	}

	duplicate := appendSection(append([]byte(nil), wasmHeader...), 2, []byte{0})
	duplicate = appendSection(duplicate, 2, []byte{0})
	if _, err := ImportedMemories(duplicate); err == nil {
		t.Fatal("ImportedMemories accepted duplicate import sections")
	}
}
