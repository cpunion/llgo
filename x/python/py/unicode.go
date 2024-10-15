package py

/*
#include <Python.h>
*/
import "C"
import (
	_ "unsafe"
)

// int PyUnicode_Check(PyObject *obj)
// Return true if the object *obj* is a Unicode object or an instance of a Unicode
// subtype.  This function always succeeds.
//
//go:linkname UnicodeCheck C.PyUnicode_Check
func UnicodeCheck(obj *Object) Int

// int PyUnicode_CheckExact(PyObject *obj)
// Return true if the object *obj* is a Unicode object, but not an instance of a
// subtype.  This function always succeeds.
//
//go:linkname UnicodeCheckExact C.PyUnicode_CheckExact
func UnicodeCheckExact(obj *Object) Int

// int PyUnicode_READY(PyObject *unicode)
// Returns “0“. This API is kept only for backward compatibility.
//
// .. deprecated:: 3.10
// This API does nothing since Python 3.12.
//
//go:linkname UnicodeREADY C.PyUnicode_READY
func UnicodeREADY(unicode *Object) Int

// Py_ssize_t PyUnicode_GET_LENGTH(PyObject *unicode)
// Return the length of the Unicode string, in code points.  *unicode* has to be a
// Unicode object in the "canonical" representation (not checked).
//
//go:linkname UnicodeGETLENGTH C.PyUnicode_GET_LENGTH
func UnicodeGETLENGTH(unicode *Object) SSizeT

// Py_UCS1* PyUnicode_1BYTE_DATA(PyObject *unicode)
// Py_UCS2* PyUnicode_2BYTE_DATA(PyObject *unicode)
// Py_UCS4* PyUnicode_4BYTE_DATA(PyObject *unicode)
//
// Return a pointer to the canonical representation cast to UCS1, UCS2 or UCS4
// integer types for direct character access.  No checks are performed if the
// canonical representation has the correct character size; use
// :c:func:`PyUnicode_KIND` to select the right function.
//
//go:linkname Unicode1BYTEDATA C.PyUnicode_1BYTE_DATA
func Unicode1BYTEDATA(unicode *Object) *UCS1

// int PyUnicode_KIND(PyObject *unicode)
// Return one of the PyUnicode kind constants (see above) that indicate how many
// bytes per character this Unicode object uses to store its data.  *unicode* has to
// be a Unicode object in the "canonical" representation (not checked).
//
//go:linkname UnicodeKIND C.PyUnicode_KIND
func UnicodeKIND(unicode *Object) Int

// void* PyUnicode_DATA(PyObject *unicode)
// Return a void pointer to the raw Unicode buffer.  *unicode* has to be a Unicode
// object in the "canonical" representation (not checked).
//
//go:linkname UnicodeDATA C.PyUnicode_DATA
func UnicodeDATA(unicode *Object) Pointer

// void PyUnicode_WRITE(int kind, void *data, \
// Py_ssize_t index, Py_UCS4 value)
//
// Write into a canonical representation *data* (as obtained with
// :c:func:`PyUnicode_DATA`).  This function performs no sanity checks, and is
// intended for usage in loops.  The caller should cache the *kind* value and
// *data* pointer as obtained from other calls.  *index* is the index in
// the string (starts at 0) and *value* is the new code point value which should
// be written to that location.
//
//go:linkname UnicodeWRITE C.PyUnicode_WRITE
func UnicodeWRITE(kind Int, data Pointer)

// Py_UCS4 PyUnicode_READ(int kind, void *data, \
// Py_ssize_t index)
//
// Read a code point from a canonical representation *data* (as obtained with
// :c:func:`PyUnicode_DATA`).  No checks or ready calls are performed.
//
//go:linkname UnicodeREAD C.PyUnicode_READ
func UnicodeREAD(kind Int, data Pointer) UCS4

// Py_UCS4 PyUnicode_READ_CHAR(PyObject *unicode, Py_ssize_t index)
// Read a character from a Unicode object *unicode*, which must be in the "canonical"
// representation.  This is less efficient than :c:func:`PyUnicode_READ` if you
// do multiple consecutive reads.
//
//go:linkname UnicodeREADCHAR C.PyUnicode_READ_CHAR
func UnicodeREADCHAR(unicode *Object, index SSizeT) UCS4

// Py_UCS4 PyUnicode_MAX_CHAR_VALUE(PyObject *unicode)
// Return the maximum code point that is suitable for creating another string
// based on *unicode*, which must be in the "canonical" representation.  This is
// always an approximation but more efficient than iterating over the string.
//
//go:linkname UnicodeMAXCHARVALUE C.PyUnicode_MAX_CHAR_VALUE
func UnicodeMAXCHARVALUE(unicode *Object) UCS4

// int PyUnicode_IsIdentifier(PyObject *unicode)
// Return “1“ if the string is a valid identifier according to the language
// definition, section :ref:`identifiers`. Return “0“ otherwise.
//
// The function does not call :c:func:`Py_FatalError` anymore if the string
// is not ready.
//
// Unicode Character Properties
// """"""""""""""""""""""""""""
//
// Unicode provides many different character properties. The most often needed ones
// are available through these macros which are mapped to C functions depending on
// the Python configuration.
//
//go:linkname UnicodeIsIdentifier C.PyUnicode_IsIdentifier
func UnicodeIsIdentifier(unicode *Object) Int

// int Py_UNICODE_ISSPACE(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is a whitespace character.
//
//go:linkname UNICODEISSPACE C.Py_UNICODE_ISSPACE
func UNICODEISSPACE(ch UCS4) Int

// int Py_UNICODE_ISLOWER(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is a lowercase character.
//
//go:linkname UNICODEISLOWER C.Py_UNICODE_ISLOWER
func UNICODEISLOWER(ch UCS4) Int

// int Py_UNICODE_ISUPPER(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is an uppercase character.
//
//go:linkname UNICODEISUPPER C.Py_UNICODE_ISUPPER
func UNICODEISUPPER(ch UCS4) Int

// int Py_UNICODE_ISTITLE(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is a titlecase character.
//
//go:linkname UNICODEISTITLE C.Py_UNICODE_ISTITLE
func UNICODEISTITLE(ch UCS4) Int

// int Py_UNICODE_ISLINEBREAK(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is a linebreak character.
//
//go:linkname UNICODEISLINEBREAK C.Py_UNICODE_ISLINEBREAK
func UNICODEISLINEBREAK(ch UCS4) Int

// int Py_UNICODE_ISDECIMAL(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is a decimal character.
//
//go:linkname UNICODEISDECIMAL C.Py_UNICODE_ISDECIMAL
func UNICODEISDECIMAL(ch UCS4) Int

// int Py_UNICODE_ISDIGIT(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is a digit character.
//
//go:linkname UNICODEISDIGIT C.Py_UNICODE_ISDIGIT
func UNICODEISDIGIT(ch UCS4) Int

// int Py_UNICODE_ISNUMERIC(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is a numeric character.
//
//go:linkname UNICODEISNUMERIC C.Py_UNICODE_ISNUMERIC
func UNICODEISNUMERIC(ch UCS4) Int

// int Py_UNICODE_ISALPHA(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is an alphabetic character.
//
//go:linkname UNICODEISALPHA C.Py_UNICODE_ISALPHA
func UNICODEISALPHA(ch UCS4) Int

// int Py_UNICODE_ISALNUM(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is an alphanumeric character.
//
//go:linkname UNICODEISALNUM C.Py_UNICODE_ISALNUM
func UNICODEISALNUM(ch UCS4) Int

// int Py_UNICODE_ISPRINTABLE(Py_UCS4 ch)
// Return “1“ or “0“ depending on whether *ch* is a printable character.
// Nonprintable characters are those characters defined in the Unicode character
// database as "Other" or "Separator", excepting the ASCII space (0x20) which is
// considered printable.  (Note that printable characters in this context are
// those which should not be escaped when :func:`repr` is invoked on a string.
// It has no bearing on the handling of strings written to :data:`sys.stdout` or
// :data:`sys.stderr`.)
//
// These APIs can be used for fast direct character conversions:
//
//go:linkname UNICODEISPRINTABLE C.Py_UNICODE_ISPRINTABLE
func UNICODEISPRINTABLE(ch UCS4) Int

// Py_UCS4 Py_UNICODE_TOLOWER(Py_UCS4 ch)
// Return the character *ch* converted to lower case.
//
//go:linkname UNICODETOLOWER C.Py_UNICODE_TOLOWER
func UNICODETOLOWER(ch UCS4) UCS4

// Py_UCS4 Py_UNICODE_TOUPPER(Py_UCS4 ch)
// Return the character *ch* converted to upper case.
//
//go:linkname UNICODETOUPPER C.Py_UNICODE_TOUPPER
func UNICODETOUPPER(ch UCS4) UCS4

// Py_UCS4 Py_UNICODE_TOTITLE(Py_UCS4 ch)
// Return the character *ch* converted to title case.
//
//go:linkname UNICODETOTITLE C.Py_UNICODE_TOTITLE
func UNICODETOTITLE(ch UCS4) UCS4

// int Py_UNICODE_TODECIMAL(Py_UCS4 ch)
// Return the character *ch* converted to a decimal positive integer.  Return
// “-1“ if this is not possible.  This function does not raise exceptions.
//
//go:linkname UNICODETODECIMAL C.Py_UNICODE_TODECIMAL
func UNICODETODECIMAL(ch UCS4) Int

