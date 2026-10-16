//go:build !llgo

package cl

import (
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestCoroFrameZeroingDoesNotCopyNativeStackAggregate(t *testing.T) {
	for _, target := range []struct {
		name  string
		value *llssa.Target
	}{{"native", nil}, {"wasm32", &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			prog, pkg := compileCoroLeafPhysicalABISource(t, target.value, `package foo
func Leaf() byte {
	var value [4096]byte
	for i := range value { value[i] = byte(i) }
	var sum byte
	for _, item := range value { sum += item }
	return sum
}
`)
			defer prog.Dispose()
			defer pkg.Module().Dispose()
			ramp := pkg.Module().NamedFunction("foo.Leaf$coro")
			pre := ramp.String()
			begin, zero := strings.Index(pre, "call ptr @llvm.coro.begin"), strings.Index(pre, "call void @llvm.memset")
			publish := strings.Index(pre, "call void @"+coroFramePublishHookV3)
			if begin < 0 || zero <= begin || publish <= zero {
				t.Fatalf("frame zeroing must follow coro.begin and precede publication: begin=%d zero=%d publish=%d", begin, zero, publish)
			}
			runCoroABITestPipeline(t, prog, pkg.Module())
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			post := pkg.Module().NamedFunction("foo.Leaf$coro").String()
			if strings.Contains(post, "load [4096 x i8]") || strings.Contains(post, "store [4096 x i8]") {
				t.Fatalf("split ramp copies a zeroed native-stack aggregate into the frame:\n%s", post)
			}
			options := llvm.NewPassBuilderOptions()
			defer options.Dispose()
			if err := pkg.Module().RunPasses("default<Os>", prog.TargetMachine(), options); err != nil {
				t.Fatal(err)
			}
			post = pkg.Module().NamedFunction("foo.Leaf$coro").String()
			if strings.Contains(post, "alloca [4096 x i8]") || strings.Count(post, "\n") > 100 {
				t.Fatal("optimized ramp retains native aggregate storage or element-wise initialization")
			}
		})
	}
}
