/* Keep the runtime shim independent of Windows SDK headers. Clang still
 * applies the target's MSVC ABI and emits ordinary Kernel32 imports. */
#include <stdint.h>

typedef __SIZE_TYPE__ llgo_size_t;
typedef unsigned long llgo_dword;
typedef unsigned short llgo_word;
typedef __UINTPTR_TYPE__ llgo_uintptr;

#if defined(_WIN64)
#define LLGO_WINAPI
#else
#define LLGO_WINAPI __attribute__((stdcall))
#endif

typedef struct {
    void *base_address;
    void *allocation_base;
    llgo_dword allocation_protect;
#if defined(_WIN64)
    llgo_word partition_id;
#endif
    llgo_size_t region_size;
    llgo_dword state;
    llgo_dword protect;
    llgo_dword type;
} llgo_memory_basic_information;

__declspec(dllimport) llgo_dword LLGO_WINAPI
GetActiveProcessorCount(llgo_word group_number);
__declspec(dllimport) llgo_size_t LLGO_WINAPI
VirtualQuery(const void *address, llgo_memory_basic_information *info,
             llgo_size_t length);
__declspec(dllimport) int LLGO_WINAPI
QueryPerformanceCounter(long long *counter);
__declspec(dllimport) int LLGO_WINAPI
QueryPerformanceFrequency(long long *frequency);

typedef struct {
    llgo_dword low;
    llgo_dword high;
} llgo_filetime;

__declspec(dllimport) void LLGO_WINAPI
GetSystemTimePreciseAsFileTime(llgo_filetime *time);
__declspec(dllimport) void *LLGO_WINAPI
LoadLibraryExW(const llgo_word *filename, void *file, llgo_dword flags);
__declspec(dllimport) void *LLGO_WINAPI
GetProcAddress(void *module, const char *name);
__declspec(dllimport) llgo_dword LLGO_WINAPI GetLastError(void);

enum {
    llgo_all_processor_groups = 0xffff,
    llgo_mem_commit = 0x1000,
    llgo_page_noaccess = 0x01,
    llgo_page_execute = 0x10,
    llgo_page_guard = 0x100,
};

int llgo_maxprocs(void)
{
    llgo_dword n = GetActiveProcessorCount(llgo_all_processor_groups);
    return n == 0 ? 1 : (int)n;
}

int llgo_mem_readable(void *p)
{
    llgo_memory_basic_information info;
    llgo_dword protect;
    if (p == 0 || VirtualQuery(p, &info, sizeof(info)) == 0 ||
        info.state != llgo_mem_commit)
        return 0;
    protect = info.protect & 0xff;
    return protect != llgo_page_noaccess && protect != llgo_page_execute &&
           (info.protect & llgo_page_guard) == 0;
}

long long llgo_nanotime(void)
{
    long long counter;
    long long frequency;
    long long seconds;
    long long remainder;
    if (!QueryPerformanceCounter(&counter) ||
        !QueryPerformanceFrequency(&frequency) || frequency <= 0)
        return 0;
    seconds = counter / frequency;
    remainder = counter % frequency;
    return seconds * 1000000000LL +
           remainder * 1000000000LL / frequency;
}

void llgo_walltime(long long *seconds, int *nanoseconds)
{
    llgo_filetime now;
    unsigned long long ticks;
    GetSystemTimePreciseAsFileTime(&now);
    ticks = ((unsigned long long)now.high << 32) | now.low;
    ticks -= 116444736000000000ULL;
    *seconds = (long long)(ticks / 10000000ULL);
    *nanoseconds = (int)((ticks % 10000000ULL) * 100ULL);
}

llgo_uintptr llgo_load_library(const llgo_word *filename, llgo_dword flags,
                               llgo_dword *error)
{
    void *module = LoadLibraryExW(filename, 0, flags);
    *error = module == 0 ? GetLastError() : 0;
    return (llgo_uintptr)module;
}

llgo_uintptr llgo_get_proc_address(llgo_uintptr module,
                                   const unsigned char *name,
                                   llgo_dword *error)
{
    void *proc = GetProcAddress((void *)module, (const char *)name);
    *error = proc == 0 ? GetLastError() : 0;
    return (llgo_uintptr)proc;
}
