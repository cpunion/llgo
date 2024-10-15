package py

/*
#include <Python.h>
*/
import "C"
import (
	_ "unsafe"
)

// int PyOS_snprintf(char *str, size_t size,  const char *format, ...)
// Output not more than *size* bytes to *str* according to the format string
// *format* and the extra arguments. See the Unix man page :manpage:`snprintf(3)`.
//
//go:linkname OSSnprintf C.PyOS_snprintf
func OSSnprintf(str *Char, size Ulong, format *Char, __llgo_va_list ...any) Int

// unsigned long PyOS_strtoul(const char *str, char **ptr, int base)
// Convert the initial part of the string in “str“ to an :c:expr:`unsigned
// long` value according to the given “base“, which must be between “2“ and
// “36“ inclusive, or be the special value “0“.
//
// Leading white space and case of characters are ignored.  If “base“ is zero
// it looks for a leading “0b“, “0o“ or “0x“ to tell which base.  If
// these are absent it defaults to “10“.  Base must be 0 or between 2 and 36
// (inclusive).  If “ptr“ is non-“NULL“ it will contain a pointer to the
// end of the scan.
//
// If the converted value falls out of range of corresponding return type,
// range error occurs (:c:data:`errno` is set to :c:macro:`!ERANGE`) and
// :c:macro:`!ULONG_MAX` is returned.  If no conversion can be performed, “0“
// is returned.
//
// See also the Unix man page :manpage:`strtoul(3)`.
//
//go:linkname OSStrtoul C.PyOS_strtoul
func OSStrtoul(str *Char, ptr **Char, base Int) Ulong

// long PyOS_strtol(const char *str, char **ptr, int base)
// Convert the initial part of the string in “str“ to an :c:expr:`long` value
// according to the given “base“, which must be between “2“ and “36“
// inclusive, or be the special value “0“.
//
// Same as :c:func:`PyOS_strtoul`, but return a :c:expr:`long` value instead
// and :c:macro:`LONG_MAX` on overflows.
//
// See also the Unix man page :manpage:`strtol(3)`.
//
//go:linkname OSStrtol C.PyOS_strtol
func OSStrtol(str *Char, ptr **Char, base Int) Long

// double PyOS_string_to_double(const char *s, char **endptr, PyObject *overflow_exception)
// Convert a string “s“ to a :c:expr:`double`, raising a Python
// exception on failure.  The set of accepted strings corresponds to
// the set of strings accepted by Python's :func:`float` constructor,
// except that “s“ must not have leading or trailing whitespace.
// The conversion is independent of the current locale.
//
// If “endptr“ is “NULL“, convert the whole string.  Raise
// :exc:`ValueError` and return “-1.0“ if the string is not a valid
// representation of a floating-point number.
//
// If endptr is not “NULL“, convert as much of the string as
// possible and set “*endptr“ to point to the first unconverted
// character.  If no initial segment of the string is the valid
// representation of a floating-point number, set “*endptr“ to point
// to the beginning of the string, raise ValueError, and return
// “-1.0“.
//
// If “s“ represents a value that is too large to store in a float
// (for example, “"1e500"“ is such a string on many platforms) then
// if “overflow_exception“ is “NULL“ return “Py_HUGE_VAL“ (with
// an appropriate sign) and don't set any exception.  Otherwise,
// “overflow_exception“ must point to a Python exception object;
// raise that exception and return “-1.0“.  In both cases, set
// “*endptr“ to point to the first character after the converted value.
//
// If any other error occurs during the conversion (for example an
// out-of-memory error), set the appropriate Python exception and
// return “-1.0“.
//
//go:linkname OSStringToDouble C.PyOS_string_to_double
func OSStringToDouble(s *Char, endptr **Char, overflowException *Object) Double

// char* PyOS_double_to_string(double val, char format_code, int precision, int flags, int *ptype)
// Convert a :c:expr:`double` *val* to a string using supplied
// *format_code*, *precision*, and *flags*.
//
// *format_code* must be one of “'e'“, “'E'“, “'f'“, “'F'“,
// “'g'“, “'G'“ or “'r'“.  For “'r'“, the supplied *precision*
// must be 0 and is ignored.  The “'r'“ format code specifies the
// standard :func:`repr` format.
//
// *flags* can be zero or more of the values “Py_DTSF_SIGN“,
// “Py_DTSF_ADD_DOT_0“, or “Py_DTSF_ALT“, or-ed together:
//
// * “Py_DTSF_SIGN“ means to always precede the returned string with a sign
// character, even if *val* is non-negative.
//
// * “Py_DTSF_ADD_DOT_0“ means to ensure that the returned string will not look
// like an integer.
//
// * “Py_DTSF_ALT“ means to apply "alternate" formatting rules.  See the
// documentation for the :c:func:`PyOS_snprintf` “'#'“ specifier for
// details.
//
// If *ptype* is non-“NULL“, then the value it points to will be set to one of
// “Py_DTST_FINITE“, “Py_DTST_INFINITE“, or “Py_DTST_NAN“, signifying that
// *val* is a finite number, an infinite number, or not a number, respectively.
//
// The return value is a pointer to *buffer* with the converted string or
// “NULL“ if the conversion failed. The caller is responsible for freeing the
// returned string by calling :c:func:`PyMem_Free`.
//
//go:linkname OSDoubleToString C.PyOS_double_to_string
func OSDoubleToString(val Double, formatCode Char, precision Int, flags Int, ptype *Int) *Char

// int PyOS_stricmp(const char *s1, const char *s2)
// Case insensitive comparison of strings. The function works almost
// identically to :c:func:`!strcmp` except that it ignores the case.
//
//go:linkname OSStricmp C.PyOS_stricmp
func OSStricmp(s1 *Char, s2 *Char) Int

// int PyOS_strnicmp(const char *s1, const char *s2, Py_ssize_t  size)
// Case insensitive comparison of strings. The function works almost
// identically to :c:func:`!strncmp` except that it ignores the case.
//
//go:linkname OSStrnicmp C.PyOS_strnicmp
func OSStrnicmp(s1 *Char, s2 *Char, size SSizeT) Int