//go:build !llgo

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

package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

const mfinalQueueSource = "internal/lib/runtime/mfinal.go"

func parseMfinalQueueSource(t *testing.T) *ast.File {
	t.Helper()
	source, err := os.ReadFile(mfinalQueueSource)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), mfinalQueueSource, source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func mfinalSourceFunc(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("%s lacks function %s", mfinalQueueSource, name)
	return nil
}

func addressedEntryField(expr ast.Expr, field string) bool {
	address, ok := expr.(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return false
	}
	selector, ok := address.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != field {
		return false
	}
	entry, ok := selector.X.(*ast.Ident)
	return ok && entry.Name == "entry"
}

func TestMfinalRegistrationPublishesPreviousChainAtomically(t *testing.T) {
	file := parseMfinalQueueSource(t)
	fn := mfinalSourceFunc(t, file, "registerFinalizerEntry")

	var register *ast.CallExpr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, packageOK := selector.X.(*ast.Ident)
		if packageOK && pkg.Name == "bdwgc" && selector.Sel.Name == "RegisterFinalizer" {
			register = call
			return false
		}
		return true
	})
	if register == nil || len(register.Args) != 5 {
		t.Fatalf("registerFinalizerEntry must contain one five-argument BDWGC registration")
	}
	if !addressedEntryField(register.Args[3], "prevFn") ||
		!addressedEntryField(register.Args[4], "prevCb") {
		t.Fatal("BDWGC previous-finalizer outputs are not written directly into entry")
	}
}

func TestMfinalRawProducerIsBoundedAtomicMPSC(t *testing.T) {
	file := parseMfinalQueueSource(t)
	enqueue := mfinalSourceFunc(t, file, "enqueueFinalizerEntry")
	callback := mfinalSourceFunc(t, file, "setFinalizerCallback")
	dequeue := mfinalSourceFunc(t, file, "dequeueFinalizerEntry")
	drain := mfinalSourceFunc(t, file, "runFinalizers")
	releaseDrain := mfinalSourceFunc(t, file, "releaseFinalizerDrain")

	atomicCalls := map[string]int{}
	forbidden := ""
	ast.Inspect(enqueue.Body, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			forbidden = "retry loop"
		case *ast.GoStmt:
			forbidden = "goroutine spawn"
		case *ast.DeferStmt:
			forbidden = "defer"
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && (ident.Name == "make" || ident.Name == "new" || ident.Name == "append") {
			forbidden = ident.Name
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, packageOK := selector.X.(*ast.Ident)
		if packageOK && pkg.Name == "atomic" {
			atomicCalls[selector.Sel.Name]++
		}
		return true
	})
	if forbidden != "" {
		t.Fatalf("raw finalizer producer contains forbidden %s", forbidden)
	}
	if atomicCalls["Exchange"] != 1 || atomicCalls["Store"] != 2 || len(atomicCalls) != 2 {
		t.Fatalf("raw producer atomics = %v, want exactly one Exchange and two Stores", atomicCalls)
	}

	callbackCallsEnqueue := false
	callbackForbidden := ""
	ast.Inspect(callback.Body, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			callbackForbidden = "loop"
		case *ast.GoStmt:
			callbackForbidden = "goroutine spawn"
		case *ast.DeferStmt:
			callbackForbidden = "defer"
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok {
			switch ident.Name {
			case "enqueueFinalizerEntry":
				callbackCallsEnqueue = true
			case "prevFn":
				// The previous BDWGC callback is a raw C function pointer.
			case "make", "new", "append":
				callbackForbidden = ident.Name
			default:
				callbackForbidden = "call to " + ident.Name
			}
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			pkg, packageOK := selector.X.(*ast.Ident)
			if !packageOK || pkg.Name != "atomic" || selector.Sel.Name != "Load" {
				callbackForbidden = "call to " + selector.Sel.Name
			}
		}
		return true
	})
	if callbackForbidden != "" {
		t.Fatalf("raw finalizer callback contains forbidden %s", callbackForbidden)
	}
	if !callbackCallsEnqueue {
		t.Fatal("raw finalizer callback does not publish through the bounded MPSC producer")
	}

	var exchanges, loads int
	ast.Inspect(dequeue.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, packageOK := selector.X.(*ast.Ident)
		if !packageOK || pkg.Name != "atomic" {
			return true
		}
		switch selector.Sel.Name {
		case "Exchange":
			exchanges++
		case "Load":
			loads++
		}
		return true
	})
	if exchanges != 0 || loads < 3 {
		t.Fatalf("single-consumer dequeue atomics = Exchange:%d Load:%d; want no direct exchange and at least three acquire loads", exchanges, loads)
	}

	var claims, deferredReleases int
	ast.Inspect(drain.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, packageOK := selector.X.(*ast.Ident)
			if packageOK && pkg.Name == "atomic" && selector.Sel.Name == "CompareAndExchange" {
				claims++
			}
		case *ast.DeferStmt:
			callee, ok := value.Call.Fun.(*ast.Ident)
			if ok && callee.Name == "releaseFinalizerDrain" {
				deferredReleases++
			}
		}
		return true
	})
	if claims != 1 || deferredReleases != 1 {
		t.Fatalf("managed drain ownership = claims:%d deferred releases:%d, want one each", claims, deferredReleases)
	}

	releaseStores := 0
	ast.Inspect(releaseDrain.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, packageOK := selector.X.(*ast.Ident)
		if packageOK && pkg.Name == "atomic" && selector.Sel.Name == "Store" {
			releaseStores++
		}
		return true
	})
	if releaseStores != 1 {
		t.Fatalf("drain release wrapper atomic stores = %d, want one exact direct call", releaseStores)
	}
}
