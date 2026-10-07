// LITTEST
package main

/*
extern int Callback(void);
static int callCallback(void) {
	return Callback();
}
*/
import "C"

// SYMBOL-NOT: main{{.*}}T{{.*}}Drop
// SYMBOL-DAG: {{ T _?Callback$}}
// SYMBOL-DAG: {{ T _?main\.Callback$}}
// SYMBOL-DAG: main{{.*}}T{{.*}}M
// SYMBOL-NOT: main{{.*}}T{{.*}}Drop

// C-only reachability must preserve both the public Callback entry and its Go
// implementation, together with the interface method reached only from C.
// TestCExportCallbackEntries also checks the public wrapper's thread guard and
// call to main.Callback in each hosted final-link mode.

type I interface {
	M() int
}

type T struct{}

//go:noinline
func (T) M() int { return 7 }

//go:noinline
func (T) Drop() int { panic("Drop should be unreachable") }

//export Callback
func Callback() C.int {
	var v I = T{}
	return C.int(v.M())
}

func main() {
	println(C.callCallback())
}
