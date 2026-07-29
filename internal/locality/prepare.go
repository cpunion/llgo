/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package locality

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// Prepare rewrites local package initializers into replayable synthetic
// functions and returns updated metadata. It is idempotent for repeated calls,
// including calls made by another compiler Program reusing the same syntax and
// types.Package objects.
func Prepare(fset *token.FileSet, pkgPath string, pkg *types.Package, typeInfo *types.Info, files []*ast.File, vars map[string]Info) (map[string]Info, error) {
	ret := cloneInfo(vars)
	if pkg == nil || typeInfo == nil || !hasLocality(ret) {
		return ret, nil
	}

	nextName := 0
	for order, initializer := range typeInfo.InitOrder {
		_, found, err := initializerLocality(fset, initializer, ret)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		initOrder := order + 1
		if initName := preparedInitName(initializer, ret, initOrder); initName != "" {
			setInitializerNames(initializer, ret, initName, initOrder)
			continue
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("cannot prepare local initializer for package %q without syntax files", pkgPath)
		}
		name := findLocalInitializer(pkg, typeInfo, files, initializer)
		if name == "" {
			for {
				name = fmt.Sprintf("%s%d", InitPrefix, nextName)
				nextName++
				if pkg.Scope().Lookup(name) == nil {
					break
				}
			}
			fnObj, decl := makeLocalInitializer(pkg, typeInfo, name, initializer)
			pkg.Scope().Insert(fnObj)
			files[len(files)-1].Decls = append(files[len(files)-1].Decls, decl)
		}
		setInitializerNames(initializer, ret, qualify(pkgPath, name), initOrder)
	}
	if err := prepareLocalDispatchers(pkgPath, pkg, typeInfo, files, ret); err != nil {
		return nil, err
	}

	return ret, nil
}

// ValidatePrepared verifies that every explicit local initializer was prepared
// before Go SSA construction.
func ValidatePrepared(pkgPath string, vars map[string]Info) error {
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		info := vars[name]
		if info.Locality == None {
			continue
		}
		hasMetadata := info.InitFunc != "" || info.InitOrder != 0 || info.InitDispatch != ""
		prepared := info.InitFunc != "" && info.InitOrder != 0 && info.InitDispatch != ""
		if info.HasInitializer != prepared || !info.HasInitializer && hasMetadata {
			return fmt.Errorf("local variable %s has inconsistent initializer metadata before SSA compilation", qualify(pkgPath, name))
		}
	}
	return nil
}

func initializerLocality(fset *token.FileSet, initializer *types.Initializer, vars map[string]Info) (Kind, bool, error) {
	var kind Kind
	localCount := 0
	for _, variable := range initializer.Lhs {
		info, ok := vars[variable.Name()]
		if !ok || info.Locality == None {
			if localCount != 0 {
				return None, false, errorAt(fset, initializer.Rhs.Pos(), "one initializer cannot mix local and ordinary package variables")
			}
			continue
		}
		if localCount == 0 {
			kind = info.Locality
		} else if kind != info.Locality {
			return None, false, errorAt(fset, initializer.Rhs.Pos(), "one initializer cannot mix thread-local and goroutine-local variables")
		}
		localCount++
	}
	if localCount != 0 && len(initializer.Lhs) != localCount {
		return None, false, errorAt(fset, initializer.Rhs.Pos(), "one initializer cannot mix local and ordinary package variables")
	}
	return kind, localCount != 0, nil
}

func preparedInitName(initializer *types.Initializer, vars map[string]Info, order int) string {
	var name string
	for _, variable := range initializer.Lhs {
		info := vars[variable.Name()]
		if info.InitFunc == "" || info.InitOrder != order {
			return ""
		}
		if name == "" {
			name = info.InitFunc
		} else if name != info.InitFunc {
			return ""
		}
	}
	return name
}

