package main

type resetter interface {
	Reset()
}

type item struct {
	value int
}

func (p *item) Reset() {
	println("reset", p.value)
}

func run(v resetter) {
	defer v.Reset()
	println("body")
}

func main() {
	run(&item{42})
}
