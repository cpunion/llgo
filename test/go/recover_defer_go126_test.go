//go:build go1.26

package gotest

import "testing"

var fixedbug73916Recovered bool

func fixedbug73916CallRecover() {
	if recover() != nil {
		fixedbug73916Recovered = true
	}
}

func fixedbug73916Deferred(int) {
	fixedbug73916CallRecover()
}

func fixedbug73916MustPanic(t *testing.T, fn func()) any {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("deferred indirect recover swallowed panic")
		}
	}()
	fn()
	return nil
}

func TestRecoverFixedbug73916IndirectRecoverDoesNotRecover(t *testing.T) {
	fixedbug73916Recovered = false
	fixedbug73916MustPanic(t, func() {
		defer fixedbug73916Deferred(1)
		panic("fixedbug73916")
	})
	if fixedbug73916Recovered {
		t.Fatal("indirect recover returned non-nil")
	}
}

var fixedbug73916bRecovered bool

func fixedbug73916bCallRecover() {
	func() {
		if recover() != nil {
			fixedbug73916bRecovered = true
		}
	}()
}

func fixedbug73916bDeferred() int {
	fixedbug73916bCallRecover()
	return 0
}

func TestRecoverFixedbug73916NestedRecoverDoesNotRecover(t *testing.T) {
	fixedbug73916bRecovered = false
	fixedbug73916MustPanic(t, func() {
		defer fixedbug73916bDeferred()
		panic("fixedbug73916b")
	})
	if fixedbug73916bRecovered {
		t.Fatal("nested recover returned non-nil")
	}
}
