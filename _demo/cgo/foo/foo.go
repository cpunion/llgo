package foo

/*
#cgo pkg-config: python-3.12-embed
#include <Python.h>
*/
import "C"

func Finalize() {
	C.Py_Finalize()
}
