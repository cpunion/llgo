//go:build !llgo

package build

import (
	"runtime"
	"strings"
	"testing"

	buildfuncinfo "github.com/xgo-dev/llgo/internal/build/funcinfo"
	"github.com/xgo-dev/llgo/internal/optlevel"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestFuncInfoRecordStoragePreservesIDsAndLine(t *testing.T) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	for _, allowNarrow := range []bool{false, true} {
		for field := 0; field < 6; field++ {
			for _, id := range []uint32{0, 65535, 65536, ^uint32(0)} {
				ids := [6]uint32{1, 2, 3, 4, 5, 6}
				ids[field] = id
				records := []buildfuncinfo.EncodedRecord{
					{SymbolPkg: 7, SymbolName: 8, NamePkg: 9, NameName: 10, FileRoot: 11, FileName: 12, Line: 42},
					{SymbolPkg: ids[0], SymbolName: ids[1], NamePkg: ids[2], NameName: ids[3], FileRoot: ids[4], FileName: ids[5], Line: 0x80000011},
				}
				_, values, size := funcInfoRecordStorage(ctx, records, allowNarrow)
				wantSize, wantBits := uint8(28), 32
				if allowNarrow && id <= 65535 {
					wantSize, wantBits = 16, 16
				}
				if size != wantSize || len(values) != 2 {
					t.Fatalf("allow=%t field=%d id=%d: size=%d rows=%d", allowNarrow, field, id, size, len(values))
				}
				for row, value := range values {
					for column := 0; column < 7; column++ {
						bits := wantBits
						if column == 6 {
							bits = 32
						}
						got := value.Operand(column)
						if got.Type().IntTypeWidth() != bits {
							t.Fatalf("row=%d column=%d bits=%d want %d", row, column, got.Type().IntTypeWidth(), bits)
						}
					}
				}
				for column, want := range ids {
					if got := values[1].Operand(column).ZExtValue(); got != uint64(want) {
						t.Fatalf("field=%d id=%d column=%d got=%d want=%d", field, id, column, got, want)
					}
				}
				if got := values[1].Operand(6).ZExtValue(); got != 0x80000011 {
					t.Fatalf("line and wrapper flag changed: %#x", got)
				}
			}
		}
	}
	_, values, size := funcInfoRecordStorage(ctx, nil, true)
	if len(values) != 0 || size != 28 {
		t.Fatalf("empty table rows=%d size=%d", len(values), size)
	}
}

func TestFuncInfoRecordEncodingProfile(t *testing.T) {
	t.Setenv("LLGO_AR", "")
	for _, test := range []struct {
		name, goos, goarch string
		level              optlevel.Level
		mode               PCLNMode
		wantNarrow         bool
	}{
		{"native", "darwin", "arm64", optlevel.Os, PCLNEmbedded, runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"},
		{"external", "darwin", "arm64", optlevel.Os, PCLNExternal, false},
		{"disabled", "darwin", "arm64", optlevel.Os, PCLNNone, false},
		{"linux64", "linux", "arm64", optlevel.Os, PCLNEmbedded, false},
		{"linux32", "linux", "386", optlevel.Os, PCLNEmbedded, false},
		{"darwin-amd64", "darwin", "amd64", optlevel.Os, PCLNEmbedded, false},
		{"other-optimization", "darwin", "arm64", optlevel.O2, PCLNEmbedded, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog := llssa.NewProgram(&llssa.Target{GOOS: test.goos, GOARCH: test.goarch})
			defer prog.Dispose()
			pkg := prog.NewPackage("example.com/p", "example.com/p")
			ctx := &context{prog: prog, buildConf: &Config{
				BuildMode: BuildModeExe, Mode: ModeBuild, Goos: test.goos,
				Goarch: test.goarch, OptLevel: test.level, PCLNMode: test.mode,
			}}
			emitFuncInfoTable(ctx, pkg, []funcInfoRecord{{
				symbol: "example.com/p.f", name: "example.com/p.F", file: "file.go", line: 17,
			}}, nil)
			ir := pkg.String()
			want := "@__llgo_funcinfo_record_size = global i8 28"
			if test.wantNarrow {
				want = "@__llgo_funcinfo_record_size = global i8 16"
				if !strings.Contains(ir, "[1 x { i16, i16, i16, i16, i16, i16, i32 }]") {
					t.Fatalf("missing compact record payload:\n%s", ir)
				}
			}
			if !strings.Contains(ir, want) {
				t.Fatalf("missing %q:\n%s", want, ir)
			}
		})
	}
}

func TestFuncInfoRecordStorageLayout(t *testing.T) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	for _, layout := range []string{"e-p:32:32-i64:64-n32", "e-p:64:64-i64:64-n32:64"} {
		td := llvm.NewTargetData(layout)
		for _, narrow := range []bool{false, true} {
			typ, _, size := funcInfoRecordStorage(ctx, []buildfuncinfo.EncodedRecord{{Line: 0x80000011}}, narrow)
			if got := td.TypeAllocSize(typ); got != uint64(size) {
				t.Fatalf("layout=%s narrow=%t: allocation=%d size=%d", layout, narrow, got, size)
			}
			width := uint64(4)
			if narrow {
				width = 2
			}
			for field := 0; field < 7; field++ {
				if got, want := td.ElementOffset(typ, field), uint64(field)*width; got != want {
					t.Fatalf("layout=%s narrow=%t field=%d: offset=%d want=%d", layout, narrow, field, got, want)
				}
			}
		}
		td.Dispose()
	}
}
