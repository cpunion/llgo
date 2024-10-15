package py

/*
#include <Python.h>
*/
import "C"
import (
	_ "unsafe"
)

// int PyDict_Check(PyObject *p)
// Return true if *p* is a dict object or an instance of a subtype of the dict
// type.  This function always succeeds.
//
//go:linkname DictCheck C.PyDict_Check
func DictCheck(p *Object) Int

// int PyDict_CheckExact(PyObject *p)
// Return true if *p* is a dict object, but not an instance of a subtype of
// the dict type.  This function always succeeds.
//
//go:linkname DictCheckExact C.PyDict_CheckExact
func DictCheckExact(p *Object) Int

// PyObject* PyDict_New()
// Return a new empty dictionary, or “NULL“ on failure.
//
//go:linkname DictNew C.PyDict_New
func DictNew() *Object

// PyObject* PyDictProxy_New(PyObject *mapping)
// Return a :class:`types.MappingProxyType` object for a mapping which
// enforces read-only behavior.  This is normally used to create a view to
// prevent modification of the dictionary for non-dynamic class types.
//
//go:linkname DictProxyNew C.PyDictProxy_New
func DictProxyNew(mapping *Object) *Object

// void PyDict_Clear(PyObject *p)
// Empty an existing dictionary of all key-value pairs.
//
//go:linkname DictClear C.PyDict_Clear
func DictClear(p *Object)

// int PyDict_Contains(PyObject *p, PyObject *key)
// Determine if dictionary *p* contains *key*.  If an item in *p* is matches
// *key*, return “1“, otherwise return “0“.  On error, return “-1“.
// This is equivalent to the Python expression “key in p“.
//
//go:linkname DictContains C.PyDict_Contains
func DictContains(p *Object, key *Object) Int

// PyObject* PyDict_Copy(PyObject *p)
// Return a new dictionary that contains the same key-value pairs as *p*.
//
//go:linkname DictCopy C.PyDict_Copy
func DictCopy(p *Object) *Object

// int PyDict_SetItem(PyObject *p, PyObject *key, PyObject *val)
// Insert *val* into the dictionary *p* with a key of *key*.  *key* must be
// :term:`hashable`; if it isn't, :exc:`TypeError` will be raised. Return
// “0“ on success or “-1“ on failure.  This function *does not* steal a
// reference to *val*.
//
//go:linkname DictSetItem C.PyDict_SetItem
func DictSetItem(p *Object, key *Object, val *Object) Int

// int PyDict_SetItemString(PyObject *p, const char *key, PyObject *val)
// This is the same as :c:func:`PyDict_SetItem`, but *key* is
// specified as a :c:expr:`const char*` UTF-8 encoded bytes string,
// rather than a :c:expr:`PyObject*`.
//
//go:linkname DictSetItemString C.PyDict_SetItemString
func DictSetItemString(p *Object, key *Char, val *Object) Int

// int PyDict_DelItem(PyObject *p, PyObject *key)
// Remove the entry in dictionary *p* with key *key*. *key* must be :term:`hashable`;
// if it isn't, :exc:`TypeError` is raised.
// If *key* is not in the dictionary, :exc:`KeyError` is raised.
// Return “0“ on success or “-1“ on failure.
//
//go:linkname DictDelItem C.PyDict_DelItem
func DictDelItem(p *Object, key *Object) Int

// int PyDict_DelItemString(PyObject *p, const char *key)
// This is the same as :c:func:`PyDict_DelItem`, but *key* is
// specified as a :c:expr:`const char*` UTF-8 encoded bytes string,
// rather than a :c:expr:`PyObject*`.
//
//go:linkname DictDelItemString C.PyDict_DelItemString
func DictDelItemString(p *Object, key *Char) Int

