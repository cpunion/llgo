//go:build go1.27

package gotest

import "testing"

type genericMethodValue[A any] struct{ value A }
type genericMethodEmbed[A any] struct{ genericMethodValue[A] }
type genericMethodPointer[A any] struct{ *genericMethodValue[A] }

func (s genericMethodValue[A]) Pair[B any](b B) (A, B)     { return s.value, b }
func (s *genericMethodValue[A]) Pointer[B any](b B) (A, B) { return s.value, b }
func (s genericMethodValue[A]) Slice[B ~[]A](b B) A        { return b[0] }
func (s genericMethodValue[A]) Variadic[B any](b ...B) B   { return b[0] }
func genericMethodExpression[A, B any]() func(genericMethodValue[A], B) (A, B) {
	return genericMethodValue[A].Pair[B]
}
func genericMethodBound[A, B any](s genericMethodValue[A]) func(B) (A, B) {
	return s.Pair[B]
}
func genericMethodPromoted[A, B any]() func(genericMethodEmbed[A], B) (A, B) {
	return genericMethodEmbed[A].Pair[B]
}
func genericMethodPromotedPointer[A, B any]() func(genericMethodPointer[A], B) (A, B) {
	return genericMethodPointer[A].Pointer[B]
}

func TestGenericMethodExpressionAndBound(t *testing.T) {
	s := genericMethodValue[int]{42}
	check := func(a int, b string) {
		t.Helper()
		if a != 42 || b != "ok" {
			t.Fatalf("generic method = (%d, %q)", a, b)
		}
	}
	check(genericMethodExpression[int, string]()(s, "ok"))
	check(genericMethodBound[int, string](s)("ok"))
	check(genericMethodPromoted[int, string]()(genericMethodEmbed[int]{s}, "ok"))
	check(genericMethodPromotedPointer[int, string]()(genericMethodPointer[int]{&s}, "ok"))
	check((*genericMethodValue[int]).Pair[string](&s, "ok"))
	if genericMethodValue[int].Slice[[]int](s, []int{42}) != 42 ||
		genericMethodValue[int].Variadic[string](s, "ok") != "ok" {
		t.Fatal("generic method constraint/variadic projection changed")
	}
}
