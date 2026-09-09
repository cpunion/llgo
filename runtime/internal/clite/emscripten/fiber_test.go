package emscripten

import (
	"reflect"
	"testing"
	"unsafe"
)

func TestFiberStorageUsesEightWords(t *testing.T) {
	if got, want := unsafe.Sizeof(Fiber{}), uintptr(8)*unsafe.Sizeof(uintptr(0)); got != want {
		t.Fatalf("Fiber size = %d, want %d", got, want)
	}
}

func TestFiberStackPointerUsesThirdWord(t *testing.T) {
	var fiber Fiber
	fiber.words[2] = 0x1234
	if got := fiber.StackPointer(); got != 0x1234 {
		t.Fatalf("Fiber.StackPointer() = %#x, want 0x1234", got)
	}
}

func TestFiberHasNoReflectableHostMethods(t *testing.T) {
	if got := reflect.TypeOf(Fiber{}).NumMethod(); got != 0 {
		t.Fatalf("Fiber has %d reflectable methods, want 0", got)
	}
}
