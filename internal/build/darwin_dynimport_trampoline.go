package build

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/xgo-dev/llgo/internal/directive"
	"github.com/xgo-dev/llgo/internal/packages"
	extplan9asm "github.com/xgo-dev/plan9asm"
)

// Darwin dynamic imports already have a native object producer. Recognize only
// complete source entries owned by that producer; a void Go PC declaration is
// not the imported C function's argument or result signature.
func darwinDynimportTrampolineSources(ctx *context, pkg *packages.Package, sources []plan9AsmSource) (map[string]bool, error) {
	owned := make(map[string]bool)
	if pkg.Types == nil || ctx.buildConf.Goos != "darwin" || (ctx.buildConf.Goarch != "amd64" && ctx.buildConf.Goarch != "arm64") {
		return owned, nil
	}
	_, imports := collectGoCgoPragmas(pkg.Syntax)
	aliases := make(map[string]string)
	for _, imp := range imports {
		if prior, exists := aliases[imp.local]; exists && prior != imp.alias {
			return nil, fmt.Errorf("%s: conflicting dynamic import %s", pkg.PkgPath, imp.local)
		}
		aliases[imp.local] = imp.alias
	}
	if len(aliases) == 0 {
		return owned, nil
	}
	variables, links, _ := nativeAsmSourceDeclarations(pkg)
	invalidPCUses, addressWrites := darwinTrampolineSourceUses(pkg)
	declarations := make(map[string][]*ast.FuncDecl)
	for _, file := range pkg.Syntax {
		for _, declaration := range file.Decls {
			if fn, ok := declaration.(*ast.FuncDecl); ok {
				declarations[fn.Name.Name] = append(declarations[fn.Name.Name], fn)
			}
		}
	}
	seen := make(map[string]bool)
	definitions := make(map[string]int)
	for _, source := range sources {
		file, err := extplan9asm.Parse(extplan9asm.Arch(ctx.buildConf.Goarch), string(source.source))
		if err != nil || len(file.Funcs) == 0 {
			continue // Preserve the ordinary parser's diagnostic.
		}
		for _, fn := range file.Funcs {
			definitions[strings.TrimSuffix(strings.TrimPrefix(fn.Sym, "·"), "<>")]++
		}
		complete := len(file.UnlinkedPrelude) == 0
		data := make(map[string]extplan9asm.DataStmt)
		globals := make(map[string]extplan9asm.GloblStmt)
		for _, item := range file.Data {
			if _, duplicate := data[item.Sym]; duplicate {
				complete = false
			}
			data[item.Sym] = item
		}
		for _, item := range file.Globl {
			if _, duplicate := globals[item.Sym]; duplicate {
				complete = false
			}
			globals[item.Sym] = item
		}
		var entries []string
		for _, fn := range file.Funcs {
			if len(fn.Instrs) != 2 || fn.Instrs[1].Op != "JMP" || len(fn.Instrs[1].Args) != 1 {
				complete = false
				break
			}
			jump := fn.Instrs[1].Args[0]
			local := strings.TrimSuffix(jump.Sym, "(SB)")
			alias := aliases[local]
			text := strings.Split(fn.Instrs[0].Raw, ",")
			validImport := token.IsIdentifier(local) && alias != "" && alias != local
			validJump := jump.Kind == extplan9asm.OpSym && jump.Sym == local+"(SB)"
			if !validImport || !validJump || len(text) != 3 || strings.TrimSpace(text[2]) != "$0-0" {
				complete = false
				break
			}
			flags, err := strconv.ParseInt(strings.TrimSpace(text[1]), 0, 64)
			if err != nil || flags != 4 { // NOSPLIT, with no other TEXT role.
				complete = false
				break
			}
			name := local + "_trampoline"
			switch fn.Sym {
			case "·" + name:
				decls := declarations[name]
				actual, ok := pkg.Types.Scope().Lookup(name).(*types.Func)
				if !ok || len(decls) != 1 {
					complete = false
					break
				}
				decl := decls[0]
				if decl.Body != nil || decl.Recv != nil || links[name] != "" || actual.Pos() != decl.Name.Pos() {
					complete = false
					break
				}
				if decl.Type == nil || decl.Type.Params.NumFields() != 0 ||
					decl.Type.Results.NumFields() != 0 || decl.Type.TypeParams.NumFields() != 0 {
					complete = false
					break
				}
				sig := actual.Type().(*types.Signature)
				if sig.Params().Len() != 0 || sig.Results().Len() != 0 || sig.Variadic() || sig.TypeParams().Len() != 0 || invalidPCUses[name] {
					complete = false
				}
				for _, item := range directive.ParseGroup(decl.Doc) {
					if strings.HasPrefix(item.Name, "llgo:") || item.Name == "export" {
						complete = false
					}
				}
			case name + "<>":
				address := "·" + name + "_addr"
				word, wordOK := data[address]
				global, globalOK := globals[address]
				variable := variables[name+"_addr"]
				validWord := wordOK && word.Off == 0 && word.Width == 8 && word.Addr == fn.Sym+"(SB)"
				validGlobal := globalOK && strings.TrimSpace(global.Flags) == "8" && global.Size == 8
				validVariable := variable != nil && variable.Type() == types.Typ[types.Uintptr] &&
					links[name+"_addr"] == "" && !addressWrites[name+"_addr"]
				if !validWord || !validGlobal || !validVariable {
					complete = false
				}
				delete(data, address)
				delete(globals, address)
			default:
				complete = false
			}
			entries = append(entries, name)
		}
		if !complete || len(data) != 0 || len(globals) != 0 {
			continue
		}
		for _, name := range entries {
			if seen[name] {
				return nil, fmt.Errorf("%s: duplicate dynamic-import trampoline %s", pkg.PkgPath, name)
			}
			seen[name] = true
		}
		owned[source.path] = true
	}
	for name := range seen {
		if definitions[name] != 1 {
			return nil, fmt.Errorf("%s: duplicate dynamic-import TEXT %s", pkg.PkgPath, name)
		}
	}
	return owned, nil
}

