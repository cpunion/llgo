//go:build !llgo

package coro

import "testing"

func TestSSABorrowedAllocationPreservesRepeatedObjectIdentity(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "borrowed_loop_identity.go", `package coroid
func selectHistory(ch chan int) int {
	var pointers [2]*int
	for i := range pointers { select { case x := <-ch: pointers[i] = &x } }
	return *pointers[0] + *pointers[1]
}
func arrayHistory() int {
	var pointers [2]*int
	for i := range pointers { x := i; pointers[i] = &x }
	return *pointers[0] + *pointers[1]
}
func conditionalHistory(skip bool) int {
	var pointers [2]*int
	for i := range pointers { if skip { continue }; x := i; pointers[i] = &x }
	if skip { return 0 }; return *pointers[0] + *pointers[1]
}
func nestedHistory() int {
	var pointers [2][2]*int
	for i := range pointers { for j := range pointers[i] { x := i+j; pointers[i][j] = &x } }
	return *pointers[0][0] + *pointers[1][1]
}
func phiHistory(count int) bool {
	var previous *int
	equal := false
	for i := 0; i < count; i++ {
		x := i
		if previous != nil { equal = equal || previous == &x }
		previous = &x
	}
	return equal
}
func observe(p *int) int { return *p }
func immediateBorrow(count int) int {
	result := 0
	for i := 0; i < count; i++ { x := i; result += observe(&x) }
	return result
}
func sameIterationPhi(count int, choose bool) int {
	result := 0
	for i := 0; i < count; i++ {
		x := i
		p := &x
		if choose { p = nil }
		if p != nil { result += observe(p) }
	}
	return result
}
func onceBeforeLoop(count int) int {
	x := 1
	var pointers [2]*int
	for i := 0; i < count; i++ { pointers[i%2] = &x }
	return *pointers[0]
}
type transaction struct { self *transaction; value int }
func begin(out *transaction, value int) { *out = transaction{self: out, value: value} }
func check(out *transaction) int { if out.self != out { panic("identity") }; return out.value }
func transactionBorrow(count int) int {
	result := 0
	for i := 0; i < count; i++ { var x transaction; begin(&x, i); result += check(&x) }
	return result
}
`)
	for _, test := range []struct {
		name string
		want bool
	}{
		{"selectHistory", false}, {"arrayHistory", false}, {"phiHistory", false},
		{"conditionalHistory", false}, {"nestedHistory", false},
		{"immediateBorrow", true}, {"transactionBorrow", true},
		{"sameIterationPhi", true}, {"onceBeforeLoop", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			allocation := exactHeapAllocation(t, packageFunction(t, pkg, test.name))
			if _, got := ProveSSABorrowedAllocation(allocation); got != test.want {
				t.Fatalf("repeatable allocation %s received reusable-slot proof=%v, want %v", allocation, got, test.want)
			}
		})
	}
}
