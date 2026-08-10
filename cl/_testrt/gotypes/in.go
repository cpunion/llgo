package main

func foo(bar) {
}

type base interface {
	f(m map[string]func())
}

type bar interface {
	base
	g(c chan func())
}

func main() {
	foo(nil)
}
