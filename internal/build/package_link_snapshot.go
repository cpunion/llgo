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

package build

import (
	"sort"

	"github.com/xgo-dev/llvm"
)

// packageLinkSnapshot is the immutable, backend-independent part of an LLVM
// package that the synthetic entry module and final linker still consume.
//
// Keeping this small value object lets native executable builds dispose every
// package module after writing bitcode. The whole-program SSA/coroutine plan can
// then be released before LLVM instruction selection starts, so frontend and
// backend peaks do not overlap.
type packageLinkSnapshot struct {
	needAbiInit         int
	methodByIndex       []int
	methodByName        []string
	funcInfo            []funcInfoRecord
	pcLineInfo          []pcLineRecord
	definedGlobals      []string
	exportFunctionNames []string
}

func freezePackageLinkSnapshot(pkg *aPackage) {
	if pkg == nil || pkg.LPkg == nil {
		return
	}
	lpkg := pkg.LPkg
	mod := lpkg.Module()
	snapshot := &packageLinkSnapshot{
		needAbiInit: lpkg.NeedAbiInit,
		funcInfo:    readFuncInfo(mod),
		pcLineInfo:  readPCLineInfo(mod),
	}
	pkg.setNeedRuntimeOrPyInit(lpkg.NeedRuntime, lpkg.NeedPyInit)

	snapshot.methodByIndex = make([]int, 0, len(lpkg.MethodByIndex))
	for index := range lpkg.MethodByIndex {
		snapshot.methodByIndex = append(snapshot.methodByIndex, index)
	}
	sort.Ints(snapshot.methodByIndex)

	snapshot.methodByName = make([]string, 0, len(lpkg.MethodByName))
	for name := range lpkg.MethodByName {
		snapshot.methodByName = append(snapshot.methodByName, name)
	}
	sort.Strings(snapshot.methodByName)

	for global := mod.FirstGlobal(); !global.IsNil(); global = llvm.NextGlobal(global) {
		if !global.IsDeclaration() {
			snapshot.definedGlobals = append(snapshot.definedGlobals, global.Name())
		}
	}
	sort.Strings(snapshot.definedGlobals)

	snapshot.exportFunctionNames = make([]string, 0, len(lpkg.ExportFuncs()))
	for _, name := range lpkg.ExportFuncs() {
		if name != "" {
			snapshot.exportFunctionNames = append(snapshot.exportFunctionNames, name)
		}
	}
	sort.Strings(snapshot.exportFunctionNames)
	pkg.LinkSnapshot = snapshot
}

func packageLinkSnapshotOf(pkg *aPackage) *packageLinkSnapshot {
	if pkg == nil {
		return nil
	}
	if pkg.LinkSnapshot == nil && pkg.LPkg != nil {
		freezePackageLinkSnapshot(pkg)
	}
	return pkg.LinkSnapshot
}

func packageNeedAbiInit(pkg *aPackage) int {
	if snapshot := packageLinkSnapshotOf(pkg); snapshot != nil {
		return snapshot.needAbiInit
	}
	return 0
}

func packageMethodIndexes(pkg *aPackage) []int {
	if snapshot := packageLinkSnapshotOf(pkg); snapshot != nil {
		return snapshot.methodByIndex
	}
	return nil
}

func packageMethodNames(pkg *aPackage) []string {
	if snapshot := packageLinkSnapshotOf(pkg); snapshot != nil {
		return snapshot.methodByName
	}
	return nil
}

func packageFuncInfo(pkg *aPackage) []funcInfoRecord {
	if snapshot := packageLinkSnapshotOf(pkg); snapshot != nil {
		return snapshot.funcInfo
	}
	return nil
}

func packagePCLineInfo(pkg *aPackage) []pcLineRecord {
	if snapshot := packageLinkSnapshotOf(pkg); snapshot != nil {
		return snapshot.pcLineInfo
	}
	return nil
}

func packageDefinedGlobals(pkg *aPackage) []string {
	if snapshot := packageLinkSnapshotOf(pkg); snapshot != nil {
		return snapshot.definedGlobals
	}
	return nil
}

func packageExportFunctionNames(pkg *aPackage) []string {
	if snapshot := packageLinkSnapshotOf(pkg); snapshot != nil {
		return snapshot.exportFunctionNames
	}
	return nil
}
