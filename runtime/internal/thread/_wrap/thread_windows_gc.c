#define LLGO_USE_BDWGC
#if defined(__MINGW32__)
#define LLGO_WAIT_FOR_BDWGC_REGISTRATION
#endif
#include "thread_windows.c"
