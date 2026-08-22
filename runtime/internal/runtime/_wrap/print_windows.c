/* Runtime-owned stderr output must use Go's byte semantics. The Universal C
 * Runtime opens stderr in text mode, where fwrite/fputc turn LF into CRLF.
 * Bypass that translation without changing the FILE mode observed by C code. */
#include <stdint.h>
#include <stdarg.h>
#define _NO_CRT_STDIO_INLINE
#include <stdio.h>

typedef __SIZE_TYPE__ llgo_size_t;
typedef unsigned long llgo_dword;

#if defined(_WIN64)
#define LLGO_WINAPI
#else
#define LLGO_WINAPI __attribute__((stdcall))
#endif

__declspec(dllimport) void *LLGO_WINAPI GetStdHandle(llgo_dword handle);
__declspec(dllimport) int LLGO_WINAPI WriteFile(
    void *file, const void *buffer, llgo_dword size, llgo_dword *written,
    void *overlapped);

#define LLGO_STD_ERROR_HANDLE ((llgo_dword)-12)

void llgo_print_write(const void *data, llgo_size_t size)
{
    void *file = GetStdHandle(LLGO_STD_ERROR_HANDLE);
    const unsigned char *p = (const unsigned char *)data;

    if (file == 0 || file == (void *)(intptr_t)-1)
        return;
    while (size != 0) {
        llgo_dword chunk = size > UINT32_MAX ? UINT32_MAX : (llgo_dword)size;
        llgo_dword written = 0;
        if (!WriteFile(file, p, chunk, &written, 0) || written == 0)
            return;
        p += written;
        size -= written;
    }
}

void llgo_print_byte(unsigned char value)
{
    llgo_print_write(&value, 1);
}

/* MSVC's Universal CRT implements the formatted stdio entry points as inline
 * header wrappers around __stdio_common_vfprintf. Code generated from a plain
 * external C declaration (including //go:linkname C.printf) instead requests
 * the historical printf/fprintf symbols, which the UCRT import libraries do
 * not provide. Keep those source-level C declarations usable by providing the
 * two ABI-compatible forwarding entry points in the Windows runtime. */
int printf(const char *format, ...)
{
    va_list args;
    va_start(args, format);
    int result = __stdio_common_vfprintf(
        _CRT_INTERNAL_LOCAL_PRINTF_OPTIONS, __acrt_iob_func(1), format, 0,
        args);
    va_end(args);
    return result;
}

int fprintf(FILE *stream, const char *format, ...)
{
    va_list args;
    va_start(args, format);
    int result = __stdio_common_vfprintf(
        _CRT_INTERNAL_LOCAL_PRINTF_OPTIONS, stream, format, 0, args);
    va_end(args);
    return result;
}
