package main

var (
	recovered any
	sink      int
)

type receiver struct{}

func (receiver) Plain() {
	recovered = recover()
}

// The loop makes this method a preemptible stackless coroutine. Recover must
// still see the panic owned by the deferred method-value wrapper after the
// method suspends and resumes.
func (receiver) Yielding() {
	for i := 0; i < 32; i++ {
		sink += i
	}
	recovered = recover()
}

type plainCatcher interface {
	Plain()
}

type yieldingCatcher interface {
	Yielding()
}

func runConcretePlain(want string) {
	method := (receiver{}).Plain
	defer method()
	panic(want)
}

func runInterfacePlain(want string) {
	var value plainCatcher = receiver{}
	method := value.Plain
	defer method()
	panic(want)
}

func runConcreteYielding(want string) {
	method := (receiver{}).Yielding
	defer method()
	panic(want)
}

func runInterfaceYielding(want string) {
	var value yieldingCatcher = receiver{}
	method := value.Yielding
	defer method()
	panic(want)
}

func requireRecovered(want string) {
	if recovered != want {
		panic("recover through method wrapper")
	}
	recovered = nil
}

func requireNonDeferredRecoverIsNil() {
	recovered = "dirty"
	method := (receiver{}).Plain
	method()
	if recovered != nil {
		panic("recover outside deferred call")
	}

	recovered = "dirty"
	var value plainCatcher = receiver{}
	interfaceMethod := value.Plain
	interfaceMethod()
	if recovered != nil {
		panic("recover outside deferred interface call")
	}
}

func main() {
	runConcretePlain("concrete plain")
	requireRecovered("concrete plain")

	runInterfacePlain("interface plain")
	requireRecovered("interface plain")

	runConcreteYielding("concrete yielding")
	requireRecovered("concrete yielding")

	runInterfaceYielding("interface yielding")
	requireRecovered("interface yielding")

	requireNonDeferredRecoverIsNil()
	println("ok")
}
