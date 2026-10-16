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
	"strings"
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

func TestMfinalManagedCallUsesCanonicalDescriptor(t *testing.T) {
	file := parseMfinalQueueSource(t)
	call := mfinalSourceFunc(t, file, "callFinalizer")
	if mfinalCallCount(call.Body, "ffi.CallLLGo") != 1 {
		t.Fatal("managed finalizer call does not use exactly one canonical descriptor bridge")
	}
	if mfinalCallCount(call.Body, "finalizerFuncType") != 1 {
		t.Fatal("managed finalizer call does not preserve the exact runtime function type")
	}

	source, err := os.ReadFile(mfinalQueueSource)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "func callFinalizer(")
	end := strings.Index(string(source)[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("cannot isolate callFinalizer source")
	}
	body := string(source)[start : start+end]
	if strings.Contains(body, "*(*func(") {
		t.Fatal("managed finalizer call still retypes a descriptor-backed function value")
	}
}

func mfinalCallCount(node ast.Node, path string) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if mfinalSelectorPath(call.Fun) == path {
			count++
		}
		return true
	})
	return count
}

func mfinalSelectorPath(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		prefix := mfinalSelectorPath(value.X)
		if prefix == "" {
			return value.Sel.Name
		}
		return prefix + "." + value.Sel.Name
	default:
		return ""
	}
}

func TestMfinalManagedRegistryUsesSchedulerAwareGate(t *testing.T) {
	file := parseMfinalQueueSource(t)
	lock := mfinalSourceFunc(t, file, "Lock")
	unlock := mfinalSourceFunc(t, file, "Unlock")
	set := mfinalSourceFunc(t, file, "SetFinalizer")
	drain := mfinalSourceFunc(t, file, "drainFinalizers")
	callback := mfinalSourceFunc(t, file, "setFinalizerCallback")

	loops := 0
	ast.Inspect(lock.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.ForStmt); ok {
			loops++
		}
		return true
	})
	if loops != 1 ||
		mfinalCallCount(lock.Body, "atomic.CompareAndExchange") != 1 ||
		mfinalCallCount(lock.Body, "coroSchedulerYield") != 1 {
		t.Fatal("finalizer registry gate must CAS once per retry and yield the stackless contender")
	}
	if mfinalCallCount(unlock.Body, "atomic.CompareAndExchange") != 1 ||
		mfinalCallCount(unlock.Body, "throw") != 1 {
		t.Fatal("finalizer registry gate release is not one checked CAS")
	}
	for _, transaction := range []struct {
		fn    *ast.FuncDecl
		count int
	}{
		{set, 1},
		// Draining removes the finalizer registration and, independently,
		// claims a registered cleanup before invoking user code unlocked.
		{drain, 2},
	} {
		if mfinalCallCount(transaction.fn.Body, "finalizerState.registry.Lock") != transaction.count ||
			mfinalCallCount(transaction.fn.Body, "finalizerState.registry.Unlock") != transaction.count {
			t.Fatalf("%s does not own exactly %d finalizer registry transactions", transaction.fn.Name, transaction.count)
		}
	}
	if mfinalCallCount(callback.Body, "finalizerState.registry.Lock") != 0 ||
		mfinalCallCount(callback.Body, "finalizerState.registry.Unlock") != 0 {
		t.Fatal("raw collector callback enters the managed finalizer registry gate")
	}

	source, err := os.ReadFile(mfinalQueueSource)
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if strings.Count(text, "finalizerState.m[") != 3 ||
		strings.Count(text, "delete(finalizerState.m") != 2 {
		t.Fatal("finalizer registry map gained an unaudited access path")
	}
}

func TestMfinalBubbleDrainTransfersOwnershipWithoutChangingCaller(t *testing.T) {
	file := parseMfinalQueueSource(t)
	entry := mfinalSourceFunc(t, file, "runFinalizers")
	drain := mfinalSourceFunc(t, file, "drainFinalizers")
	if mfinalCallCount(entry.Body, "coro.ExecutionDomainEnabled") != 1 ||
		mfinalCallCount(entry.Body, "llruntime.GoOutsideExecutionDomain") != 1 ||
		mfinalCallCount(entry.Body, "drainFinalizers") != 1 {
		t.Fatal("drain entry must specialize away optional bubble dispatch and retain the ordinary inline drain")
	}
	if mfinalCallCount(entry.Body, "releaseFinalizerDrain") != 0 ||
		mfinalCallCount(drain.Body, "releaseFinalizerDrain") != 1 {
		t.Fatal("finalizer drain ownership was released by the publishing caller instead of the drain")
	}
	if mfinalCallCount(entry.Body, "llruntime.LeaveExecutionDomain") != 0 {
		t.Fatal("collector entry must not detach the requesting goroutine from its bubble")
	}
}

func TestMfinalRawProducerIsBoundedAtomicMPSC(t *testing.T) {
	file := parseMfinalQueueSource(t)
	enqueue := mfinalSourceFunc(t, file, "enqueueFinalizerEntry")
	callback := mfinalSourceFunc(t, file, "setFinalizerCallback")
	dequeue := mfinalSourceFunc(t, file, "dequeueFinalizerEntry")
	drain := mfinalSourceFunc(t, file, "runFinalizers")
	ownedDrain := mfinalSourceFunc(t, file, "drainFinalizers")
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

	var claims, cleanupClaims, deferredReleases int
	bodies := &ast.BlockStmt{List: []ast.Stmt{drain.Body, ownedDrain.Body}}
	ast.Inspect(bodies, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, packageOK := selector.X.(*ast.Ident)
			if packageOK && pkg.Name == "atomic" && selector.Sel.Name == "CompareAndExchange" {
				if len(value.Args) != 3 {
					t.Fatal("drain CAS has an unexpected signature")
				}
				address, ok := value.Args[0].(*ast.UnaryExpr)
				if !ok || address.Op != token.AND {
					t.Fatal("drain CAS does not address an owned state field")
				}
				switch target := mfinalSelectorPath(address.X); target {
				case "finalizerState.draining":
					claims++
				case "entry.state":
					cleanupClaims++
				default:
					t.Fatalf("drain has an unaudited CAS on %s", target)
				}
			}
		case *ast.DeferStmt:
			callee, ok := value.Call.Fun.(*ast.Ident)
			if ok && callee.Name == "releaseFinalizerDrain" {
				deferredReleases++
			}
		}
		return true
	})
	if claims != 1 || cleanupClaims != 1 || deferredReleases != 1 {
		t.Fatalf("managed drain ownership = claims:%d cleanup claims:%d deferred releases:%d, want one each", claims, cleanupClaims, deferredReleases)
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