func findLocalInitializer(pkg *types.Package, info *types.Info, files []*ast.File, initializer *types.Initializer) string {
	for _, file := range files {
		for _, node := range file.Decls {
			decl, ok := node.(*ast.FuncDecl)
			if !ok || len(decl.Name.Name) < len(InitPrefix) || decl.Name.Name[:len(InitPrefix)] != InitPrefix || decl.Body == nil || len(decl.Body.List) != 1 {
				continue
			}
			assign, ok := decl.Body.List[0].(*ast.AssignStmt)
			if !ok || assign.Tok != token.ASSIGN || len(assign.Rhs) != 1 || assign.Rhs[0] != initializer.Rhs || len(assign.Lhs) != len(initializer.Lhs) {
				continue
			}
			matches := true
			for i, lhs := range assign.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || info.Uses[ident] != initializer.Lhs[i] {
					matches = false
					break
				}
			}
			if matches {
				if object := pkg.Scope().Lookup(decl.Name.Name); object == info.Defs[decl.Name] {
					return decl.Name.Name
				}
			}
		}
	}
	return ""
}

func makeLocalInitializer(pkg *types.Package, info *types.Info, name string, initializer *types.Initializer) (*types.Func, *ast.FuncDecl) {
	if info.Uses == nil {
		info.Uses = make(map[*ast.Ident]types.Object)
	}
	if info.Defs == nil {
		info.Defs = make(map[*ast.Ident]types.Object)
	}
	lhs := make([]ast.Expr, len(initializer.Lhs))
	for i, variable := range initializer.Lhs {
		ident := ast.NewIdent(variable.Name())
		info.Uses[ident] = variable
		lhs[i] = ident
	}
	nameIdent := ast.NewIdent(name)
	sig := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	fnObj := types.NewFunc(token.NoPos, pkg, name, sig)
	info.Defs[nameIdent] = fnObj
	decl := &ast.FuncDecl{
		Name: nameIdent,
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.AssignStmt{Lhs: lhs, Tok: token.ASSIGN, Rhs: []ast.Expr{initializer.Rhs}},
		}},
	}
	return fnObj, decl
}

func setInitializerNames(initializer *types.Initializer, vars map[string]Info, initName string, order int) {
	for _, variable := range initializer.Lhs {
		info := vars[variable.Name()]
		info.InitFunc = initName
		info.InitOrder = order
		vars[variable.Name()] = info
	}
}

type preparedLocalInitializer struct {
	name  string
	order int
	fn    *types.Func
}

func prepareLocalDispatchers(
	pkgPath string,
	pkg *types.Package,
	info *types.Info,
	files []*ast.File,
	vars map[string]Info,
) error {
	for _, kind := range []Kind{Thread, Goroutine} {
		initializers, err := preparedLocalInitializers(pkgPath, pkg, vars, kind)
		if err != nil {
			return err
		}
		if len(initializers) == 0 {
			continue
		}
		name := preparedDispatchName(pkgPath, vars, kind)
		if name == "" {
			name = findLocalDispatcher(pkg, info, files, kind, initializers)
		}
		if name == "" {
			if len(files) == 0 {
				return fmt.Errorf("cannot prepare local initializer dispatcher for package %q without syntax files", pkgPath)
			}
			prefix := DispatchPrefix + kind.String() + "_"
			for suffix := 0; ; suffix++ {
				name = fmt.Sprintf("%s%d", prefix, suffix)
				if pkg.Scope().Lookup(name) == nil {
					break
				}
			}
			fnObj, decl := makeLocalDispatcher(pkg, info, name, initializers)
			pkg.Scope().Insert(fnObj)
			files[len(files)-1].Decls = append(files[len(files)-1].Decls, decl)
		}
		fullName := qualify(pkgPath, name)
		for variable, local := range vars {
			if local.Locality == kind && local.HasInitializer {
				local.InitDispatch = fullName
				vars[variable] = local
			}
		}
	}
	return nil
}

