package gotest

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/xgo-dev/llgo/test/go/anonymousabipkg"
)

type anonymousABIReceiver struct {
	entered chan<- struct{}
	gate    <-chan struct{}
}

func (r anonymousABIReceiver) ApplyAnon(input struct {
	R0 uint32
	R1 string
}) struct {
	R0 uint32
	R1 string
} {
	r.entered <- struct{}{}
	<-r.gate
	input.R0++
	input.R1 += "!"
	return input
}

func TestCrossPackageAnonymousMemberABI(t *testing.T) {
	for _, mode := range []struct {
		name string
		call func(anonymousabipkg.Transformer, anonymousabipkg.Payload) anonymousabipkg.Payload
	}{
		{"interface", anonymousabipkg.Call},
		{"method value", func(value anonymousabipkg.Transformer, input anonymousabipkg.Payload) anonymousabipkg.Payload {
			var callback func(anonymousabipkg.Payload) anonymousabipkg.Payload = value.ApplyAnon
			return callback(input)
		}},
		{"reflect", func(value anonymousabipkg.Transformer, input anonymousabipkg.Payload) anonymousabipkg.Payload {
			out := reflect.ValueOf(value).MethodByName("ApplyAnon").Call([]reflect.Value{reflect.ValueOf(input)})
			return out[0].Interface().(anonymousabipkg.Payload)
		}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			entered, gate := make(chan struct{}), make(chan struct{})
			done := make(chan anonymousabipkg.Payload, 1)
			value := anonymousABIReceiver{entered: entered, gate: gate}
			input := anonymousabipkg.Payload{R0: 41, R1: "coro"}
			go func() { done <- mode.call(value, input) }()
			<-entered
			runtime.Gosched()
			close(gate)
			if got := <-done; got.R0 != 42 || got.R1 != "coro!" {
				t.Fatalf("cross-package aggregate result = %#v", got)
			}
			if input.R0 != 41 || input.R1 != "coro" {
				t.Fatal("callee mutated the by-value aggregate input")
			}
		})
	}
}
