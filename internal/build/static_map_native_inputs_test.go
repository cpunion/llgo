//go:build !llgo

package build

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func staticMapNativeObjectFixture(t *testing.T, symbol string, define bool) []byte {
	t.Helper()
	llssa.Initialize(llssa.InitAll)
	program := llssa.NewProgram(&llssa.Target{GOOS: "darwin", GOARCH: "arm64"})
	defer program.Dispose()
	mod := program.NewPackage("native", "native").Module()
	mod.SetTarget(program.TargetSpec().Triple)
	mod.SetDataLayout(program.DataLayout())
	ctx := mod.Context()
	global := llvm.AddGlobal(mod, ctx.Int64Type(), symbol)
	if define {
		global.SetInitializer(llvm.ConstInt(ctx.Int64Type(), 1, false))
	}
	function := llvm.AddFunction(mod, "read_"+symbol, llvm.FunctionType(ctx.Int64Type(), nil, false))
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(ctx.AddBasicBlock(function, "entry"))
	builder.CreateRet(builder.CreateLoad(ctx.Int64Type(), global, "read"))
	buffer, err := program.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Dispose()
	return append([]byte(nil), buffer.Bytes()...)
}

func TestStaticMapNativeArchiveActualRawReferences(t *testing.T) {
	// Raw LLVM fixture names are already physical object names; do not rely
	// on a frontend's target-specific source-symbol prefixing policy.
	own := staticMapNativeObjectFixture(t, "_owner.M", true)
	raw := staticMapNativeObjectFixture(t, "_unicode.Scripts", false)
	path := writeCoroArchiveTestFile(t, coroArchiveTestMember{"own.o", own}, coroArchiveTestMember{"raw.o", raw})
	owned := staticMapObjectDigest{SHA256: digestBytes(own), Bytes: int64(len(own))}
	result, err := inspectStaticMapNativeArchive(path, []staticMapObjectDigest{owned})
	if err != nil || result.OwnObjects != 1 || result.Members != 2 || !slices.Contains(result.Mentions, "_unicode.Scripts") || slices.Contains(result.Mentions, "_owner.M") {
		t.Fatalf("actual native archive: %+v, %v", result, err)
	}
	if all, err := inspectStaticMapNativeArchive(path, nil); err != nil || !slices.Contains(all.Mentions, "_owner.M") {
		t.Fatalf("foreign library must inventory all members: %+v, %v", all, err)
	}
	for _, name := range []string{"changed-digest", "changed-size", "duplicate-receipt", "missing-owned", "duplicate-owned", "opaque-member"} {
		t.Run(name, func(t *testing.T) {
			objects := []staticMapObjectDigest{owned}
			archive := path
			switch name {
			case "changed-digest":
				objects[0].SHA256 = digestBytes([]byte("different object"))
			case "changed-size":
				objects[0].Bytes--
			case "duplicate-receipt":
				objects = append(objects, owned)
			case "missing-owned":
				archive = writeCoroArchiveTestFile(t, coroArchiveTestMember{"raw.o", raw})
			case "duplicate-owned":
				archive = writeCoroArchiveTestFile(t, coroArchiveTestMember{"one.o", own}, coroArchiveTestMember{"two.o", own})
			case "opaque-member":
				archive = writeCoroArchiveTestFile(t, coroArchiveTestMember{"own.o", own}, coroArchiveTestMember{"opaque.bc", []byte("BC\xc0\xdeunknown raw bitcode")})
			}
			if _, err := inspectStaticMapNativeArchive(archive, objects); err == nil {
				t.Fatal("accepted an unproved native archive")
			}
		})
	}
	for _, name := range []string{"automatic-link-input", "indirect-alias", "foreign-subtype"} {
		t.Run(name, func(t *testing.T) {
			changed := append([]byte(nil), raw...)
			object, err := macho.NewFile(bytes.NewReader(changed))
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "automatic-link-input":
				object.ByteOrder.PutUint32(changed[32:36], 0x2d) // LC_LINKER_OPTION
			case "indirect-alias":
				changed[object.Symtab.Symoff+4] = 0x0b // N_INDR | N_EXT
			case "foreign-subtype":
				object.ByteOrder.PutUint32(changed[8:12], 2) // arm64e
			}
			object.Close()
			if _, err := staticMapMachOObjectMentions(changed); err == nil {
				t.Fatal("accepted an unproved native object")
			}
		})
	}
}

