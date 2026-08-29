#include <stdio.h>
#define interface struct

interface ICalc {
	virtual double calc(double v) = 0;
};

interface IVal {
	virtual int val() = 0;
};

class Callback : public ICalc, public IVal {
};

extern "C" void f(Callback* cb) {
	printf("val: %d\ncalc(2): %lf\n", cb->val(), cb->calc(2));
	fflush(stdout);
}

#if defined(_WIN32) && defined(_M_IX86)
extern "C" double llgo_cppmintf_calc_cdecl(void* cb, double value);
extern "C" int llgo_cppmintf_val_cdecl(void* cb);

static double __thiscall llgo_cppmintf_calc_thiscall(ICalc* cb, double value) {
	return llgo_cppmintf_calc_cdecl(cb, value);
}

static int __thiscall llgo_cppmintf_val_thiscall(IVal* cb) {
	return llgo_cppmintf_val_cdecl(cb);
}

extern "C" void* llgo_cppmintf_calc_thunk() {
	return reinterpret_cast<void*>(&llgo_cppmintf_calc_thiscall);
}

extern "C" void* llgo_cppmintf_val_thunk() {
	return reinterpret_cast<void*>(&llgo_cppmintf_val_thiscall);
}
#endif
