//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/typepatch"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llgo/ssa/abi"
	"golang.org/x/tools/go/ssa"
)

func TestStaticMapInitializerFrozenFacts(t *testing.T) {
	const path = "example.com/emission/mapfacts"
	for _, tc := range []struct {
		name, source string
		profile      CoroRawDataSymbolProfile
		unread, raw  bool
	}{
		{"unused", `var M = map[string]int{"a": 1}`, CoroRawDataSymbolProfile{Complete: true}, true, true},
		{"read", `var M = map[string]int{"a": 1}; func Read() int { return M["a"] }`, CoroRawDataSymbolProfile{Complete: true}, false, true},
		{"address", `var M = map[string]int{"a": 1}; var Address = &M`, CoroRawDataSymbolProfile{Complete: true}, false, true},
		{"alias-load", `var M = map[string]int{"a": 1}; var Alias = M`, CoroRawDataSymbolProfile{Complete: true}, false, true},
		{"overwrite", `var M = map[string]int{"a": 1}; func Clear() { M = nil }`, CoroRawDataSymbolProfile{Complete: true}, false, true},
		{"raw-open", `var M = map[string]int{"a": 1}`, CoroRawDataSymbolProfile{Blockers: []string{"opaque archive"}}, true, false},
		{"raw-read", `var M = map[string]int{"a": 1}`, CoroRawDataSymbolProfile{Complete: true, Mentions: []string{path + ".M"}}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			test := newEmissionTestProgram()
			pkg := test.addPackage(t, path, "package mapfacts\n"+tc.source)
			test.ssa.Build()
			prog := llssa.NewProgram(nil)
			prog.TypeSizes(types.SizesFor("gc", "arm64"))
			defer prog.Dispose()
			u, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "map-owner", RawDataSymbols: tc.profile}})
			if err != nil {
				t.Fatal(err)
			}
			facts, err := u.StaticMapInitializers()
			if err != nil || len(facts) != 1 {
				t.Fatalf("facts=%+v err=%v; want one pure constructor", facts, err)
			}
			f := facts[0]
			if f.GoUnread != tc.unread || f.RawInputsClosed != tc.raw || !f.ValueCellsDefined || f.Entries != 1 || f.PhysicalSymbol != path+".M" || len(f.ConstructorID) != 64 {
				t.Fatalf("facts=%+v; want unread/raw=%t/%t", f, tc.unread, tc.raw)
			}
			// Maps never acquire the coroutine function-cell certificate.
			if _, certified, err := u.CoroGlobalPhysicalIdentity(emissionTestGlobal(t, pkg, "M")); err != nil || certified {
				t.Fatalf("map acquired function-cell authority: certified=%v err=%v", certified, err)
			}
			if len(f.Blockers) != 0 {
				facts[0].Blockers[0] = "caller mutation"
				again, err := u.StaticMapInitializers()
				if err != nil || again[0].Blockers[0] == "caller mutation" {
					t.Fatal("caller changed the frozen blockers")
				}
			}
		})
	}
}

func TestStaticMapInitializerCrossPackageAndLinkname(t *testing.T) {
	const path = "example.com/emission/mapowner"
	for _, tc := range []struct {
		name, other       string
		linkname, rawOpen bool
		wantPlans         int
		unread, raw       bool
	}{
		{"cross-read", `import "example.com/emission/mapowner"; func Read() int { return mapowner.M["a"] }`, false, false, 1, false, true},
		{"unrelated-raw-open", `var Other int`, false, true, 1, true, false},
		{"linkname-alias", `var Alias map[string]int`, true, false, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			test := newEmissionTestProgram()
			owner := test.addPackage(t, path, `package mapowner; var M = map[string]int{"a": 1}`)
			other := test.addPackage(t, "example.com/emission/mapuser", "package mapuser\n"+tc.other)
			test.ssa.Build()
			prog := llssa.NewProgram(nil)
			prog.TypeSizes(types.SizesFor("gc", "arm64"))
			defer prog.Dispose()
			if tc.linkname {
				prog.SetLinkname(other.types.Path()+".Alias", path+".M")
			}
			profile := CoroRawDataSymbolProfile{Complete: !tc.rawOpen}
			u, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
				{SSA: owner.ssa, Files: []*ast.File{owner.file}, Identity: "owner", RawDataSymbols: CoroRawDataSymbolProfile{Complete: true}},
				{SSA: other.ssa, Files: []*ast.File{other.file}, Identity: "other", RawDataSymbols: profile},
			})
			if err != nil {
				t.Fatal(err)
			}
			facts, err := u.StaticMapInitializers()
			if err != nil || len(facts) != tc.wantPlans {
				t.Fatalf("facts=%+v err=%v", facts, err)
			}
			if len(facts) != 0 && (facts[0].GoUnread != tc.unread || facts[0].RawInputsClosed != tc.raw) {
				t.Fatalf("facts=%+v", facts)
			}
		})
	}
}