func staticMapUniversalArchiveFixture(t *testing.T) []byte {
	t.Helper()
	data := make([]byte, 136)
	binary.BigEndian.PutUint32(data[:4], 0xcafebabe)
	binary.BigEndian.PutUint32(data[4:8], 2)
	for i, cpu := range []macho.Cpu{macho.CpuAmd64, macho.CpuArm64} {
		h := data[8+i*20 : 28+i*20]
		binary.BigEndian.PutUint32(h[:4], uint32(cpu))
		if i == 0 {
			binary.BigEndian.PutUint32(h[4:8], 3)
		}
		offset := uint32(64 + i*64)
		binary.BigEndian.PutUint32(h[8:12], offset)
		binary.BigEndian.PutUint32(h[12:16], 8)
		binary.BigEndian.PutUint32(h[16:20], 6)
		copy(data[offset:], "!<arch>\n")
	}
	return data
}

func TestStaticMapNativeArchiveUniversalSelectionAndFraming(t *testing.T) {
	data := staticMapUniversalArchiveFixture(t)
	if result, err := inspectStaticMapNativeArchiveBytes(data, nil); err != nil || result.Members != 0 || result.SHA256 != digestBytes(data) {
		t.Fatalf("universal archive: %+v %v", result, err)
	}
	for _, name := range []string{"truncated", "arm64e-only", "duplicate-arm64", "overlap", "out-of-range", "bad-alignment", "short-header", "unsupported-archive", "truncated-member", "oversized-name"} {
		t.Run(name, func(t *testing.T) {
			changed := append([]byte(nil), data...)
			switch name {
			case "truncated":
				changed = changed[:130]
			case "arm64e-only":
				binary.BigEndian.PutUint32(changed[32:36], 2)
			case "duplicate-arm64":
				binary.BigEndian.PutUint32(changed[8:12], uint32(macho.CpuArm64))
				binary.BigEndian.PutUint32(changed[12:16], 0)
			case "overlap":
				binary.BigEndian.PutUint32(changed[36:40], 64)
			case "out-of-range":
				binary.BigEndian.PutUint32(changed[40:44], 1000)
			case "bad-alignment":
				binary.BigEndian.PutUint32(changed[44:48], 31)
			case "short-header":
				binary.BigEndian.PutUint32(changed[4:8], 64)
			case "unsupported-archive":
				copy(changed[128:], "!<thin>\n")
			case "truncated-member", "oversized-name":
				changed = []byte("!<arch>\n" + "x.o/            " + "0           " + "0     " + "0     " + "100644  " + "1         " + "`\n")
				if name == "oversized-name" {
					copy(changed[8:24], "#1/999999999    ")
					changed = append(changed, 'x', '\n')
				}
			}
			if _, err := inspectStaticMapNativeArchiveBytes(changed, nil); err == nil {
				t.Fatal("accepted malformed universal/native archive framing")
			}
		})
	}
}

func TestStaticMapNativeArchiveActualCompilerRuntime(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("actual compiler runtime fixture requires native Darwin ARM64")
	}
	config := os.Getenv("LLVM_CONFIG")
	if config == "" {
		t.Skip("LLVM_CONFIG is not set")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(config), "../lib/clang/22/lib/darwin/libclang_rt.osx.a"))
	result, err := inspectStaticMapNativeArchive(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Members < 200 || result.OwnObjects != 0 || len(result.Mentions) == 0 || slices.Contains(result.Mentions, "_unicode.Scripts") {
		t.Fatalf("incomplete actual compiler runtime inventory: %+v", result)
	}
	t.Logf("compiler runtime: %d ARM64 members, %d distinct symbols, sha256 %s", result.Members, len(result.Mentions), result.SHA256)
}
