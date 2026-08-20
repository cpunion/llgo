#if defined(_WIN64)
#define LLGO_WINAPI
#else
#define LLGO_WINAPI __attribute__((stdcall))
#endif

__declspec(dllimport) void LLGO_WINAPI ExitProcess(unsigned long code);

void llgo_core_exit_process(unsigned int code)
{
    ExitProcess(code);
}
