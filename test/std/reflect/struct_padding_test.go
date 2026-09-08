package reflect_test

import (
	"reflect"
	"testing"
	"unsafe"
)

func TestNestedStructTailPadding(t *testing.T) {
	// encoding/gob uses this shape for slice type descriptors. The element
	// type ID must follow the complete embedded struct, including its padding.
	type Common struct {
		Name string
		ID   int32
	}
	type descriptor struct {
		Common
		Elem int32
	}
	values := [2]descriptor{
		{Common: Common{Name: "first", ID: 17}, Elem: 2},
		{Common: Common{Name: "second", ID: 18}, Elem: 3},
	}
	commonSize := unsafe.Sizeof(Common{})
	if got := unsafe.Offsetof(values[0].Elem); got != commonSize {
		t.Errorf("Elem offset = %d, want complete Common size %d", got, commonSize)
	}
	typ := reflect.TypeOf(values[0])
	if got := typ.Field(1).Offset; got != commonSize {
		t.Errorf("reflected Elem offset = %d, want %d", got, commonSize)
	}
	if got, want := typ.Size(), unsafe.Sizeof(values[0]); got != want {
		t.Errorf("reflected size = %d, want %d", got, want)
	}
	for i := range values {
		v := reflect.ValueOf(&values[i]).Elem()
		if got, want := v.Field(1).Int(), int64(i+2); got != want {
			t.Errorf("values[%d].Elem = %d via reflection, want %d", i, got, want)
		}
		v.Field(1).SetInt(int64(i + 42))
		if got, want := values[i].Elem, int32(i+42); got != want {
			t.Errorf("values[%d].Elem after reflected write = %d, want %d", i, got, want)
		}
		if got, want := values[i].ID, int32(i+17); got != want {
			t.Errorf("values[%d].ID = %d, want %d", i, got, want)
		}
	}
}
