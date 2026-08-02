// LITTEST
package main

import (
	"github.com/goplus/llgo/cl/_testdrop/interface_demand_fixedpoint/api"
	"github.com/goplus/llgo/cl/_testdrop/interface_demand_fixedpoint/flow"
	"github.com/goplus/llgo/cl/_testdrop/interface_demand_fixedpoint/model"
)

// SYMBOL-NOT: testdrop/interface_demand_fixedpoint/model{{.*}}Runner{{.*}}Drop
// SYMBOL-NOT: testdrop/interface_demand_fixedpoint/flow{{.*}}Worker{{.*}}Drop
// SYMBOL-NOT: testdrop/interface_demand_fixedpoint/flow{{.*}}Finisher{{.*}}Drop
// SYMBOL-DAG: testdrop/interface_demand_fixedpoint/model.NewRunner
// SYMBOL-DAG: _llgo_github.com/goplus/llgo/cl/_testdrop/interface_demand_fixedpoint/model.Runner
// SYMBOL-DAG: _llgo_github.com/goplus/llgo/cl/_testdrop/interface_demand_fixedpoint/flow.Worker
// SYMBOL-NOT: testdrop/interface_demand_fixedpoint/model{{.*}}Runner{{.*}}Drop
// SYMBOL-NOT: testdrop/interface_demand_fixedpoint/flow{{.*}}Worker{{.*}}Drop
// SYMBOL-NOT: testdrop/interface_demand_fixedpoint/flow{{.*}}Finisher{{.*}}Drop

var sink any

func main() {
	// Keep a second Worker type descriptor reachable without directly creating
	// a Second.Next demand from main. The actual Worker.Next demand is produced
	// only after Runner.Run is kept and reaches flow.Step.
	sink = flow.Worker{N: 0}
	println(api.UseFirst(model.NewRunner()))
}