// Inspect uses once per selected package, rather than rescanning all Go files
// for each generated trampoline. These maps live only for this source capture.
func darwinTrampolineSourceUses(pkg *packages.Package) (map[string]bool, map[string]bool) {
	allowed := make(map[*ast.Ident]bool)
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 || pkg.TypesInfo == nil {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			callee, ok := pkg.TypesInfo.Uses[selector.Sel].(*types.Func)
			if ok && callee.Pkg() != nil && callee.Pkg().Path() == "internal/abi" && callee.Name() == "FuncPCABI0" {
				if arg, ok := ast.Unparen(call.Args[0]).(*ast.Ident); ok {
					allowed[arg] = true
				}
			}
			return true
		})
	}
	invalidPCUses := make(map[string]bool)
	addressWrites := make(map[string]bool)
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			id, ok := node.(*ast.Ident)
			if ok && strings.HasSuffix(id.Name, "_trampoline") && !allowed[id] {
				if fn, ok := pkg.Types.Scope().Lookup(id.Name).(*types.Func); ok && id.Pos() != fn.Pos() && (pkg.TypesInfo == nil || pkg.TypesInfo.Uses[id] == fn) {
					invalidPCUses[id.Name] = true
				}
			}
			var targets []ast.Expr
			switch item := node.(type) {
			case *ast.AssignStmt:
				targets = item.Lhs
			case *ast.IncDecStmt:
				targets = []ast.Expr{item.X}
			case *ast.RangeStmt:
				if item.Tok == token.ASSIGN {
					targets = []ast.Expr{item.Key, item.Value}
				}
			case *ast.UnaryExpr:
				if item.Op == token.AND {
					targets = []ast.Expr{item.X}
				}
			}
			for _, target := range targets {
				if target == nil {
					continue
				}
				if id, ok := ast.Unparen(target).(*ast.Ident); ok && strings.HasSuffix(id.Name, "_trampoline_addr") {
					if variable, ok := pkg.Types.Scope().Lookup(id.Name).(*types.Var); ok && (pkg.TypesInfo == nil || pkg.TypesInfo.Uses[id] == variable) {
						addressWrites[id.Name] = true
					}
				}
			}
			return true
		})
	}
	return invalidPCUses, addressWrites
}