// PyObject* PyDict_GetItem(PyObject *p, PyObject *key)
// Return a :term:`borrowed reference` to the object from dictionary *p* which
// has a key *key*.  Return “NULL“ if the key *key* is missing *without*
// setting an exception.
//
// .. note::
//
// Exceptions that occur while this calls :meth:`~object.__hash__` and
// :meth:`~object.__eq__` methods are silently ignored.
// Prefer the :c:func:`PyDict_GetItemWithError` function instead.
//
// Calling this API without :term:`GIL` held had been allowed for historical
// reason. It is no longer allowed.
//
//go:linkname DictGetItem C.PyDict_GetItem
func DictGetItem(p *Object, key *Object) *Object

// PyObject* PyDict_GetItemWithError(PyObject *p, PyObject *key)
// Variant of :c:func:`PyDict_GetItem` that does not suppress
// exceptions. Return “NULL“ **with** an exception set if an exception
// occurred.  Return “NULL“ **without** an exception set if the key
// wasn't present.
//
//go:linkname DictGetItemWithError C.PyDict_GetItemWithError
func DictGetItemWithError(p *Object, key *Object) *Object

// PyObject* PyDict_GetItemString(PyObject *p, const char *key)
// This is the same as :c:func:`PyDict_GetItem`, but *key* is specified as a
// :c:expr:`const char*` UTF-8 encoded bytes string, rather than a
// :c:expr:`PyObject*`.
//
// .. note::
//
// Exceptions that occur while this calls :meth:`~object.__hash__` and
// :meth:`~object.__eq__` methods or while creating the temporary :class:`str`
// object are silently ignored.
// Prefer using the :c:func:`PyDict_GetItemWithError` function with your own
// :c:func:`PyUnicode_FromString` *key* instead.
//
//go:linkname DictGetItemString C.PyDict_GetItemString
func DictGetItemString(p *Object, key *Char) *Object

// PyObject* PyDict_SetDefault(PyObject *p, PyObject *key, PyObject *defaultobj)
// This is the same as the Python-level :meth:`dict.setdefault`.  If present, it
// returns the value corresponding to *key* from the dictionary *p*.  If the key
// is not in the dict, it is inserted with value *defaultobj* and *defaultobj*
// is returned.  This function evaluates the hash function of *key* only once,
// instead of evaluating it independently for the lookup and the insertion.
//
//go:linkname DictSetDefault C.PyDict_SetDefault
func DictSetDefault(p *Object, key *Object, defaultobj *Object) *Object

// PyObject* PyDict_Items(PyObject *p)
// Return a :c:type:`PyListObject` containing all the items from the dictionary.
//
//go:linkname DictItems C.PyDict_Items
func DictItems(p *Object) *Object

// PyObject* PyDict_Keys(PyObject *p)
// Return a :c:type:`PyListObject` containing all the keys from the dictionary.
//
//go:linkname DictKeys C.PyDict_Keys
func DictKeys(p *Object) *Object

// PyObject* PyDict_Values(PyObject *p)
// Return a :c:type:`PyListObject` containing all the values from the dictionary
// *p*.
//
//go:linkname DictValues C.PyDict_Values
func DictValues(p *Object) *Object

// Py_ssize_t PyDict_Size(PyObject *p)
// .. index:: pair: built-in function; len
//
// Return the number of items in the dictionary.  This is equivalent to
// “len(p)“ on a dictionary.
//
//go:linkname DictSize C.PyDict_Size
func DictSize(p *Object) SSizeT

