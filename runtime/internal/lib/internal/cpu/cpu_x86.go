//go:build 386 || amd64

package cpu

/*
#if defined(__GNUC__) || defined(__clang__)
    static void getcpuid(unsigned int eax, unsigned int ecx,
                        unsigned int *a, unsigned int *b,
                        unsigned int *c, unsigned int *d) {
    #if defined(__i386__) || defined(__x86_64__)
        __asm__ __volatile__(
            "cpuid"
            : "=a"(*a), "=b"(*b), "=c"(*c), "=d"(*d)
            : "a"(eax), "c"(ecx)
        );
    #endif
    }
#else
    #error This code requires GCC or Clang
#endif
*/
import "C"

func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32) {
	C.getcpuid(
		C.uint(eaxArg),
		C.uint(ecxArg),
		(*C.uint)(&eax),
		(*C.uint)(&ebx),
		(*C.uint)(&ecx),
		(*C.uint)(&edx),
	)
	return
}
