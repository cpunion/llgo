package gotest

import "testing"

type plainDescriptorPayload struct {
	Number uint32
	Text   string
	Code   [3]byte
}

func plainDescriptorFirst(value plainDescriptorPayload) plainDescriptorPayload {
	value.Number++
	return value
}

func plainDescriptorSecond(value plainDescriptorPayload) plainDescriptorPayload {
	value.Number += 2
	return value
}

func applyPlainDescriptor(callback func(plainDescriptorPayload) plainDescriptorPayload, value plainDescriptorPayload) plainDescriptorPayload {
	return callback(value)
}

var plainDescriptorDeferCount uint32

func plainDescriptorDeferFirst(value plainDescriptorPayload) {
	plainDescriptorDeferCount += value.Number
}

func plainDescriptorDeferSecond(value plainDescriptorPayload) {
	plainDescriptorDeferCount += 2 * value.Number
}

func applyPlainDescriptorDefer(callback func(plainDescriptorPayload), value plainDescriptorPayload) {
	defer callback(value)
}

func TestClosedPlainDescriptorFamily(t *testing.T) {
	input := plainDescriptorPayload{Number: 40, Text: "unchanged", Code: [3]byte{1, 2, 3}}
	for index, callback := range []func(plainDescriptorPayload) plainDescriptorPayload{plainDescriptorFirst, plainDescriptorSecond} {
		got := applyPlainDescriptor(callback, input)
		if got.Number != 41+uint32(index) || got.Text != input.Text || got.Code != input.Code || input.Number != 40 {
			t.Fatalf("plain aggregate callback %d: got=%#v input=%#v", index, got, input)
		}
	}
	// The nullable member of the same closed family must retain a recoverable
	// nil-call panic; selecting its plain ABI must not skip argument evaluation.
	evaluated, recovered := false, false
	func() {
		defer func() { recovered = recover() != nil }()
		var callback func(plainDescriptorPayload) plainDescriptorPayload
		argument := func() plainDescriptorPayload { evaluated = true; return input }
		applyPlainDescriptor(callback, argument())
	}()
	if !evaluated || !recovered {
		t.Fatalf("nil callback: argument evaluated=%t panic recovered=%t", evaluated, recovered)
	}
	plainDescriptorDeferCount = 0
	for _, callback := range []func(plainDescriptorPayload){plainDescriptorDeferFirst, plainDescriptorDeferSecond} {
		applyPlainDescriptorDefer(callback, input)
	}
	if plainDescriptorDeferCount != 120 {
		t.Fatalf("deferred plain callbacks = %d, want 120", plainDescriptorDeferCount)
	}
	recovered = false
	func() {
		defer func() { recovered = recover() != nil }()
		applyPlainDescriptorDefer(nil, input)
	}()
	if !recovered {
		t.Fatal("nil deferred plain callback did not produce a recoverable panic")
	}
}