// int PyDict_Next(PyObject *p, Py_ssize_t *ppos, PyObject **pkey, PyObject **pvalue)
// Iterate over all key-value pairs in the dictionary *p*.  The
// :c:type:`Py_ssize_t` referred to by *ppos* must be initialized to “0“
// prior to the first call to this function to start the iteration; the
// function returns true for each pair in the dictionary, and false once all
// pairs have been reported.  The parameters *pkey* and *pvalue* should either
// point to :c:expr:`PyObject*` variables that will be filled in with each key
// and value, respectively, or may be “NULL“.  Any references returned through
// them are borrowed.  *ppos* should not be altered during iteration. Its
// value represents offsets within the internal dictionary structure, and
// since the structure is sparse, the offsets are not consecutive.
//
// For example::
//
// PyObject *key, *value;
// Py_ssize_t pos = 0;
//
// while (PyDict_Next(self->dict, &pos, &key, &value)) {
// /* do something interesting with the values... */
// ...
// }
//
// The dictionary *p* should not be mutated during iteration.  It is safe to
// modify the values of the keys as you iterate over the dictionary, but only
// so long as the set of keys does not change.  For example::
//
// PyObject *key, *value;
// Py_ssize_t pos = 0;
//
// while (PyDict_Next(self->dict, &pos, &key, &value)) {
// long i = PyLong_AsLong(value);
// if (i == -1 && PyErr_Occurred()) {
// return -1;
// }
// PyObject *o = PyLong_FromLong(i + 1);
// if (o == NULL)
// return -1;
// if (PyDict_SetItem(self->dict, key, o) < 0) {
// Py_DECREF(o);
// return -1;
// }
// Py_DECREF(o);
// }
//
// The function is not thread-safe in the :term:`free-threaded <free threading>`
// build without external synchronization.  You can use
// :c:macro:`Py_BEGIN_CRITICAL_SECTION` to lock the dictionary while iterating
// over it::
//
// Py_BEGIN_CRITICAL_SECTION(self->dict);
// while (PyDict_Next(self->dict, &pos, &key, &value)) {
// ...
// }
// Py_END_CRITICAL_SECTION();
//
//go:linkname DictNext C.PyDict_Next
func DictNext(p *Object, ppos *SSizeT, pkey **Object, pvalue **Object) Int

// int PyDict_Merge(PyObject *a, PyObject *b, int override)
// Iterate over mapping object *b* adding key-value pairs to dictionary *a*.
// *b* may be a dictionary, or any object supporting :c:func:`PyMapping_Keys`
// and :c:func:`PyObject_GetItem`. If *override* is true, existing pairs in *a*
// will be replaced if a matching key is found in *b*, otherwise pairs will
// only be added if there is not a matching key in *a*. Return “0“ on
// success or “-1“ if an exception was raised.
//
//go:linkname DictMerge C.PyDict_Merge
func DictMerge(a *Object, b *Object, override Int) Int

// int PyDict_Update(PyObject *a, PyObject *b)
// This is the same as “PyDict_Merge(a, b, 1)“ in C, and is similar to
// “a.update(b)“ in Python except that :c:func:`PyDict_Update` doesn't fall
// back to the iterating over a sequence of key value pairs if the second
// argument has no "keys" attribute.  Return “0“ on success or “-1“ if an
// exception was raised.
//
//go:linkname DictUpdate C.PyDict_Update
func DictUpdate(a *Object, b *Object) Int

// int PyDict_MergeFromSeq2(PyObject *a, PyObject *seq2, int override)
// Update or merge into dictionary *a*, from the key-value pairs in *seq2*.
// *seq2* must be an iterable object producing iterable objects of length 2,
// viewed as key-value pairs.  In case of duplicate keys, the last wins if
// *override* is true, else the first wins. Return “0“ on success or “-1“
// if an exception was raised. Equivalent Python (except for the return
// value)::
//
// def PyDict_MergeFromSeq2(a, seq2, override):
// for key, value in seq2:
// if override or key not in a:
// a[key] = value
//
//go:linkname DictMergeFromSeq2 C.PyDict_MergeFromSeq2
func DictMergeFromSeq2(a *Object, seq2 *Object, override Int) Int

// int PyDict_AddWatcher(PyDict_WatchCallback callback)
// Register *callback* as a dictionary watcher. Return a non-negative integer
// id which must be passed to future calls to :c:func:`PyDict_Watch`. In case
// of error (e.g. no more watcher IDs available), return “-1“ and set an
// exception.
//
//go:linkname DictAddWatcher C.PyDict_AddWatcher
func DictAddWatcher(callback DictWatchCallback) Int

// int PyDict_ClearWatcher(int watcher_id)
// Clear watcher identified by *watcher_id* previously returned from
// :c:func:`PyDict_AddWatcher`. Return “0“ on success, “-1“ on error (e.g.
// if the given *watcher_id* was never registered.)
//
//go:linkname DictClearWatcher C.PyDict_ClearWatcher
func DictClearWatcher(watcherId Int) Int

