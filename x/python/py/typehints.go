package py

/*
#include <Python.h>
*/
import "C"
import (
	_ "unsafe"
)

// PyObject* Py_GenericAlias(PyObject *origin, PyObject *args)
// Create a :ref:`GenericAlias <types-genericalias>` object.
// Equivalent to calling the Python class
// :class:`types.GenericAlias`.  The *origin* and *args* arguments set the
// “GenericAlias“\ 's “__origin__“ and “__args__“ attributes respectively.
// *origin* should be a :c:expr:`PyTypeObject*`, and *args* can be a
// :c:expr:`PyTupleObject*` or any “PyObject*“.  If *args* passed is
// not a tuple, a 1-tuple is automatically constructed and “__args__“ is set
// to “(args,)“.
// Minimal checking is done for the arguments, so the function will succeed even
// if *origin* is not a type.
// The “GenericAlias“\ 's “__parameters__“ attribute is constructed lazily
// from “__args__“.  On failure, an exception is raised and “NULL“ is
// returned.
//
// Here's an example of how to make an extension type generic::
//
// ...
// static PyMethodDef my_obj_methods[] = {
// // Other methods.
// ...
// {"__class_getitem__", Py_GenericAlias, METH_O|METH_CLASS, "See PEP 585"}
// ...
// }
//
// .. seealso:: The data model method :meth:`~object.__class_getitem__`.
//
//go:linkname GenericAlias C.Py_GenericAlias
func GenericAlias(origin *Object, args *Object) *Object

// PyTypeObject Py_GenericAliasType
// The C type of the object returned by :c:func:`Py_GenericAlias`. Equivalent to
// :class:`types.GenericAlias` in Python.
func GenericAliasType() TypeObject {
	return *(*TypeObject)(Pointer(&C.Py_GenericAliasType))
}