package main

type receiver struct{ x int }
type embedded struct{ receiver }

// A buffered side-effect channel works for direct/defer/go calls without a
// test-side data race if a broken compiler incorrectly reaches this method.
func (*receiver) touch(effects chan<- struct{}) { effects <- struct{}{} }

func mustPanic(name string, f func()) {
	panicked := true
	func() {
		defer func() { recover() }()
		f()
		panicked = false
	}()
	if !panicked {
		panic(name + ": missing receiver panic")
	}
}

func main() {
	var pointer *receiver
	effects := make(chan struct{}, 8)
	mustPanic("direct", func() { (*pointer).touch(effects) })
	mustPanic("parenthesized", func() { (*(pointer)).touch(effects) })
	mustPanic("defer", func() { defer (*pointer).touch(effects) })
	mustPanic("go", func() { go (*pointer).touch(effects) })
	mustPanic("method-value", func() { f := (*pointer).touch; _ = f })
	var outer *embedded
	mustPanic("embedded", func() { outer.touch(effects) })
	select {
	case <-effects:
		panic("nil-safe method ran before caller-side panic")
	default:
	}
	// Nil pointer receivers without explicit dereference remain valid.
	pointer.touch(effects)
	f := pointer.touch
	f(effects)
	valid := new(receiver)
	(*valid).touch(effects)
	for i := 0; i < 3; i++ {
		select {
		case <-effects:
		default:
			panic("valid receiver call was lost")
		}
	}
	println("explicit receiver faults precede nil-safe method side effects")
}
