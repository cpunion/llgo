package main

func main() {
	// Both nested functions are invoked through function values with closure
	// environments, and the inner closure keeps the original x slot alive.
	// DARWIN-ARM64: [[INNER:%.*]] = call { ptr, ptr } [[OUTER_CODE]](ptr swiftself [[OUTER_CALL_ENV]], i64 1)
	// LINUX-AMD64: [[INNER:%.*]] = call { ptr, ptr } [[OUTER_CODE]](ptr nest [[OUTER_CALL_ENV]], i64 1)
	// DARWIN-ARM64: call void [[INNER_CODE]](ptr swiftself [[INNER_CALL_ENV]], i64 2)
	// LINUX-AMD64: call void [[INNER_CODE]](ptr nest [[INNER_CALL_ENV]], i64 2)
	x := 1
	f := func(i int) func(int) {
		// DARWIN-ARM64-LABEL: define { ptr, ptr } @"main.main$1"(ptr swiftself %0, i64 %1){{.*}} {
		// LINUX-AMD64-LABEL: define { ptr, ptr } @"main.main$1"(ptr nest %0, i64 %1){{.*}} {
		return func(i int) {
			// DARWIN-ARM64-LABEL: define void @"main.main$1$1"(ptr swiftself %0, i64 %1){{.*}} {
			// LINUX-AMD64-LABEL: define void @"main.main$1$1"(ptr nest %0, i64 %1){{.*}} {
			println("closure", i, x)
		}
	}
	f(1)(2)
}
