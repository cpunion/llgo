typedef __SIZE_TYPE__ llgo_size_t;
typedef __UINTPTR_TYPE__ llgo_uintptr;
typedef unsigned long llgo_dword;
typedef int llgo_bool;

#if defined(_WIN64)
#define LLGO_WINAPI
#else
#define LLGO_WINAPI __attribute__((stdcall))
#endif

typedef llgo_dword(LLGO_WINAPI *llgo_thread_start)(void *parameter);
typedef llgo_uintptr (*llgo_callback)(llgo_uintptr argument);

__declspec(dllimport) void *LLGO_WINAPI
CreateThread(void *attributes, llgo_size_t stack_size,
             llgo_thread_start start, void *parameter,
             llgo_dword flags, llgo_dword *thread_id);
__declspec(dllimport) llgo_dword LLGO_WINAPI
WaitForSingleObject(void *handle, llgo_dword milliseconds);
__declspec(dllimport) llgo_bool LLGO_WINAPI CloseHandle(void *handle);
__declspec(dllimport) llgo_dword LLGO_WINAPI GetLastError(void);

typedef struct {
    llgo_callback callback;
    llgo_uintptr argument;
    llgo_uintptr result;
} llgo_callback_context;

static llgo_dword LLGO_WINAPI llgo_foreign_thread_start(void *parameter)
{
    llgo_callback_context *context = (llgo_callback_context *)parameter;
    context->result = context->callback(context->argument);
    return 0;
}

int llgo_windows_call_foreign_thread(llgo_callback callback,
                                     llgo_uintptr argument,
                                     llgo_uintptr *result)
{
    const llgo_dword infinite = 0xffffffffUL;
    llgo_callback_context context = {callback, argument, 0};
    void *thread = CreateThread(0, 0, llgo_foreign_thread_start,
                                &context, 0, 0);
    llgo_dword error;
    if (thread == 0)
        return (int)GetLastError();
    if (WaitForSingleObject(thread, infinite) != 0) {
        error = GetLastError();
        CloseHandle(thread);
        return (int)error;
    }
    CloseHandle(thread);
    *result = context.result;
    return 0;
}
