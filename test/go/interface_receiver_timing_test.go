package gotest

import (
	"runtime"
	"testing"
)

type timingReceiver struct{ value int }

func (r timingReceiver) record(out *[]int) { *out = append(*out, r.value) }

type timingRecorder interface{ record(*[]int) }

func TestInterfaceValueReceiverEvaluationTime(t *testing.T) {
	var got []int
	func() {
		r := timingReceiver{1}
		var recorder timingRecorder = &r
		defer recorder.record(&got) // Dereference the interface's *T at invocation.
		r.value = 2
		method := recorder.record
		r.value = 3
		method(&got)
		defer r.record(&got) // Capture the concrete value receiver now.
		r.value = 4
	}()
	if len(got) != 3 || got[0] != 3 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("receiver evaluation = %v, want [3 3 4]", got)
	}
}

func TestInterfaceMethodValueEvaluationAfterAwait(t *testing.T) {
	r := timingReceiver{1}
	var recorder timingRecorder = &r
	method := recorder.record
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var got []int
	go func() {
		close(ready)
		<-release
		method(&got)
		close(done)
	}()
	<-ready
	r.value = 9
	runtime.GC()
	close(release)
	<-done
	if len(got) != 1 || got[0] != 9 {
		t.Fatalf("method value after await = %v, want [9]", got)
	}
}

func TestInterfaceNilValueReceiverPanicsAtInvocation(t *testing.T) {
	created := false
	var out []int
	func() {
		defer func() {
			if recover() == nil {
				t.Error("nil *T value-method call did not panic")
			}
		}()
		var receiver *timingReceiver
		var recorder timingRecorder = receiver
		method := recorder.record
		created = true
		method(&out)
	}()
	if !created || len(out) != 0 {
		t.Fatal("nil receiver was evaluated before the method-value invocation")
	}
}