// int PyDict_Watch(int watcher_id, PyObject *dict)
// Mark dictionary *dict* as watched. The callback granted *watcher_id* by
// :c:func:`PyDict_AddWatcher` will be called when *dict* is modified or
// deallocated. Return “0“ on success or “-1“ on error.
//
//go:linkname DictWatch C.PyDict_Watch
func DictWatch(watcherId Int, dict *Object) Int

// int PyDict_Unwatch(int watcher_id, PyObject *dict)
// Mark dictionary *dict* as no longer watched. The callback granted
// *watcher_id* by :c:func:`PyDict_AddWatcher` will no longer be called when
// *dict* is modified or deallocated. The dict must previously have been
// watched by this watcher. Return “0“ on success or “-1“ on error.
//
//go:linkname DictUnwatch C.PyDict_Unwatch
func DictUnwatch(watcherId Int, dict *Object) Int

// PyDictObject
// This subtype of :c:type:`PyObject` represents a Python dictionary object.
type DictObject = C.PyDictObject

// PyDict_WatchEvent
// Enumeration of possible dictionary watcher events: “PyDict_EVENT_ADDED“,
// “PyDict_EVENT_MODIFIED“, “PyDict_EVENT_DELETED“, “PyDict_EVENT_CLONED“,
// “PyDict_EVENT_CLEARED“, or “PyDict_EVENT_DEALLOCATED“.
type DictWatchEvent = C.PyDict_WatchEvent

// int (*PyDict_WatchCallback)(PyDict_WatchEvent event, PyObject *dict, PyObject *key, PyObject *new_value)
// Type of a dict watcher callback function.
//
// If *event* is “PyDict_EVENT_CLEARED“ or “PyDict_EVENT_DEALLOCATED“, both
// *key* and *new_value* will be “NULL“. If *event* is “PyDict_EVENT_ADDED“
// or “PyDict_EVENT_MODIFIED“, *new_value* will be the new value for *key*.
// If *event* is “PyDict_EVENT_DELETED“, *key* is being deleted from the
// dictionary and *new_value* will be “NULL“.
//
// “PyDict_EVENT_CLONED“ occurs when *dict* was previously empty and another
// dict is merged into it. To maintain efficiency of this operation, per-key
// “PyDict_EVENT_ADDED“ events are not issued in this case; instead a
// single “PyDict_EVENT_CLONED“ is issued, and *key* will be the source
// dictionary.
//
// The callback may inspect but must not modify *dict*; doing so could have
// unpredictable effects, including infinite recursion. Do not trigger Python
// code execution in the callback, as it could modify the dict as a side effect.
//
// If *event* is “PyDict_EVENT_DEALLOCATED“, taking a new reference in the
// callback to the about-to-be-destroyed dictionary will resurrect it and
// prevent it from being freed at this time. When the resurrected object is
// destroyed later, any watcher callbacks active at that time will be called
// again.
//
// Callbacks occur before the notified modification to *dict* takes place, so
// the prior state of *dict* can be inspected.
//
// If the callback sets an exception, it must return “-1“; this exception will
// be printed as an unraisable exception using :c:func:`PyErr_WriteUnraisable`.
// Otherwise it should return “0“.
//
// There may already be a pending exception set on entry to the callback. In
// this case, the callback should return “0“ with the same exception still
// set. This means the callback may not call any other API that can set an
// exception unless it saves and clears the exception state first, and restores
// it before returning.
// llgo:type C
type DictWatchCallback func(event DictWatchEvent, dict *Object, key *Object, newValue *Object) Int

// PyTypeObject PyDict_Type
// This instance of :c:type:`PyTypeObject` represents the Python dictionary
// type.  This is the same object as :class:`dict` in the Python layer.
func DictType() TypeObject {
	return *(*TypeObject)(Pointer(&C.PyDict_Type))
}