package main

type IteratorG[T any] func(T) bool

type TreeG[T any] struct{}

func (*TreeG[T]) Ascend(iterator IteratorG[T]) {
	var zero T
	iterator(zero)
}

type Tree TreeG[int]

type Iterator IteratorG[int]

func (t *Tree) Ascend(iterator Iterator) {
	(*TreeG[int])(t).Ascend((IteratorG[int])(iterator))
}

func main() {
	var got int
	tree := (*Tree)(new(TreeG[int]))

	tree.Ascend(func(v int) bool {
		got = v + 1
		return false
	})
	if got != 1 {
		panic("bad Ascend result")
	}
	println("ok")
}
