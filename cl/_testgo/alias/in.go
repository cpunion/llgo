package main

type Point struct {
	x float64
	y float64
}

type MyPoint = Point

// Pointer receiver dereferences use explicit-status fault lowering, so the
// method is a coroutine. The alias must still resolve to the one Point layout.
func (p *MyPoint) Move(dx, dy float64) {
	p.x += dx
	p.y += dy
}

func (p *Point) Scale(factor float64) {
	p.x *= factor
	p.y *= factor
}

func main() {
	pt := &MyPoint{1, 2}
	pt.Scale(2)
	pt.Move(3, 4)
	println(pt.x, pt.y)
}