// int Py_UNICODE_TODIGIT(Py_UCS4 ch)
// Return the character *ch* converted to a single digit integer. Return “-1“ if
// this is not possible.  This function does not raise exceptions.
//
//go:linkname UNICODETODIGIT C.Py_UNICODE_TODIGIT
func UNICODETODIGIT(ch UCS4) Int

// double Py_UNICODE_TONUMERIC(Py_UCS4 ch)
// Return the character *ch* converted to a double. Return “-1.0“ if this is not
// possible.  This function does not raise exceptions.
//
// These APIs can be used to work with surrogates:
//
//go:linkname UNICODETONUMERIC C.Py_UNICODE_TONUMERIC
func UNICODETONUMERIC(ch UCS4) Double

// int Py_UNICODE_IS_SURROGATE(Py_UCS4 ch)
// Check if *ch* is a surrogate (“0xD800 <= ch <= 0xDFFF“).
//
//go:linkname UNICODEISSURROGATE C.Py_UNICODE_IS_SURROGATE
func UNICODEISSURROGATE(ch UCS4) Int

// int Py_UNICODE_IS_HIGH_SURROGATE(Py_UCS4 ch)
// Check if *ch* is a high surrogate (“0xD800 <= ch <= 0xDBFF“).
//
//go:linkname UNICODEISHIGHSURROGATE C.Py_UNICODE_IS_HIGH_SURROGATE
func UNICODEISHIGHSURROGATE(ch UCS4) Int

// int Py_UNICODE_IS_LOW_SURROGATE(Py_UCS4 ch)
// Check if *ch* is a low surrogate (“0xDC00 <= ch <= 0xDFFF“).
//
//go:linkname UNICODEISLOWSURROGATE C.Py_UNICODE_IS_LOW_SURROGATE
func UNICODEISLOWSURROGATE(ch UCS4) Int

// Py_UCS4 Py_UNICODE_JOIN_SURROGATES(Py_UCS4 high, Py_UCS4 low)
// Join two surrogate code points and return a single :c:type:`Py_UCS4` value.
// *high* and *low* are respectively the leading and trailing surrogates in a
// surrogate pair. *high* must be in the range [0xD800; 0xDBFF] and *low* must
// be in the range [0xDC00; 0xDFFF].
//
// Creating and accessing Unicode strings
// """"""""""""""""""""""""""""""""""""""
//
// To create Unicode objects and access their basic sequence properties, use these
// APIs:
//
//go:linkname UNICODEJOINSURROGATES C.Py_UNICODE_JOIN_SURROGATES
func UNICODEJOINSURROGATES(high UCS4, low UCS4) UCS4

// PyObject* PyUnicode_New(Py_ssize_t size, Py_UCS4 maxchar)
// Create a new Unicode object.  *maxchar* should be the true maximum code point
// to be placed in the string.  As an approximation, it can be rounded up to the
// nearest value in the sequence 127, 255, 65535, 1114111.
//
// This is the recommended way to allocate a new Unicode object.  Objects
// created using this function are not resizable.
//
// On error, set an exception and return “NULL“.
//
//go:linkname UnicodeNew C.PyUnicode_New
func UnicodeNew(size SSizeT, maxchar UCS4) *Object

// PyObject* PyUnicode_FromKindAndData(int kind, const void *buffer, \
// Py_ssize_t size)
//
// Create a new Unicode object with the given *kind* (possible values are
// :c:macro:`PyUnicode_1BYTE_KIND` etc., as returned by
// :c:func:`PyUnicode_KIND`).  The *buffer* must point to an array of *size*
// units of 1, 2 or 4 bytes per character, as given by the kind.
//
// If necessary, the input *buffer* is copied and transformed into the
// canonical representation.  For example, if the *buffer* is a UCS4 string
// (:c:macro:`PyUnicode_4BYTE_KIND`) and it consists only of codepoints in
// the UCS1 range, it will be transformed into UCS1
// (:c:macro:`PyUnicode_1BYTE_KIND`).
//
//go:linkname UnicodeFromKindAndData C.PyUnicode_FromKindAndData
func UnicodeFromKindAndData(kind Int, buffer Pointer) *Object

// PyObject* PyUnicode_FromStringAndSize(const char *str, Py_ssize_t size)
// Create a Unicode object from the char buffer *str*.  The bytes will be
// interpreted as being UTF-8 encoded.  The buffer is copied into the new
// object.
// The return value might be a shared object, i.e. modification of the data is
// not allowed.
//
// This function raises :exc:`SystemError` when:
//
// * *size* < 0,
// * *str* is “NULL“ and *size* > 0
//
// *str* == “NULL“ with *size* > 0 is not allowed anymore.
//
//go:linkname UnicodeFromStringAndSize C.PyUnicode_FromStringAndSize
func UnicodeFromStringAndSize(str *Char, size SSizeT) *Object

// PyObject *PyUnicode_FromString(const char *str)
// Create a Unicode object from a UTF-8 encoded null-terminated char buffer
// *str*.
//
//go:linkname UnicodeFromString C.PyUnicode_FromString
func UnicodeFromString(str *Char) *Object

// PyObject* PyUnicode_FromFormat(const char *format, ...)
// Take a C :c:func:`printf`\ -style *format* string and a variable number of
// arguments, calculate the size of the resulting Python Unicode string and return
// a string with the values formatted into it.  The variable arguments must be C
// types and must correspond exactly to the format characters in the *format*
// ASCII-encoded string.
//
// A conversion specifier contains two or more characters and has the following
// components, which must occur in this order:
//
// #. The “'%'“ character, which marks the start of the specifier.
//
// #. Conversion flags (optional), which affect the result of some conversion
// types.
//
// #. Minimum field width (optional).
// If specified as an “'*'“ (asterisk), the actual width is given in the
// next argument, which must be of type :c:expr:`int`, and the object to
// convert comes after the minimum field width and optional precision.
//
// #. Precision (optional), given as a “'.'“ (dot) followed by the precision.
// If specified as “'*'“ (an asterisk), the actual precision is given in
// the next argument, which must be of type :c:expr:`int`, and the value to
// convert comes after the precision.
//
// #. Length modifier (optional).
//
// #. Conversion type.
//
// The conversion flag characters are:
//
// .. tabularcolumns:: |l|L|
//
// +-------+-------------------------------------------------------------+
// | Flag  | Meaning                                                     |
// +=======+=============================================================+
// | “0“ | The conversion will be zero padded for numeric values.      |
// +-------+-------------------------------------------------------------+
// | “-“ | The converted value is left adjusted (overrides the “0“   |
// |       | flag if both are given).                                    |
// +-------+-------------------------------------------------------------+
//
// The length modifiers for following integer conversions (“d“, “i“,
// “o“, “u“, “x“, or “X“) specify the type of the argument
// (:c:expr:`int` by default):
//
// .. tabularcolumns:: |l|L|
//
// +----------+-----------------------------------------------------+
// | Modifier | Types                                               |
// +==========+=====================================================+
// | “l“    | :c:expr:`long` or :c:expr:`unsigned long`           |
// +----------+-----------------------------------------------------+
// | “ll“   | :c:expr:`long long` or :c:expr:`unsigned long long` |
// +----------+-----------------------------------------------------+
// | “j“    | :c:type:`intmax_t` or :c:type:`uintmax_t`           |
// +----------+-----------------------------------------------------+
// | “z“    | :c:type:`size_t` or :c:type:`ssize_t`               |
// +----------+-----------------------------------------------------+
// | “t“    | :c:type:`ptrdiff_t`                                 |
// +----------+-----------------------------------------------------+
//
// The length modifier “l“ for following conversions “s“ or “V“ specify
// that the type of the argument is :c:expr:`const wchar_t*`.
//
// The conversion specifiers are:
//
// .. list-table::
// :widths: auto
// :header-rows: 1
//
// * - Conversion Specifier
// - Type
// - Comment
//
// * - “%“
// - *n/a*
// - The literal “%“ character.
//
// * - “d“, “i“
// - Specified by the length modifier
// - The decimal representation of a signed C integer.
//
// * - “u“
// - Specified by the length modifier
// - The decimal representation of an unsigned C integer.
//
// * - “o“
// - Specified by the length modifier
// - The octal representation of an unsigned C integer.
//
// * - “x“
// - Specified by the length modifier
// - The hexadecimal representation of an unsigned C integer (lowercase).
//
// * - “X“
// - Specified by the length modifier
// - The hexadecimal representation of an unsigned C integer (uppercase).
//
// * - “c“
// - :c:expr:`int`
// - A single character.
//
// * - “s“
// - :c:expr:`const char*` or :c:expr:`const wchar_t*`
// - A null-terminated C character array.
//
// * - “p“
// - :c:expr:`const void*`
// - The hex representation of a C  pointer.
// Mostly equivalent to “printf("%p")“ except that it is guaranteed to
// start with the literal “0x“ regardless of what the platform's
// “printf“ yields.
//
// * - “A“
// - :c:expr:`PyObject*`
// - The result of calling :func:`ascii`.
//
// * - “U“
// - :c:expr:`PyObject*`
// - A Unicode object.
//
// * - “V“
// - :c:expr:`PyObject*`, :c:expr:`const char*` or :c:expr:`const wchar_t*`
// - A Unicode object (which may be “NULL“) and a null-terminated
// C character array as a second parameter (which will be used,
// if the first parameter is “NULL“).
//
// * - “S“
// - :c:expr:`PyObject*`
// - The result of calling :c:func:`PyObject_Str`.
//
// * - “R“
// - :c:expr:`PyObject*`
// - The result of calling :c:func:`PyObject_Repr`.
//
// * - “T“
// - :c:expr:`PyObject*`
// - Get the fully qualified name of an object type;
// call :c:func:`PyType_GetFullyQualifiedName`.
//
// * - “#T“
// - :c:expr:`PyObject*`
// - Similar to “T“ format, but use a colon (“:“) as separator between
// the module name and the qualified name.
//
// * - “N“
// - :c:expr:`PyTypeObject*`
// - Get the fully qualified name of a type;
// call :c:func:`PyType_GetFullyQualifiedName`.
//
// * - “#N“
// - :c:expr:`PyTypeObject*`
// - Similar to “N“ format, but use a colon (“:“) as separator between
// the module name and the qualified name.
//
// .. note::
// The width formatter unit is number of characters rather than bytes.
// The precision formatter unit is number of bytes or :c:type:`wchar_t`
// items (if the length modifier “l“ is used) for “"%s"“ and
// “"%V"“ (if the “PyObject*“ argument is “NULL“), and a number of
// characters for “"%A"“, “"%U"“, “"%S"“, “"%R"“ and “"%V"“
// (if the “PyObject*“ argument is not “NULL“).
//
// .. note::
// Unlike to C :c:func:`printf` the “0“ flag has effect even when
// a precision is given for integer conversions (“d“, “i“, “u“, “o“,
// “x“, or “X“).
//
// Support for “"%lld"“ and “"%llu"“ added.
//
// Support for “"%li"“, “"%lli"“ and “"%zi"“ added.
//
// Support width and precision formatter for “"%s"“, “"%A"“, “"%U"“,
// “"%V"“, “"%S"“, “"%R"“ added.
//
// Support for conversion specifiers “o“ and “X“.
// Support for length modifiers “j“ and “t“.
// Length modifiers are now applied to all integer conversions.
// Length modifier “l“ is now applied to conversion specifiers “s“ and “V“.
// Support for variable width and precision “*“.
// Support for flag “-“.
//
// An unrecognized format character now sets a :exc:`SystemError`.
// In previous versions it caused all the rest of the format string to be
// copied as-is to the result string, and any extra arguments discarded.
//
// Support for “%T“, “%#T“, “%N“ and “%#N“ formats added.
//
//go:linkname UnicodeFromFormat C.PyUnicode_FromFormat
func UnicodeFromFormat(format *Char, __llgo_va_list ...any) *Object

