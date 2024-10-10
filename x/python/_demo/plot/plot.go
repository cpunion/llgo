package main

import (
	"github.com/goplus/llgo/py"
	"github.com/goplus/llgo/x/python"
)

type plt struct {
	python.Module
}

func Plt() plt {
	return plt{python.ImportModule("matplotlib.pyplot")}
}

func (m plt) Plot(args ...any) {
	m.Call("plot", args...)
}

func (m plt) Show() {
	m.Call("show")
}

func plot1() {
	plt := python.ImportModule("matplotlib.pyplot")
	plt.Call("plot", python.MakeTuple(5, 10), python.MakeTuple(10, 15),
		python.DictFromPairs("color", "red"))
	plt.Call("show")
}

func plot2() {
	plt := Plt()
	plt.Plot(python.MakeTuple(5, 10), python.MakeTuple(10, 15), python.DictFromPairs("color", "red"))
	plt.Show()
}

func main() {
	py.Initialize()
	plot1()
	plot2()
}
