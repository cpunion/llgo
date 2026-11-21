package gotest

import (
	"runtime"
	"testing"
)

type tailForwardHeaderBytes []byte
type tailForwardHeaderText string
type tailForwardHeaderValue struct {
	Prefix int
	Value  struct {
		Data tailForwardHeaderBytes
		Text tailForwardHeaderText
	}
}

func tailForwardHeaderChild(
	data tailForwardHeaderBytes, text tailForwardHeaderText, bytes, chars int,
	entered chan struct{}, release <-chan struct{},
) [2]int {
	close(entered)
	<-release
	runtime.Gosched()
	if bytes != len(data) || chars != len(text) {
		panic("forwarded header length no longer matches its snapshotted value")
	}
	return [2]int{bytes, chars}
}

func tailForwardHeaderWrapper(value tailForwardHeaderValue, entered chan struct{}, release <-chan struct{}) [2]int {
	return tailForwardHeaderChild(
		value.Value.Data, value.Value.Text, len(value.Value.Data), len(value.Value.Text),
		entered, release,
	)
}

func tailForwardHeaderOuter(value tailForwardHeaderValue, entered chan struct{}, release <-chan struct{}) [2]int {
	return tailForwardHeaderWrapper(value, entered, release)
}

func TestTailForwardHeaderLengthSurvivesSuspension(t *testing.T) {
	for _, test := range []struct {
		name string
		data tailForwardHeaderBytes
		text tailForwardHeaderText
	}{
		{"nil", nil, ""},
		{"empty", make(tailForwardHeaderBytes, 0, 64), ""},
		{"subslice", tailForwardHeaderBytes{1, 2, 3, 4}[1:3], "abc"},
		{"utf8", tailForwardHeaderBytes{9}, "世界"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value tailForwardHeaderValue
			value.Value.Data, value.Value.Text = test.data, test.text
			want := [2]int{len(test.data), len(test.text)}
			entered, release := make(chan struct{}), make(chan struct{})
			result := make(chan [2]int, 1)
			go func(snapshot tailForwardHeaderValue) {
				result <- tailForwardHeaderOuter(snapshot, entered, release)
			}(value)
			<-entered
			// Mutate the original headers while the target is suspended.
			// The projected argument and its length must use the same copy.
			value.Value.Data, value.Value.Text = nil, "replacement"
			runtime.GC()
			close(release)
			if got := <-result; got != want {
				t.Fatalf("header snapshot result = %v, want %v", got, want)
			}
		})
	}
}

//go:noinline
func tailForwardHeaderFaultChild(data []byte, length int, pointer *int) int {
	runtime.Gosched()
	return len(data) + length + *pointer
}

func tailForwardHeaderFaultWrapper(data []byte, pointer *int) int {
	return tailForwardHeaderFaultChild(data, len(data), pointer)
}

func TestTailForwardHeaderLengthKeepsFaultRecovery(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("missing fault propagated through header-length wrapper")
		}
	}()
	tailForwardHeaderFaultWrapper([]byte{1, 2, 3}, nil)
	t.Fatal("nil dereference returned")
}