// PyObject* PyUnicode_FromObject(PyObject *obj)
// Copy an instance of a Unicode subtype to a new true Unicode object if
// necessary. If *obj* is already a true Unicode object (not a subtype),
// return a new :term:`strong reference` to the object.
//
// Objects other than Unicode or its subtypes will cause a :exc:`TypeError`.
//
//go:linkname UnicodeFromObject C.PyUnicode_FromObject
func UnicodeFromObject(obj *Object) *Object

// PyObject* PyUnicode_FromEncodedObject(PyObject *obj, \
// const char *encoding, const char *errors)
//
// Decode an encoded object *obj* to a Unicode object.
//
// :class:`bytes`, :class:`bytearray` and other
// :term:`bytes-like objects <bytes-like object>`
// are decoded according to the given *encoding* and using the error handling
// defined by *errors*. Both can be “NULL“ to have the interface use the default
// values (see :ref:`builtincodecs` for details).
//
// All other objects, including Unicode objects, cause a :exc:`TypeError` to be
// set.
//
// The API returns “NULL“ if there was an error.  The caller is responsible for
// decref'ing the returned objects.
//
//go:linkname UnicodeFromEncodedObject C.PyUnicode_FromEncodedObject
func UnicodeFromEncodedObject(obj *Object) *Object

// Py_ssize_t PyUnicode_GetLength(PyObject *unicode)
// Return the length of the Unicode object, in code points.
//
// On error, set an exception and return “-1“.
//
//go:linkname UnicodeGetLength C.PyUnicode_GetLength
func UnicodeGetLength(unicode *Object) SSizeT

// Py_ssize_t PyUnicode_CopyCharacters(PyObject *to, \
// Py_ssize_t to_start, \
// PyObject *from, \
// Py_ssize_t from_start, \
// Py_ssize_t how_many)
//
// Copy characters from one Unicode object into another.  This function performs
// character conversion when necessary and falls back to :c:func:`!memcpy` if
// possible.  Returns “-1“ and sets an exception on error, otherwise returns
// the number of copied characters.
//
//go:linkname UnicodeCopyCharacters C.PyUnicode_CopyCharacters
func UnicodeCopyCharacters(to *Object) SSizeT

// Py_ssize_t PyUnicode_Fill(PyObject *unicode, Py_ssize_t start, \
// Py_ssize_t length, Py_UCS4 fill_char)
//
// Fill a string with a character: write *fill_char* into
// “unicode[start:start+length]“.
//
// Fail if *fill_char* is bigger than the string maximum character, or if the
// string has more than 1 reference.
//
// Return the number of written character, or return “-1“ and raise an
// exception on error.
//
//go:linkname UnicodeFill C.PyUnicode_Fill
func UnicodeFill(unicode *Object, start SSizeT) SSizeT

// int PyUnicode_WriteChar(PyObject *unicode, Py_ssize_t index, \
// Py_UCS4 character)
//
// Write a character to a string.  The string must have been created through
// :c:func:`PyUnicode_New`.  Since Unicode strings are supposed to be immutable,
// the string must not be shared, or have been hashed yet.
//
// This function checks that *unicode* is a Unicode object, that the index is
// not out of bounds, and that the object can be modified safely (i.e. that it
// its reference count is one).
//
// Return “0“ on success, “-1“ on error with an exception set.
//
//go:linkname UnicodeWriteChar C.PyUnicode_WriteChar
func UnicodeWriteChar(unicode *Object, index SSizeT) Int

// Py_UCS4 PyUnicode_ReadChar(PyObject *unicode, Py_ssize_t index)
// Read a character from a string.  This function checks that *unicode* is a
// Unicode object and the index is not out of bounds, in contrast to
// :c:func:`PyUnicode_READ_CHAR`, which performs no error checking.
//
// Return character on success, “-1“ on error with an exception set.
//
//go:linkname UnicodeReadChar C.PyUnicode_ReadChar
func UnicodeReadChar(unicode *Object, index SSizeT) UCS4

// PyObject* PyUnicode_Substring(PyObject *unicode, Py_ssize_t start, \
// Py_ssize_t end)
//
// Return a substring of *unicode*, from character index *start* (included) to
// character index *end* (excluded).  Negative indices are not supported.
// On error, set an exception and return “NULL“.
//
//go:linkname UnicodeSubstring C.PyUnicode_Substring
func UnicodeSubstring(unicode *Object, start SSizeT) *Object

// Py_UCS4* PyUnicode_AsUCS4(PyObject *unicode, Py_UCS4 *buffer, \
// Py_ssize_t buflen, int copy_null)
//
// Copy the string *unicode* into a UCS4 buffer, including a null character, if
// *copy_null* is set.  Returns “NULL“ and sets an exception on error (in
// particular, a :exc:`SystemError` if *buflen* is smaller than the length of
// *unicode*).  *buffer* is returned on success.
//
//go:linkname UnicodeAsUCS4 C.PyUnicode_AsUCS4
func UnicodeAsUCS4(unicode *Object, buffer *UCS4) *UCS4

// Py_UCS4* PyUnicode_AsUCS4Copy(PyObject *unicode)
// Copy the string *unicode* into a new UCS4 buffer that is allocated using
// :c:func:`PyMem_Malloc`.  If this fails, “NULL“ is returned with a
// :exc:`MemoryError` set.  The returned buffer always has an extra
// null code point appended.
//
// Locale Encoding
// """""""""""""""
//
// The current locale encoding can be used to decode text from the operating
// system.
//
//go:linkname UnicodeAsUCS4Copy C.PyUnicode_AsUCS4Copy
func UnicodeAsUCS4Copy(unicode *Object) *UCS4

// PyObject* PyUnicode_DecodeLocaleAndSize(const char *str, \
// Py_ssize_t length, \
// const char *errors)
//
// Decode a string from UTF-8 on Android and VxWorks, or from the current
// locale encoding on other platforms. The supported
// error handlers are “"strict"“ and “"surrogateescape"“
// (:pep:`383`). The decoder uses “"strict"“ error handler if
// *errors* is “NULL“.  *str* must end with a null character but
// cannot contain embedded null characters.
//
// Use :c:func:`PyUnicode_DecodeFSDefaultAndSize` to decode a string from
// the :term:`filesystem encoding and error handler`.
//
// This function ignores the :ref:`Python UTF-8 Mode <utf8-mode>`.
//
// .. seealso::
//
// The :c:func:`Py_DecodeLocale` function.
//
// The function now also uses the current locale encoding for the
// “surrogateescape“ error handler, except on Android. Previously, :c:func:`Py_DecodeLocale`
// was used for the “surrogateescape“, and the current locale encoding was
// used for “strict“.
//
//go:linkname UnicodeDecodeLocaleAndSize C.PyUnicode_DecodeLocaleAndSize
func UnicodeDecodeLocaleAndSize(str *Char) *Object