func TestStaticMapInitializerCanonicalIDAndMutation(t *testing.T) {
	build := func(t *testing.T, tail, value string, profile CoroRawDataSymbolProfile) (*EmissionUniverse, emissionTestPackage, func()) {
		t.Helper()
		test := newEmissionTestProgram()
		pkg := test.addPackage(t, "example.com/emission/mapstable", `package mapstable; var V *int; var Other map[string]*int; var M = map[string]*int{"a": `+value+`}; `+tail)
		test.ssa.Build()
		prog := llssa.NewProgram(nil)
		prog.TypeSizes(types.SizesFor("gc", "arm64"))
		u, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "stable", RawDataSymbols: profile}})
		if err != nil {
			prog.Dispose()
			t.Fatal(err)
		}
		return u, pkg, prog.Dispose
	}
	complete := CoroRawDataSymbolProfile{Complete: true}
	u, pkg, dispose := build(t, "func ReadOther() map[string]*int { return Other }", "V", complete)
	defer dispose()
	facts, err := u.StaticMapInitializers()
	if err != nil || len(facts) != 1 || !facts[0].ValueCellsDefined || !facts[0].GoUnread {
		t.Fatalf("facts=%+v err=%v", facts, err)
	}
	id := facts[0].ConstructorID
	other, _, disposeOther := build(t, `func Read() *int { return M["a"] }`, "V", CoroRawDataSymbolProfile{})
	defer disposeOther()
	otherFacts, err := other.StaticMapInitializers()
	if err != nil || len(otherFacts) != 1 || otherFacts[0].ConstructorID != id || otherFacts[0].GoUnread || otherFacts[0].RawInputsClosed {
		t.Fatalf("consumer/raw changes altered canonical producer identity: %+v %v", otherFacts, err)
	}
	changed, _, disposeChanged := build(t, "", "nil", complete)
	defer disposeChanged()
	changedFacts, err := changed.StaticMapInitializers()
	if err != nil || len(changedFacts) != 1 || changedFacts[0].ConstructorID == id {
		t.Fatalf("different constructor retained the old identity: %+v %v", changedFacts, err)
	}
	// A new Go reference after freezing must invalidate the unread snapshot.
	reader := pkg.ssa.Func("ReadOther")
	for _, block := range reader.Blocks {
		for _, instr := range block.Instrs {
			if load, ok := instr.(*ssa.UnOp); ok {
				load.X = emissionTestGlobal(t, pkg, "M")
			}
		}
	}
	if _, err := u.StaticMapInitializers(); err == nil || !strings.Contains(err.Error(), "Go references") {
		t.Fatalf("mutated reference was accepted: %v", err)
	}
	if _, err := (*EmissionUniverse)(nil).StaticMapInitializers(); err == nil {
		t.Fatal("nil universe accepted")
	}
}

func TestStaticMapInitializerPatchAliasesAndValueCells(t *testing.T) {
	for _, tc := range []struct {
		name, original, alternate string
		unread, values            bool
	}{
		{
			"original-read-of-alternate-map",
			`package p; var M map[string]int; func Read() int { return M["a"] }`,
			`package p; var M = map[string]int{"a": 1}`,
			false, true,
		},
		{
			"skipped-original-reference-is-not-absent",
			`package p; var M map[string]int; func Read() int { return M["a"] }`,
			"package p\n//llgo:skip M\ntype PatchControl struct{}\nvar M = map[string]int{\"a\": 1}",
			false, true,
		},
		{
			"conflicting-value-cell",
			`package p; var V *int; var M = map[string]*int{"a": V}`,
			`package p; var V *string`,
			true, false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			test := newEmissionTestProgram()
			const path = "example.com/emission/mappatch"
			original := test.addPackage(t, path, tc.original)
			alternate := test.addPackage(t, abi.PatchPathPrefix+path, tc.alternate)
			test.ssa.Build()
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			prog.TypeSizes(types.SizesFor("gc", "arm64"))
			patches := Patches{path: {Alt: alternate.ssa, Types: typepatch.Clone(alternate.types)}}
			u, err := PrepareEmissionUniverse(prog, patches, []EmissionPackage{{SSA: original.ssa, Files: []*ast.File{original.file, alternate.file}}})
			if err != nil {
				t.Fatal(err)
			}
			facts, err := u.StaticMapInitializers()
			if err != nil || len(facts) != 1 || facts[0].GoUnread != tc.unread || facts[0].ValueCellsDefined != tc.values {
				t.Fatalf("facts=%+v err=%v; want unread/values=%t/%t", facts, err, tc.unread, tc.values)
			}
			if !tc.values && (facts[0].ConstructorID != "" || facts[0].GateSymbol != "") {
				t.Fatal("unproved value cells acquired a reusable gate identity")
			}
		})
	}
}
