// LITTEST darwin/arm64 linux/amd64
package main

// CHECK-LABEL: define linkonce ptr @"{{.*}}Y[{{.*}}int,int]$coro"(
// DARWIN-ARM64: call ptr @"{{.*}}Y$1[{{.*}}int,int]$coro"(ptr %0, ptr %{{.*}}, ptr swiftself %{{.*}}, [[INT_INTERNAL:%"[^"]+"]] %{{.*}})
// LINUX-AMD64: call ptr @"{{.*}}Y$1[{{.*}}int,int]$coro"(ptr %0, ptr %{{.*}}, ptr nest %{{.*}}, [[INT_INTERNAL:%"[^"]+"]] %{{.*}})
// DARWIN-ARM64: define linkonce ptr @"{{.*}}Y$1[{{.*}}int,int]$coro"(ptr %0, ptr %1, ptr swiftself %2, [[INT_INTERNAL]] %3)
// LINUX-AMD64: define linkonce ptr @"{{.*}}Y$1[{{.*}}int,int]$coro"(ptr %0, ptr %1, ptr nest %2, [[INT_INTERNAL]] %3)
// CHECK-LABEL: define linkonce ptr @"{{.*}}Y[{{.*}}string,string]$coro"(
// DARWIN-ARM64: call ptr @"{{.*}}Y$1[{{.*}}string,string]$coro"(ptr %0, ptr %{{.*}}, ptr swiftself %{{.*}}, [[STRING_INTERNAL:%"[^"]+"]] %{{.*}})
// LINUX-AMD64: call ptr @"{{.*}}Y$1[{{.*}}string,string]$coro"(ptr %0, ptr %{{.*}}, ptr nest %{{.*}}, [[STRING_INTERNAL:%"[^"]+"]] %{{.*}})
// DARWIN-ARM64: define linkonce ptr @"{{.*}}Y$1[{{.*}}string,string]$coro"(ptr %0, ptr %1, ptr swiftself %2, [[STRING_INTERNAL]] %3)
// LINUX-AMD64: define linkonce ptr @"{{.*}}Y$1[{{.*}}string,string]$coro"(ptr %0, ptr %1, ptr nest %2, [[STRING_INTERNAL]] %3)

func Y[Endo ~func(RecFct) RecFct, RecFct ~func(T) R, T, R any](f Endo) RecFct {
	type internal[RecFct ~func(T) R, T, R any] func(internal[RecFct, T, R]) RecFct

	g := func(h internal[RecFct, T, R]) RecFct {
		return func(t T) R {
			return f(h(h))(t)
		}
	}
	return g(g)
}

func main() {
	factorial := Y(func(recur func(int) int) func(int) int {
		return func(n int) int {
			if n == 0 {
				return 1
			}
			return n * recur(n-1)
		}
	})
	repeat := Y(func(recur func(string) string) func(string) string {
		return func(s string) string {
			if len(s) == 3 {
				return s
			}
			return recur(s + "x")
		}
	})
	println(factorial(10))
	println(repeat(""))
}