// PyObject* PyUnicode_DecodeLocale(const char *str, const char *errors)
// Similar to :c:func:`PyUnicode_DecodeLocaleAndSize`, but compute the string
// length using :c:func:`!strlen`.
//
//go:linkname UnicodeDecodeLocale C.PyUnicode_DecodeLocale
func UnicodeDecodeLocale(str *Char, errors *Char) *Object

// PyObject* PyUnicode_EncodeLocale(PyObject *unicode, const char *errors)
// Encode a Unicode object to UTF-8 on Android and VxWorks, or to the current
// locale encoding on other platforms. The
// supported error handlers are “"strict"“ and “"surrogateescape"“
// (:pep:`383`). The encoder uses “"strict"“ error handler if
// *errors* is “NULL“. Return a :class:`bytes` object. *unicode* cannot
// contain embedded null characters.
//
// Use :c:func:`PyUnicode_EncodeFSDefault` to encode a string to the
// :term:`filesystem encoding and error handler`.
//
// This function ignores the :ref:`Python UTF-8 Mode <utf8-mode>`.
//
// .. seealso::
//
// The :c:func:`Py_EncodeLocale` function.
//
// The function now also uses the current locale encoding for the
// “surrogateescape“ error handler, except on Android. Previously,
// :c:func:`Py_EncodeLocale`
// was used for the “surrogateescape“, and the current locale encoding was
// used for “strict“.
//
// File System Encoding
// """"""""""""""""""""
//
// Functions encoding to and decoding from the :term:`filesystem encoding and
// error handler` (:pep:`383` and :pep:`529`).
//
// To encode file names to :class:`bytes` during argument parsing, the “"O&"“
// converter should be used, passing :c:func:`PyUnicode_FSConverter` as the
// conversion function:
//
//go:linkname UnicodeEncodeLocale C.PyUnicode_EncodeLocale
func UnicodeEncodeLocale(unicode *Object, errors *Char) *Object

// int PyUnicode_FSConverter(PyObject* obj, void* result)
// ParseTuple converter: encode :class:`str` objects -- obtained directly or
// through the :class:`os.PathLike` interface -- to :class:`bytes` using
// :c:func:`PyUnicode_EncodeFSDefault`; :class:`bytes` objects are output as-is.
// *result* must be a :c:expr:`PyBytesObject*` which must be released when it is
// no longer used.
//
// Accepts a :term:`path-like object`.
//
// To decode file names to :class:`str` during argument parsing, the “"O&"“
// converter should be used, passing :c:func:`PyUnicode_FSDecoder` as the
// conversion function:
//
//go:linkname UnicodeFSConverter C.PyUnicode_FSConverter
func UnicodeFSConverter(obj *Object, result Pointer) Int

// int PyUnicode_FSDecoder(PyObject* obj, void* result)
// ParseTuple converter: decode :class:`bytes` objects -- obtained either
// directly or indirectly through the :class:`os.PathLike` interface -- to
// :class:`str` using :c:func:`PyUnicode_DecodeFSDefaultAndSize`; :class:`str`
// objects are output as-is. *result* must be a :c:expr:`PyUnicodeObject*` which
// must be released when it is no longer used.
//
// Accepts a :term:`path-like object`.
//
//go:linkname UnicodeFSDecoder C.PyUnicode_FSDecoder
func UnicodeFSDecoder(obj *Object, result Pointer) Int

// PyObject* PyUnicode_DecodeFSDefaultAndSize(const char *str, Py_ssize_t size)
// Decode a string from the :term:`filesystem encoding and error handler`.
//
// If you need to decode a string from the current locale encoding, use
// :c:func:`PyUnicode_DecodeLocaleAndSize`.
//
// .. seealso::
//
// The :c:func:`Py_DecodeLocale` function.
//
// The :term:`filesystem error handler <filesystem encoding and error
// handler>` is now used.
//
//go:linkname UnicodeDecodeFSDefaultAndSize C.PyUnicode_DecodeFSDefaultAndSize
func UnicodeDecodeFSDefaultAndSize(str *Char, size SSizeT) *Object

// PyObject* PyUnicode_DecodeFSDefault(const char *str)
// Decode a null-terminated string from the :term:`filesystem encoding and
// error handler`.
//
// If the string length is known, use
// :c:func:`PyUnicode_DecodeFSDefaultAndSize`.
//
// The :term:`filesystem error handler <filesystem encoding and error
// handler>` is now used.
//
//go:linkname UnicodeDecodeFSDefault C.PyUnicode_DecodeFSDefault
func UnicodeDecodeFSDefault(str *Char) *Object

// PyObject* PyUnicode_EncodeFSDefault(PyObject *unicode)
// Encode a Unicode object to the :term:`filesystem encoding and error
// handler`, and return :class:`bytes`. Note that the resulting :class:`bytes`
// object can contain null bytes.
//
// If you need to encode a string to the current locale encoding, use
// :c:func:`PyUnicode_EncodeLocale`.
//
// .. seealso::
//
// The :c:func:`Py_EncodeLocale` function.
//
// The :term:`filesystem error handler <filesystem encoding and error
// handler>` is now used.
//
// wchar_t Support
// """""""""""""""
//
// :c:type:`wchar_t` support for platforms which support it:
//
//go:linkname UnicodeEncodeFSDefault C.PyUnicode_EncodeFSDefault
func UnicodeEncodeFSDefault(unicode *Object) *Object

// PyObject* PyUnicode_FromWideChar(const wchar_t *wstr, Py_ssize_t size)
// Create a Unicode object from the :c:type:`wchar_t` buffer *wstr* of the given *size*.
// Passing “-1“ as the *size* indicates that the function must itself compute the length,
// using :c:func:`!wcslen`.
// Return “NULL“ on failure.
//
//go:linkname UnicodeFromWideChar C.PyUnicode_FromWideChar
func UnicodeFromWideChar(wstr *Wchar, size SSizeT) *Object

// Py_ssize_t PyUnicode_AsWideChar(PyObject *unicode, wchar_t *wstr, Py_ssize_t size)
// Copy the Unicode object contents into the :c:type:`wchar_t` buffer *wstr*.  At most
// *size* :c:type:`wchar_t` characters are copied (excluding a possibly trailing
// null termination character).  Return the number of :c:type:`wchar_t` characters
// copied or “-1“ in case of an error.
//
// When *wstr* is “NULL“, instead return the *size* that would be required
// to store all of *unicode* including a terminating null.
//
// Note that the resulting :c:expr:`wchar_t*`
// string may or may not be null-terminated.  It is the responsibility of the caller
// to make sure that the :c:expr:`wchar_t*` string is null-terminated in case this is
// required by the application. Also, note that the :c:expr:`wchar_t*` string
// might contain null characters, which would cause the string to be truncated
// when used with most C functions.
//
//go:linkname UnicodeAsWideChar C.PyUnicode_AsWideChar
func UnicodeAsWideChar(unicode *Object, wstr *Wchar, size SSizeT) SSizeT

// wchar_t* PyUnicode_AsWideCharString(PyObject *unicode, Py_ssize_t *size)
// Convert the Unicode object to a wide character string. The output string
// always ends with a null character. If *size* is not “NULL“, write the number
// of wide characters (excluding the trailing null termination character) into
// *\*size*. Note that the resulting :c:type:`wchar_t` string might contain
// null characters, which would cause the string to be truncated when used with
// most C functions. If *size* is “NULL“ and the :c:expr:`wchar_t*` string
// contains null characters a :exc:`ValueError` is raised.
//
// Returns a buffer allocated by :c:macro:`PyMem_New` (use
// :c:func:`PyMem_Free` to free it) on success. On error, returns “NULL“
// and *\*size* is undefined. Raises a :exc:`MemoryError` if memory allocation
// is failed.
//
// Raises a :exc:`ValueError` if *size* is “NULL“ and the :c:expr:`wchar_t*`
// string contains null characters.
//
// .. _builtincodecs:
//
// Built-in Codecs
// ^^^^^^^^^^^^^^^
//
// Python provides a set of built-in codecs which are written in C for speed. All of
// these codecs are directly usable via the following functions.
//
// Many of the following APIs take two arguments encoding and errors, and they
// have the same semantics as the ones of the built-in :func:`str` string object
// constructor.
//
// Setting encoding to “NULL“ causes the default encoding to be used
// which is UTF-8.  The file system calls should use
// :c:func:`PyUnicode_FSConverter` for encoding file names. This uses the
// :term:`filesystem encoding and error handler` internally.
//
// Error handling is set by errors which may also be set to “NULL“ meaning to use
// the default handling defined for the codec.  Default error handling for all
// built-in codecs is "strict" (:exc:`ValueError` is raised).
//
// The codecs all use a similar interface.  Only deviations from the following
// generic ones are documented for simplicity.
//
// Generic Codecs
// """"""""""""""
//
// These are the generic codec APIs:
//
//go:linkname UnicodeAsWideCharString C.PyUnicode_AsWideCharString
func UnicodeAsWideCharString(unicode *Object, size *SSizeT) *Wchar

// PyObject* PyUnicode_Decode(const char *str, Py_ssize_t size, \
// const char *encoding, const char *errors)
//
// Create a Unicode object by decoding *size* bytes of the encoded string *str*.
// *encoding* and *errors* have the same meaning as the parameters of the same name
// in the :func:`str` built-in function.  The codec to be used is looked up
// using the Python codec registry.  Return “NULL“ if an exception was raised by
// the codec.
//
//go:linkname UnicodeDecode C.PyUnicode_Decode
func UnicodeDecode(str *Char, size SSizeT) *Object

