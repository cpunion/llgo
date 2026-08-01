//go:build !llgo

package build

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCollectArtifacts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	main := write("app.wasm", "main")
	elf := write("app.elf", "elf-data")
	dwarf := write("app.debug.wasm", "debug")
	pcln := write("app.wasm.pclntab", "pcln")
	bin := write("app.bin", "bin")
	hex := write("app.hex", "hex-data")

	tests := []struct {
		name string
		conf Config
		out  OutFmtDetails
		want []Artifact
	}{
		{
			name: "embedded",
			conf: Config{Goarch: "wasm", DebugArtifactMode: DebugArtifactEmbedded},
			out:  OutFmtDetails{Out: main},
			want: []Artifact{{Role: ArtifactRoleDebugDeployment, Format: "wasm", Path: main, Size: 4}},
		},
		{
			name: "external with runtime symbols",
			conf: Config{Goarch: "wasm", DebugArtifactMode: DebugArtifactExternal},
			out:  OutFmtDetails{Out: main, DWARF: dwarf, PCLN: pcln},
			want: []Artifact{
				{Role: ArtifactRoleDeployment, Format: "wasm", Path: main, Size: 4},
				{Role: ArtifactRoleDebug, Format: "wasm-dwarf", Path: dwarf, Size: 5},
				{Role: ArtifactRoleRuntimeSymbols, Format: "pclntab", Path: pcln, Size: 4},
			},
		},
		{
			name: "host and deployment formats",
			conf: Config{Target: "cortex-m-qemu", DebugArtifactMode: DebugArtifactHost},
			out:  OutFmtDetails{Out: elf, Bin: bin, Hex: hex},
			want: []Artifact{
				{Role: ArtifactRoleDebug, Format: "elf", Path: elf, Size: 8},
				{Role: ArtifactRoleDeployment, Format: "bin", Path: bin, Size: 3},
				{Role: ArtifactRoleDeployment, Format: "hex", Path: hex, Size: 8},
			},
		},
		{
			name: "none",
			conf: Config{DebugArtifactMode: DebugArtifactNone},
			out:  OutFmtDetails{Out: main},
			want: []Artifact{{Role: ArtifactRoleDeployment, Format: "wasm", Path: main, Size: 4}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CollectArtifacts(&tt.conf, &tt.out)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("CollectArtifacts() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCollectArtifactsValidation(t *testing.T) {
	if got, err := CollectArtifacts(nil, nil); err != nil || got != nil {
		t.Fatalf("CollectArtifacts(nil) = %#v, %v", got, err)
	}
	if _, err := CollectArtifacts(&Config{}, &OutFmtDetails{}); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("unresolved mode error = %v", err)
	}
	if _, err := CollectArtifacts(&Config{DebugArtifactMode: DebugArtifactNone}, &OutFmtDetails{}); err == nil || !strings.Contains(err.Error(), "path is empty") {
		t.Fatalf("empty primary path error = %v", err)
	}
	if _, err := CollectArtifacts(
		&Config{DebugArtifactMode: DebugArtifactNone},
		&OutFmtDetails{Out: filepath.Join(t.TempDir(), "missing")},
	); err == nil || !strings.Contains(err.Error(), "stat deployment artifact") {
		t.Fatalf("missing artifact error = %v", err)
	}
	dir := t.TempDir()
	if _, err := CollectArtifacts(
		&Config{DebugArtifactMode: DebugArtifactNone},
		&OutFmtDetails{Out: dir},
	); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory artifact error = %v", err)
	}
}

func TestPrimaryArtifactFormat(t *testing.T) {
	tests := []struct {
		name string
		conf Config
		path string
		want string
	}{
		{name: "wasm architecture", conf: Config{Goarch: "wasm"}, path: "app", want: "wasm"},
		{name: "wasm extension", path: "app.WASM", want: "wasm"},
		{name: "target ELF", conf: Config{Target: "cortex-m-qemu"}, path: "app.elf", want: "elf"},
		{name: "archive", conf: Config{Goarch: "wasm", BuildMode: BuildModeCArchive}, path: "libapp.a", want: "archive"},
		{name: "Mach-O", conf: Config{Goos: "darwin", BuildMode: BuildModeCShared}, path: "libapp.dylib", want: "macho"},
		{name: "PE", conf: Config{Goos: "windows"}, path: "app.exe", want: "pe"},
		{name: "ELF", conf: Config{Goos: "linux", BuildMode: BuildModeCShared}, path: "libapp.so", want: "elf"},
		{name: "executable", conf: Config{BuildMode: BuildModeExe}, path: "app", want: "executable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := primaryArtifactFormat(&tt.conf, tt.path); got != tt.want {
				t.Fatalf("primaryArtifactFormat() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReportBuildArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app with space")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	conf := &Config{DebugArtifactMode: DebugArtifactNone, DebugArtifactModeSet: true}
	if err := reportBuildArtifacts(conf, &OutFmtDetails{Out: path}, &report); err != nil {
		t.Fatal(err)
	}
	want := "llgo: artifact role=deployment format=executable size=4 path=" + strconv.Quote(path) + "\n"
	if got := report.String(); got != want {
		t.Fatalf("artifact report = %q, want %q", got, want)
	}

	report.Reset()
	conf.DebugArtifactModeSet = false
	if err := reportBuildArtifacts(conf, &OutFmtDetails{Out: path}, &report); err != nil || report.Len() != 0 {
		t.Fatalf("implicit artifact report = %q, %v", report.String(), err)
	}

	conf.Target = "cortex-m-qemu"
	conf.DebugArtifactMode = DebugArtifactHost
	if err := reportBuildArtifacts(conf, &OutFmtDetails{Out: path}, &report); err != nil {
		t.Fatal(err)
	}
	if got := report.String(); !strings.Contains(got, "role=debug format=elf size=4") {
		t.Fatalf("target artifact report = %q", got)
	}
}
