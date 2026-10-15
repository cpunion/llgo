package gotest

import "testing"

type explicitNilReceiver struct{ value int }
type explicitNilEmbedded struct{ explicitNilReceiver }

// The method is deliberately nil-safe: the fault must come from evaluating
// the explicit receiver, not from executing the method body.
func (*explicitNilReceiver) touch(effects chan<- struct{}) { effects <- struct{}{} }

func TestExplicitPointerReceiverFaultsBeforeMethod(t *testing.T) {
	var pointer *explicitNilReceiver
	var outer *explicitNilEmbedded
	effects := make(chan struct{}, 8)
	for _, test := range []struct {
		name string
		call func()
	}{
		{"direct", func() { (*pointer).touch(effects) }},
		{"parenthesized", func() { (*(pointer)).touch(effects) }},
		{"defer", func() { defer (*pointer).touch(effects) }},
		{"go", func() { go (*pointer).touch(effects) }},
		{"method-value", func() { f := (*pointer).touch; _ = f }},
		{"embedded", func() { outer.touch(effects) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			panicked := true
			func() {
				defer func() { recover() }()
				test.call()
				panicked = false
			}()
			if !panicked {
				t.Fatal("receiver evaluation did not panic")
			}
			select {
			case <-effects:
				t.Fatal("nil-safe method ran before receiver panic")
			default:
			}
		})
	}
	// No explicit dereference: a nil pointer receiver is legal.
	pointer.touch(effects)
	f := pointer.touch
	f(effects)
	valid := new(explicitNilReceiver)
	(*valid).touch(effects)
	for i := 0; i < 3; i++ {
		select {
		case <-effects:
		default:
			t.Fatal("valid receiver call was lost")
		}
	}
}