// PyObject* PyUnicode_AsEncodedString(PyObject *unicode, \
// const char *encoding, const char *errors)
//
// Encode a Unicode object and return the result as Python bytes object.
// *encoding* and *errors* have the same meaning as the parameters of the same
// name in the Unicode :meth:`~str.encode` method. The codec to be used is looked up
// using the Python codec registry. Return “NULL“ if an exception was raised by
// the codec.
//
// UTF-8 Codecs
// """"""""""""
//
// These are the UTF-8 codec APIs:
//
//go:linkname UnicodeAsEncodedString C.PyUnicode_AsEncodedString
func UnicodeAsEncodedString(unicode *Object) *Object

// PyObject* PyUnicode_DecodeUTF8(const char *str, Py_ssize_t size, const char *errors)
// Create a Unicode object by decoding *size* bytes of the UTF-8 encoded string
// *str*. Return “NULL“ if an exception was raised by the codec.
//
//go:linkname UnicodeDecodeUTF8 C.PyUnicode_DecodeUTF8
func UnicodeDecodeUTF8(str *Char, size SSizeT, errors *Char) *Object

// PyObject* PyUnicode_DecodeUTF8Stateful(const char *str, Py_ssize_t size, \
// const char *errors, Py_ssize_t *consumed)
//
// If *consumed* is “NULL“, behave like :c:func:`PyUnicode_DecodeUTF8`. If
// *consumed* is not “NULL“, trailing incomplete UTF-8 byte sequences will not be
// treated as an error. Those bytes will not be decoded and the number of bytes
// that have been decoded will be stored in *consumed*.
//
//go:linkname UnicodeDecodeUTF8Stateful C.PyUnicode_DecodeUTF8Stateful
func UnicodeDecodeUTF8Stateful(str *Char, size SSizeT) *Object

// PyObject* PyUnicode_AsUTF8String(PyObject *unicode)
// Encode a Unicode object using UTF-8 and return the result as Python bytes
// object.  Error handling is "strict".  Return “NULL“ if an exception was
// raised by the codec.
//
// The function fails if the string contains surrogate code points
// (“U+D800“ - “U+DFFF“).
//
//go:linkname UnicodeAsUTF8String C.PyUnicode_AsUTF8String
func UnicodeAsUTF8String(unicode *Object) *Object

// const char* PyUnicode_AsUTF8AndSize(PyObject *unicode, Py_ssize_t *size)
// Return a pointer to the UTF-8 encoding of the Unicode object, and
// store the size of the encoded representation (in bytes) in *size*.  The
// *size* argument can be “NULL“; in this case no size will be stored.  The
// returned buffer always has an extra null byte appended (not included in
// *size*), regardless of whether there are any other null code points.
//
// On error, set an exception, set *size* to “-1“ (if it's not NULL) and
// return “NULL“.
//
// The function fails if the string contains surrogate code points
// (“U+D800“ - “U+DFFF“).
//
// This caches the UTF-8 representation of the string in the Unicode object, and
// subsequent calls will return a pointer to the same buffer.  The caller is not
// responsible for deallocating the buffer. The buffer is deallocated and
// pointers to it become invalid when the Unicode object is garbage collected.
//
// The return type is now “const char *“ rather of “char *“.
//
// This function is a part of the :ref:`limited API <limited-c-api>`.
//
//go:linkname UnicodeAsUTF8AndSize C.PyUnicode_AsUTF8AndSize
func UnicodeAsUTF8AndSize(unicode *Object, size *SSizeT) *Char

// const char* PyUnicode_AsUTF8(PyObject *unicode)
// As :c:func:`PyUnicode_AsUTF8AndSize`, but does not store the size.
//
// The return type is now “const char *“ rather of “char *“.
//
// UTF-32 Codecs
// """""""""""""
//
// These are the UTF-32 codec APIs:
//
//go:linkname UnicodeAsUTF8 C.PyUnicode_AsUTF8
func UnicodeAsUTF8(unicode *Object) *Char

// PyObject* PyUnicode_DecodeUTF32(const char *str, Py_ssize_t size, \
// const char *errors, int *byteorder)
//
// Decode *size* bytes from a UTF-32 encoded buffer string and return the
// corresponding Unicode object.  *errors* (if non-“NULL“) defines the error
// handling. It defaults to "strict".
//
// If *byteorder* is non-“NULL“, the decoder starts decoding using the given byte
// order::
//
// *byteorder == -1: little endian
// *byteorder == 0:  native order
// *byteorder == 1:  big endian
//
// If “*byteorder“ is zero, and the first four bytes of the input data are a
// byte order mark (BOM), the decoder switches to this byte order and the BOM is
// not copied into the resulting Unicode string.  If “*byteorder“ is “-1“ or
// “1“, any byte order mark is copied to the output.
//
// After completion, *\*byteorder* is set to the current byte order at the end
// of input data.
//
// If *byteorder* is “NULL“, the codec starts in native order mode.
//
// Return “NULL“ if an exception was raised by the codec.
//
//go:linkname UnicodeDecodeUTF32 C.PyUnicode_DecodeUTF32
func UnicodeDecodeUTF32(str *Char, size SSizeT) *Object

// PyObject* PyUnicode_DecodeUTF32Stateful(const char *str, Py_ssize_t size, \
// const char *errors, int *byteorder, Py_ssize_t *consumed)
//
// If *consumed* is “NULL“, behave like :c:func:`PyUnicode_DecodeUTF32`. If
// *consumed* is not “NULL“, :c:func:`PyUnicode_DecodeUTF32Stateful` will not treat
// trailing incomplete UTF-32 byte sequences (such as a number of bytes not divisible
// by four) as an error. Those bytes will not be decoded and the number of bytes
// that have been decoded will be stored in *consumed*.
//
//go:linkname UnicodeDecodeUTF32Stateful C.PyUnicode_DecodeUTF32Stateful
func UnicodeDecodeUTF32Stateful(str *Char, size SSizeT) *Object

// PyObject* PyUnicode_AsUTF32String(PyObject *unicode)
// Return a Python byte string using the UTF-32 encoding in native byte
// order. The string always starts with a BOM mark.  Error handling is "strict".
// Return “NULL“ if an exception was raised by the codec.
//
// UTF-16 Codecs
// """""""""""""
//
// These are the UTF-16 codec APIs:
//
//go:linkname UnicodeAsUTF32String C.PyUnicode_AsUTF32String
func UnicodeAsUTF32String(unicode *Object) *Object

// PyObject* PyUnicode_DecodeUTF16(const char *str, Py_ssize_t size, \
// const char *errors, int *byteorder)
//
// Decode *size* bytes from a UTF-16 encoded buffer string and return the
// corresponding Unicode object.  *errors* (if non-“NULL“) defines the error
// handling. It defaults to "strict".
//
// If *byteorder* is non-“NULL“, the decoder starts decoding using the given byte
// order::
//
// *byteorder == -1: little endian
// *byteorder == 0:  native order
// *byteorder == 1:  big endian
//
// If “*byteorder“ is zero, and the first two bytes of the input data are a
// byte order mark (BOM), the decoder switches to this byte order and the BOM is
// not copied into the resulting Unicode string.  If “*byteorder“ is “-1“ or
// “1“, any byte order mark is copied to the output (where it will result in
// either a “\ufeff“ or a “\ufffe“ character).
//
// After completion, “*byteorder“ is set to the current byte order at the end
// of input data.
//
// If *byteorder* is “NULL“, the codec starts in native order mode.
//
// Return “NULL“ if an exception was raised by the codec.
//
//go:linkname UnicodeDecodeUTF16 C.PyUnicode_DecodeUTF16
func UnicodeDecodeUTF16(str *Char, size SSizeT) *Object

// PyObject* PyUnicode_DecodeUTF16Stateful(const char *str, Py_ssize_t size, \
// const char *errors, int *byteorder, Py_ssize_t *consumed)
//
// If *consumed* is “NULL“, behave like :c:func:`PyUnicode_DecodeUTF16`. If
// *consumed* is not “NULL“, :c:func:`PyUnicode_DecodeUTF16Stateful` will not treat
// trailing incomplete UTF-16 byte sequences (such as an odd number of bytes or a
// split surrogate pair) as an error. Those bytes will not be decoded and the
// number of bytes that have been decoded will be stored in *consumed*.
//
//go:linkname UnicodeDecodeUTF16Stateful C.PyUnicode_DecodeUTF16Stateful
func UnicodeDecodeUTF16Stateful(str *Char, size SSizeT) *Object

// PyObject* PyUnicode_AsUTF16String(PyObject *unicode)
// Return a Python byte string using the UTF-16 encoding in native byte
// order. The string always starts with a BOM mark.  Error handling is "strict".
// Return “NULL“ if an exception was raised by the codec.
//
// UTF-7 Codecs
// """"""""""""
//
// These are the UTF-7 codec APIs:
//
//go:linkname UnicodeAsUTF16String C.PyUnicode_AsUTF16String
func UnicodeAsUTF16String(unicode *Object) *Object

// PyObject* PyUnicode_DecodeUTF7(const char *str, Py_ssize_t size, const char *errors)
// Create a Unicode object by decoding *size* bytes of the UTF-7 encoded string
// *str*.  Return “NULL“ if an exception was raised by the codec.
//
//go:linkname UnicodeDecodeUTF7 C.PyUnicode_DecodeUTF7
func UnicodeDecodeUTF7(str *Char, size SSizeT, errors *Char) *Object

