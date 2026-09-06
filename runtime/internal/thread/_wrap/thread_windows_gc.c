#define LLGO_USE_BDWGC
#if defined(__MINGW32__)
#define LLGO_USE_BDWGC_BEGINTHREADEX
#endif
#include "thread_windows.c"