func preparedLocalInitializers(
	pkgPath string,
	pkg *types.Package,
	vars map[string]Info,
	kind Kind,
) ([]preparedLocalInitializer, error) {
	byOrder := make(map[int]preparedLocalInitializer)
	for _, local := range vars {
		if local.Locality != kind || !local.HasInitializer {
			continue
		}
		name := strings.TrimPrefix(local.InitFunc, pkgPath+".")
		object, _ := pkg.Scope().Lookup(name).(*types.Func)
		if local.InitFunc == "" || local.InitOrder == 0 || object == nil {
			return nil, fmt.Errorf("local initializer %q has incomplete prepared function metadata", local.InitFunc)
		}
		current := preparedLocalInitializer{name: name, order: local.InitOrder, fn: object}
		if previous, exists := byOrder[local.InitOrder]; exists && previous.fn != object {
			return nil, fmt.Errorf(
				"local initializer order %d names both %s and %s",
				local.InitOrder, previous.name, name,
			)
		}
		byOrder[local.InitOrder] = current
	}
	ret := make([]preparedLocalInitializer, 0, len(byOrder))
	for _, initializer := range byOrder {
		ret = append(ret, initializer)
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].order < ret[j].order })
	return ret, nil
}

func preparedDispatchName(pkgPath string, vars map[string]Info, kind Kind) string {
	var fullName string
	for _, local := range vars {
		if local.Locality != kind || !local.HasInitializer {
			continue
		}
		if local.InitDispatch == "" {
			return ""
		}
		if fullName == "" {
			fullName = local.InitDispatch
		} else if fullName != local.InitDispatch {
			return ""
		}
	}
	return strings.TrimPrefix(fullName, pkgPath+".")
}

func findLocalDispatcher(
	pkg *types.Package,
	info *types.Info,
	files []*ast.File,
	kind Kind,
	initializers []preparedLocalInitializer,
) string {
	prefix := DispatchPrefix + kind.String() + "_"
	for _, file := range files {
		for _, node := range file.Decls {
			decl, ok := node.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(decl.Name.Name, prefix) || decl.Body == nil ||
				len(decl.Body.List) != len(initializers) {
				continue
			}
			matches := true
			for index, statement := range decl.Body.List {
				expression, ok := statement.(*ast.ExprStmt)
				if !ok {
					matches = false
					break
				}
				call, ok := expression.X.(*ast.CallExpr)
				if !ok {
					matches = false
					break
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok || len(call.Args) != 0 ||
					info.Uses[ident] != initializers[index].fn {
					matches = false
					break
				}
			}
			if matches && pkg.Scope().Lookup(decl.Name.Name) == info.Defs[decl.Name] {
				return decl.Name.Name
			}
		}
	}
	return ""
}

func makeLocalDispatcher(
	pkg *types.Package,
	info *types.Info,
	name string,
	initializers []preparedLocalInitializer,
) (*types.Func, *ast.FuncDecl) {
	if info.Types == nil {
		info.Types = make(map[ast.Expr]types.TypeAndValue)
	}
	if info.Uses == nil {
		info.Uses = make(map[*ast.Ident]types.Object)
	}
	if info.Defs == nil {
		info.Defs = make(map[*ast.Ident]types.Object)
	}
	body := make([]ast.Stmt, len(initializers))
	for index, initializer := range initializers {
		ident := ast.NewIdent(initializer.name)
		call := &ast.CallExpr{Fun: ident}
		info.Uses[ident] = initializer.fn
		info.Types[ident] = types.TypeAndValue{Type: initializer.fn.Type()}
		info.Types[call] = types.TypeAndValue{
			Type: initializer.fn.Signature().Results(),
		}
		body[index] = &ast.ExprStmt{X: call}
	}
	nameIdent := ast.NewIdent(name)
	sig := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	fnObj := types.NewFunc(token.NoPos, pkg, name, sig)
	info.Defs[nameIdent] = fnObj
	return fnObj, &ast.FuncDecl{
		Name: nameIdent,
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: body},
	}
}

func cloneInfo(vars map[string]Info) map[string]Info {
	ret := make(map[string]Info, len(vars))
	for name, info := range vars {
		ret[name] = info
	}
	return ret
}

func hasLocality(vars map[string]Info) bool {
	for _, info := range vars {
		if info.Locality != None {
			return true
		}
	}
	return false
}

func qualify(pkgPath, name string) string {
	if pkgPath == "" {
		return name
	}
	return pkgPath + "." + name
}