// PyObject* PyUnicode_DecodeUTF7Stateful(const char *str, Py_ssize_t size, \
// const char *errors, Py_ssize_t *consumed)
//
// If *consumed* is “NULL“, behave like :c:func:`PyUnicode_DecodeUTF7`.  If
// *consumed* is not “NULL“, trailing incomplete UTF-7 base-64 sections will not
// be treated as an error.  Those bytes will not be decoded and the number of
// bytes that have been decoded will be stored in *consumed*.
//
// Unicode-Escape Codecs
// """""""""""""""""""""
//
// These are the "Unicode Escape" codec APIs:
//
//go:linkname UnicodeDecodeUTF7Stateful C.PyUnicode_DecodeUTF7Stateful
func UnicodeDecodeUTF7Stateful(str *Char, size SSizeT) *Object

// PyObject* PyUnicode_DecodeUnicodeEscape(const char *str, \
// Py_ssize_t size, const char *errors)
//
// Create a Unicode object by decoding *size* bytes of the Unicode-Escape encoded
// string *str*.  Return “NULL“ if an exception was raised by the codec.
//
//go:linkname UnicodeDecodeUnicodeEscape C.PyUnicode_DecodeUnicodeEscape
func UnicodeDecodeUnicodeEscape(str *Char) *Object

// PyObject* PyUnicode_AsUnicodeEscapeString(PyObject *unicode)
// Encode a Unicode object using Unicode-Escape and return the result as a
// bytes object.  Error handling is "strict".  Return “NULL“ if an exception was
// raised by the codec.
//
// Raw-Unicode-Escape Codecs
// """""""""""""""""""""""""
//
// These are the "Raw Unicode Escape" codec APIs:
//
//go:linkname UnicodeAsUnicodeEscapeString C.PyUnicode_AsUnicodeEscapeString
func UnicodeAsUnicodeEscapeString(unicode *Object) *Object

// PyObject* PyUnicode_DecodeRawUnicodeEscape(const char *str, \
// Py_ssize_t size, const char *errors)
//
// Create a Unicode object by decoding *size* bytes of the Raw-Unicode-Escape
// encoded string *str*.  Return “NULL“ if an exception was raised by the codec.
//
//go:linkname UnicodeDecodeRawUnicodeEscape C.PyUnicode_DecodeRawUnicodeEscape
func UnicodeDecodeRawUnicodeEscape(str *Char) *Object

// PyObject* PyUnicode_AsRawUnicodeEscapeString(PyObject *unicode)
// Encode a Unicode object using Raw-Unicode-Escape and return the result as
// a bytes object.  Error handling is "strict".  Return “NULL“ if an exception
// was raised by the codec.
//
// Latin-1 Codecs
// """"""""""""""
//
// These are the Latin-1 codec APIs: Latin-1 corresponds to the first 256 Unicode
// ordinals and only these are accepted by the codecs during encoding.
//
//go:linkname UnicodeAsRawUnicodeEscapeString C.PyUnicode_AsRawUnicodeEscapeString
func UnicodeAsRawUnicodeEscapeString(unicode *Object) *Object

// PyObject* PyUnicode_DecodeLatin1(const char *str, Py_ssize_t size, const char *errors)
// Create a Unicode object by decoding *size* bytes of the Latin-1 encoded string
// *str*.  Return “NULL“ if an exception was raised by the codec.
//
//go:linkname UnicodeDecodeLatin1 C.PyUnicode_DecodeLatin1
func UnicodeDecodeLatin1(str *Char, size SSizeT, errors *Char) *Object

// PyObject* PyUnicode_AsLatin1String(PyObject *unicode)
// Encode a Unicode object using Latin-1 and return the result as Python bytes
// object.  Error handling is "strict".  Return “NULL“ if an exception was
// raised by the codec.
//
// ASCII Codecs
// """"""""""""
//
// These are the ASCII codec APIs.  Only 7-bit ASCII data is accepted. All other
// codes generate errors.
//
//go:linkname UnicodeAsLatin1String C.PyUnicode_AsLatin1String
func UnicodeAsLatin1String(unicode *Object) *Object

// PyObject* PyUnicode_DecodeASCII(const char *str, Py_ssize_t size, const char *errors)
// Create a Unicode object by decoding *size* bytes of the ASCII encoded string
// *str*.  Return “NULL“ if an exception was raised by the codec.
//
//go:linkname UnicodeDecodeASCII C.PyUnicode_DecodeASCII
func UnicodeDecodeASCII(str *Char, size SSizeT, errors *Char) *Object

// PyObject* PyUnicode_AsASCIIString(PyObject *unicode)
// Encode a Unicode object using ASCII and return the result as Python bytes
// object.  Error handling is "strict".  Return “NULL“ if an exception was
// raised by the codec.
//
// Character Map Codecs
// """"""""""""""""""""
//
// This codec is special in that it can be used to implement many different codecs
// (and this is in fact what was done to obtain most of the standard codecs
// included in the :mod:`!encodings` package). The codec uses mappings to encode and
// decode characters.  The mapping objects provided must support the
// :meth:`~object.__getitem__` mapping interface; dictionaries and sequences work well.
//
// These are the mapping codec APIs:
//
//go:linkname UnicodeAsASCIIString C.PyUnicode_AsASCIIString
func UnicodeAsASCIIString(unicode *Object) *Object

// PyObject* PyUnicode_DecodeCharmap(const char *str, Py_ssize_t length, \
// PyObject *mapping, const char *errors)
//
// Create a Unicode object by decoding *size* bytes of the encoded string *str*
// using the given *mapping* object.  Return “NULL“ if an exception was raised
// by the codec.
//
// If *mapping* is “NULL“, Latin-1 decoding will be applied.  Else
// *mapping* must map bytes ordinals (integers in the range from 0 to 255)
// to Unicode strings, integers (which are then interpreted as Unicode
// ordinals) or “None“.  Unmapped data bytes -- ones which cause a
// :exc:`LookupError`, as well as ones which get mapped to “None“,
// “0xFFFE“ or “'\ufffe'“, are treated as undefined mappings and cause
// an error.
//
//go:linkname UnicodeDecodeCharmap C.PyUnicode_DecodeCharmap
func UnicodeDecodeCharmap(str *Char, length SSizeT) *Object

// PyObject* PyUnicode_AsCharmapString(PyObject *unicode, PyObject *mapping)
// Encode a Unicode object using the given *mapping* object and return the
// result as a bytes object.  Error handling is "strict".  Return “NULL“ if an
// exception was raised by the codec.
//
// The *mapping* object must map Unicode ordinal integers to bytes objects,
// integers in the range from 0 to 255 or “None“.  Unmapped character
// ordinals (ones which cause a :exc:`LookupError`) as well as mapped to
// “None“ are treated as "undefined mapping" and cause an error.
//
// The following codec API is special in that maps Unicode to Unicode.
//
//go:linkname UnicodeAsCharmapString C.PyUnicode_AsCharmapString
func UnicodeAsCharmapString(unicode *Object, mapping *Object) *Object

// PyObject* PyUnicode_Translate(PyObject *unicode, PyObject *table, const char *errors)
// Translate a string by applying a character mapping table to it and return the
// resulting Unicode object. Return “NULL“ if an exception was raised by the
// codec.
//
// The mapping table must map Unicode ordinal integers to Unicode ordinal integers
// or “None“ (causing deletion of the character).
//
// Mapping tables need only provide the :meth:`~object.__getitem__` interface; dictionaries
// and sequences work well.  Unmapped character ordinals (ones which cause a
// :exc:`LookupError`) are left untouched and are copied as-is.
//
// *errors* has the usual meaning for codecs. It may be “NULL“ which indicates to
// use the default error handling.
//
// MBCS codecs for Windows
// """""""""""""""""""""""
//
// These are the MBCS codec APIs. They are currently only available on Windows and
// use the Win32 MBCS converters to implement the conversions.  Note that MBCS (or
// DBCS) is a class of encodings, not just one.  The target encoding is defined by
// the user settings on the machine running the codec.
//
//go:linkname UnicodeTranslate C.PyUnicode_Translate
func UnicodeTranslate(unicode *Object, table *Object, errors *Char) *Object

// PyObject* PyUnicode_DecodeMBCS(const char *str, Py_ssize_t size, const char *errors)
// Create a Unicode object by decoding *size* bytes of the MBCS encoded string *str*.
// Return “NULL“ if an exception was raised by the codec.
//
//go:linkname UnicodeDecodeMBCS C.PyUnicode_DecodeMBCS
func UnicodeDecodeMBCS(str *Char, size SSizeT, errors *Char) *Object

// PyObject* PyUnicode_DecodeMBCSStateful(const char *str, Py_ssize_t size, \
// const char *errors, Py_ssize_t *consumed)
//
// If *consumed* is “NULL“, behave like :c:func:`PyUnicode_DecodeMBCS`. If
// *consumed* is not “NULL“, :c:func:`PyUnicode_DecodeMBCSStateful` will not decode
// trailing lead byte and the number of bytes that have been decoded will be stored
// in *consumed*.
//
//go:linkname UnicodeDecodeMBCSStateful C.PyUnicode_DecodeMBCSStateful
func UnicodeDecodeMBCSStateful(str *Char, size SSizeT) *Object

