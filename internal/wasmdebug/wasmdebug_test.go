package wasmdebug

import (
	"bytes"
	"strings"
	"testing"
)

func appendSection(dst []byte, id byte, payload []byte) []byte {
	dst = append(dst, id)
	dst = appendULEB32(dst, uint32(len(payload)))
	return append(dst, payload...)
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
	sidecar := debugFixture()
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