// PyObject* PyUnicode_AsMBCSString(PyObject *unicode)
// Encode a Unicode object using MBCS and return the result as Python bytes
// object.  Error handling is "strict".  Return “NULL“ if an exception was
// raised by the codec.
//
//go:linkname UnicodeAsMBCSString C.PyUnicode_AsMBCSString
func UnicodeAsMBCSString(unicode *Object) *Object

// PyObject* PyUnicode_EncodeCodePage(int code_page, PyObject *unicode, const char *errors)
// Encode the Unicode object using the specified code page and return a Python
// bytes object.  Return “NULL“ if an exception was raised by the codec. Use
// :c:macro:`!CP_ACP` code page to get the MBCS encoder.
//
// Methods & Slots
// """""""""""""""
//
// .. _unicodemethodsandslots:
//
// Methods and Slot Functions
// ^^^^^^^^^^^^^^^^^^^^^^^^^^
//
// The following APIs are capable of handling Unicode objects and strings on input
// (we refer to them as strings in the descriptions) and return Unicode objects or
// integers as appropriate.
//
// They all return “NULL“ or “-1“ if an exception occurs.
//
//go:linkname UnicodeEncodeCodePage C.PyUnicode_EncodeCodePage
func UnicodeEncodeCodePage(codePage Int, unicode *Object, errors *Char) *Object

// PyObject* PyUnicode_Concat(PyObject *left, PyObject *right)
// Concat two strings giving a new Unicode string.
//
//go:linkname UnicodeConcat C.PyUnicode_Concat
func UnicodeConcat(left *Object, right *Object) *Object

// PyObject* PyUnicode_Split(PyObject *unicode, PyObject *sep, Py_ssize_t maxsplit)
// Split a string giving a list of Unicode strings.  If *sep* is “NULL“, splitting
// will be done at all whitespace substrings.  Otherwise, splits occur at the given
// separator.  At most *maxsplit* splits will be done.  If negative, no limit is
// set.  Separators are not included in the resulting list.
//
//go:linkname UnicodeSplit C.PyUnicode_Split
func UnicodeSplit(unicode *Object, sep *Object, maxsplit SSizeT) *Object

// PyObject* PyUnicode_Splitlines(PyObject *unicode, int keepends)
// Split a Unicode string at line breaks, returning a list of Unicode strings.
// CRLF is considered to be one line break.  If *keepends* is “0“, the Line break
// characters are not included in the resulting strings.
//
//go:linkname UnicodeSplitlines C.PyUnicode_Splitlines
func UnicodeSplitlines(unicode *Object, keepends Int) *Object

// PyObject* PyUnicode_Join(PyObject *separator, PyObject *seq)
// Join a sequence of strings using the given *separator* and return the resulting
// Unicode string.
//
//go:linkname UnicodeJoin C.PyUnicode_Join
func UnicodeJoin(separator *Object, seq *Object) *Object

// Py_ssize_t PyUnicode_Tailmatch(PyObject *unicode, PyObject *substr, \
// Py_ssize_t start, Py_ssize_t end, int direction)
//
// Return “1“ if *substr* matches “unicode[start:end]“ at the given tail end
// (*direction* == “-1“ means to do a prefix match, *direction* == “1“ a suffix match),
// “0“ otherwise. Return “-1“ if an error occurred.
//
//go:linkname UnicodeTailmatch C.PyUnicode_Tailmatch
func UnicodeTailmatch(unicode *Object, substr *Object) SSizeT

// Py_ssize_t PyUnicode_Find(PyObject *unicode, PyObject *substr, \
// Py_ssize_t start, Py_ssize_t end, int direction)
//
// Return the first position of *substr* in “unicode[start:end]“ using the given
// *direction* (*direction* == “1“ means to do a forward search, *direction* == “-1“ a
// backward search).  The return value is the index of the first match; a value of
// “-1“ indicates that no match was found, and “-2“ indicates that an error
// occurred and an exception has been set.
//
//go:linkname UnicodeFind C.PyUnicode_Find
func UnicodeFind(unicode *Object, substr *Object) SSizeT

// Py_ssize_t PyUnicode_FindChar(PyObject *unicode, Py_UCS4 ch, \
// Py_ssize_t start, Py_ssize_t end, int direction)
//
// Return the first position of the character *ch* in “unicode[start:end]“ using
// the given *direction* (*direction* == “1“ means to do a forward search,
// *direction* == “-1“ a backward search).  The return value is the index of the
// first match; a value of “-1“ indicates that no match was found, and “-2“
// indicates that an error occurred and an exception has been set.
//
// *start* and *end* are now adjusted to behave like “unicode[start:end]“.
//
//go:linkname UnicodeFindChar C.PyUnicode_FindChar
func UnicodeFindChar(unicode *Object, ch UCS4) SSizeT

// Py_ssize_t PyUnicode_Count(PyObject *unicode, PyObject *substr, \
// Py_ssize_t start, Py_ssize_t end)
//
// Return the number of non-overlapping occurrences of *substr* in
// “unicode[start:end]“.  Return “-1“ if an error occurred.
//
//go:linkname UnicodeCount C.PyUnicode_Count
func UnicodeCount(unicode *Object, substr *Object) SSizeT

// PyObject* PyUnicode_Replace(PyObject *unicode, PyObject *substr, \
// PyObject *replstr, Py_ssize_t maxcount)
//
// Replace at most *maxcount* occurrences of *substr* in *unicode* with *replstr* and
// return the resulting Unicode object. *maxcount* == “-1“ means replace all
// occurrences.
//
//go:linkname UnicodeReplace C.PyUnicode_Replace
func UnicodeReplace(unicode *Object, substr *Object) *Object

// int PyUnicode_Compare(PyObject *left, PyObject *right)
// Compare two strings and return “-1“, “0“, “1“ for less than, equal, and greater than,
// respectively.
//
// This function returns “-1“ upon failure, so one should call
// :c:func:`PyErr_Occurred` to check for errors.
//
// .. seealso::
//
// The :c:func:`PyUnicode_Equal` function.
//
//go:linkname UnicodeCompare C.PyUnicode_Compare
func UnicodeCompare(left *Object, right *Object) Int

// int PyUnicode_CompareWithASCIIString(PyObject *unicode, const char *string)
// Compare a Unicode object, *unicode*, with *string* and return “-1“, “0“, “1“ for less
// than, equal, and greater than, respectively. It is best to pass only
// ASCII-encoded strings, but the function interprets the input string as
// ISO-8859-1 if it contains non-ASCII characters.
//
// This function does not raise exceptions.
//
//go:linkname UnicodeCompareWithASCIIString C.PyUnicode_CompareWithASCIIString
func UnicodeCompareWithASCIIString(unicode *Object, string_ *Char) Int

// PyObject* PyUnicode_RichCompare(PyObject *left,  PyObject *right, int op)
// Rich compare two Unicode strings and return one of the following:
//
// * “NULL“ in case an exception was raised
// * :c:data:`Py_True` or :c:data:`Py_False` for successful comparisons
// * :c:data:`Py_NotImplemented` in case the type combination is unknown
//
// Possible values for *op* are :c:macro:`Py_GT`, :c:macro:`Py_GE`, :c:macro:`Py_EQ`,
// :c:macro:`Py_NE`, :c:macro:`Py_LT`, and :c:macro:`Py_LE`.
//
//go:linkname UnicodeRichCompare C.PyUnicode_RichCompare
func UnicodeRichCompare(left *Object, right *Object, op Int) *Object

// PyObject* PyUnicode_Format(PyObject *format, PyObject *args)
// Return a new string object from *format* and *args*; this is analogous to
// “format % args“.
//
//go:linkname UnicodeFormat C.PyUnicode_Format
func UnicodeFormat(format *Object, args *Object) *Object

// int PyUnicode_Contains(PyObject *unicode, PyObject *substr)
// Check whether *substr* is contained in *unicode* and return true or false
// accordingly.
//
// *substr* has to coerce to a one element Unicode string. “-1“ is returned
// if there was an error.
//
//go:linkname UnicodeContains C.PyUnicode_Contains
func UnicodeContains(unicode *Object, substr *Object) Int

// void PyUnicode_InternInPlace(PyObject **p_unicode)
// Intern the argument :c:expr:`*p_unicode` in place.  The argument must be the address of a
// pointer variable pointing to a Python Unicode string object.  If there is an
// existing interned string that is the same as :c:expr:`*p_unicode`, it sets :c:expr:`*p_unicode` to
// it (releasing the reference to the old string object and creating a new
// :term:`strong reference` to the interned string object), otherwise it leaves
// :c:expr:`*p_unicode` alone and interns it.
//
// (Clarification: even though there is a lot of talk about references, think
// of this function as reference-neutral. You must own the object you pass in;
// after the call you no longer own the passed-in reference, but you newly own
// the result.)
//
// This function never raises an exception.
// On error, it leaves its argument unchanged without interning it.
//
// Instances of subclasses of :py:class:`str` may not be interned, that is,
// :c:expr:`PyUnicode_CheckExact(*p_unicode)` must be true. If it is not,
// then -- as with any other error -- the argument is left unchanged.
//
// Note that interned strings are not “immortal”.
// You must keep a reference to the result to benefit from interning.
//
//go:linkname UnicodeInternInPlace C.PyUnicode_InternInPlace
func UnicodeInternInPlace(pUnicode **Object)

// PyObject* PyUnicode_InternFromString(const char *str)
// A combination of :c:func:`PyUnicode_FromString` and
// :c:func:`PyUnicode_InternInPlace`, meant for statically allocated strings.
//
// Return a new ("owned") reference to either a new Unicode string object
// that has been interned, or an earlier interned string object with the
// same value.
//
// Python may keep a reference to the result, or make it :term:`immortal`,
// preventing it from being garbage-collected promptly.
// For interning an unbounded number of different strings, such as ones coming
// from user input, prefer calling :c:func:`PyUnicode_FromString` and
// :c:func:`PyUnicode_InternInPlace` directly.
//
// .. impl-detail::
//
// Strings interned this way are made :term:`immortal`.
//
// PyUnicodeWriter
// ^^^^^^^^^^^^^^^
//
// The :c:type:`PyUnicodeWriter` API can be used to create a Python :class:`str`
// object.
//
//go:linkname UnicodeInternFromString C.PyUnicode_InternFromString
func UnicodeInternFromString(str *Char) *Object

// PyUnicodeWriter* PyUnicodeWriter_Create(Py_ssize_t length)
// Create a Unicode writer instance.
//
// Set an exception and return “NULL“ on error.
//
//go:linkname UnicodeWriterCreate C.PyUnicodeWriter_Create
func UnicodeWriterCreate(length SSizeT) *UnicodeWriter

// PyObject* PyUnicodeWriter_Finish(PyUnicodeWriter *writer)
// Return the final Python :class:`str` object and destroy the writer instance.
//
// Set an exception and return “NULL“ on error.
//
//go:linkname UnicodeWriterFinish C.PyUnicodeWriter_Finish
func UnicodeWriterFinish(writer *UnicodeWriter) *Object

// void PyUnicodeWriter_Discard(PyUnicodeWriter *writer)
// Discard the internal Unicode buffer and destroy the writer instance.
//
// If *writer* is “NULL“, no operation is performed.
//
//go:linkname UnicodeWriterDiscard C.PyUnicodeWriter_Discard
func UnicodeWriterDiscard(writer *UnicodeWriter)

// int PyUnicodeWriter_WriteChar(PyUnicodeWriter *writer, Py_UCS4 ch)
// Write the single Unicode character *ch* into *writer*.
//
// On success, return “0“.
// On error, set an exception, leave the writer unchanged, and return “-1“.
//
//go:linkname UnicodeWriterWriteChar C.PyUnicodeWriter_WriteChar
func UnicodeWriterWriteChar(writer *UnicodeWriter, ch UCS4) Int

// int PyUnicodeWriter_WriteUTF8(PyUnicodeWriter *writer, const char *str, Py_ssize_t size)
// Decode the string *str* from UTF-8 in strict mode and write the output into *writer*.
//
// *size* is the string length in bytes. If *size* is equal to “-1“, call
// “strlen(str)“ to get the string length.
//
// On success, return “0“.
// On error, set an exception, leave the writer unchanged, and return “-1“.
//
// See also :c:func:`PyUnicodeWriter_DecodeUTF8Stateful`.
//
//go:linkname UnicodeWriterWriteUTF8 C.PyUnicodeWriter_WriteUTF8
func UnicodeWriterWriteUTF8(writer *UnicodeWriter, str *Char, size SSizeT) Int

// int PyUnicodeWriter_WriteWideChar(PyUnicodeWriter *writer, const wchar_t *str, Py_ssize_t size)
// Writer the wide string *str* into *writer*.
//
// *size* is a number of wide characters. If *size* is equal to “-1“, call
// “wcslen(str)“ to get the string length.
//
// On success, return “0“.
// On error, set an exception, leave the writer unchanged, and return “-1“.
//
//go:linkname UnicodeWriterWriteWideChar C.PyUnicodeWriter_WriteWideChar
func UnicodeWriterWriteWideChar(writer *UnicodeWriter, str *Wchar, size SSizeT) Int

// int PyUnicodeWriter_WriteUCS4(PyUnicodeWriter *writer, Py_UCS4 *str, Py_ssize_t size)
// Writer the UCS4 string *str* into *writer*.
//
// *size* is a number of UCS4 characters.
//
// On success, return “0“.
// On error, set an exception, leave the writer unchanged, and return “-1“.
//
//go:linkname UnicodeWriterWriteUCS4 C.PyUnicodeWriter_WriteUCS4
func UnicodeWriterWriteUCS4(writer *UnicodeWriter, str *UCS4, size SSizeT) Int

// int PyUnicodeWriter_WriteStr(PyUnicodeWriter *writer, PyObject *obj)
// Call :c:func:`PyObject_Str` on *obj* and write the output into *writer*.
//
// On success, return “0“.
// On error, set an exception, leave the writer unchanged, and return “-1“.
//
//go:linkname UnicodeWriterWriteStr C.PyUnicodeWriter_WriteStr
func UnicodeWriterWriteStr(writer *UnicodeWriter, obj *Object) Int

// int PyUnicodeWriter_WriteRepr(PyUnicodeWriter *writer, PyObject *obj)
// Call :c:func:`PyObject_Repr` on *obj* and write the output into *writer*.
//
// On success, return “0“.
// On error, set an exception, leave the writer unchanged, and return “-1“.
//
//go:linkname UnicodeWriterWriteRepr C.PyUnicodeWriter_WriteRepr
func UnicodeWriterWriteRepr(writer *UnicodeWriter, obj *Object) Int

// int PyUnicodeWriter_WriteSubstring(PyUnicodeWriter *writer, PyObject *str, Py_ssize_t start, Py_ssize_t end)
// Write the substring “str[start:end]“ into *writer*.
//
// *str* must be Python :class:`str` object. *start* must be greater than or
// equal to 0, and less than or equal to *end*. *end* must be less than or
// equal to *str* length.
//
// On success, return “0“.
// On error, set an exception, leave the writer unchanged, and return “-1“.
//
//go:linkname UnicodeWriterWriteSubstring C.PyUnicodeWriter_WriteSubstring
func UnicodeWriterWriteSubstring(writer *UnicodeWriter, str *Object, start SSizeT, end SSizeT) Int

// int PyUnicodeWriter_Format(PyUnicodeWriter *writer, const char *format, ...)
// Similar to :c:func:`PyUnicode_FromFormat`, but write the output directly into *writer*.
//
// On success, return “0“.
// On error, set an exception, leave the writer unchanged, and return “-1“.
//
//go:linkname UnicodeWriterFormat C.PyUnicodeWriter_Format
func UnicodeWriterFormat(writer *UnicodeWriter, format *Char, __llgo_va_list ...any) Int

// int PyUnicodeWriter_DecodeUTF8Stateful(PyUnicodeWriter *writer, const char *string, Py_ssize_t length, const char *errors, Py_ssize_t *consumed)
// Decode the string *str* from UTF-8 with *errors* error handler and write the
// output into *writer*.
//
// *size* is the string length in bytes. If *size* is equal to “-1“, call
// “strlen(str)“ to get the string length.
//
// *errors* is an error handler name, such as “"replace"“. If *errors* is
// “NULL“, use the strict error handler.
//
// If *consumed* is not “NULL“, set *\*consumed* to the number of decoded
// bytes on success.
// If *consumed* is “NULL“, treat trailing incomplete UTF-8 byte sequences
// as an error.
//
// On success, return “0“.
// On error, set an exception, leave the writer unchanged, and return “-1“.
//
// See also :c:func:`PyUnicodeWriter_WriteUTF8`.
//
//go:linkname UnicodeWriterDecodeUTF8Stateful C.PyUnicodeWriter_DecodeUTF8Stateful
func UnicodeWriterDecodeUTF8Stateful(writer *UnicodeWriter, string_ *Char, length SSizeT, errors *Char, consumed *SSizeT) Int

// Py_UCS4
// Py_UCS2
// Py_UCS1
//
// These types are typedefs for unsigned integer types wide enough to contain
// characters of 32 bits, 16 bits and 8 bits, respectively.  When dealing with
// single Unicode characters, use :c:type:`Py_UCS4`.
type UCS4 = C.Py_UCS4

// Py_UNICODE
// This is a typedef of :c:type:`wchar_t`, which is a 16-bit type or 32-bit type
// depending on the platform.
//
// In previous versions, this was a 16-bit type or a 32-bit type depending on
// whether you selected a "narrow" or "wide" Unicode version of Python at
// build time.
//
// .. deprecated-removed:: 3.13 3.15
type UNICODE = C.Py_UNICODE

// PyASCIIObject
// PyCompactUnicodeObject
// PyUnicodeObject
//
// These subtypes of :c:type:`PyObject` represent a Python Unicode object.  In
// almost all cases, they shouldn't be used directly, since all API functions
// that deal with Unicode objects take and return :c:type:`PyObject` pointers.
type ASCIIObject = C.PyASCIIObject

// PyUnicodeWriter
// A Unicode writer instance.
//
// The instance must be destroyed by :c:func:`PyUnicodeWriter_Finish` on
// success, or :c:func:`PyUnicodeWriter_Discard` on error.
type UnicodeWriter struct{}

// PyTypeObject PyUnicode_Type
// This instance of :c:type:`PyTypeObject` represents the Python Unicode type.  It
// is exposed to Python code as “str“.
//
// The following APIs are C macros and static inlined functions for fast checks and
// access to internal read-only data of Unicode objects:
func UnicodeType() TypeObject {
	return *(*TypeObject)(Pointer(&C.PyUnicode_Type))
}