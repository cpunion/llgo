package build

// LLGo gets Go package selection and export data from its companion Go
// toolchain. Keep that metadata format separate from runtime.Compiler, which
// identifies the native code generator.
var Default = func() Context {
	ctx := defaultContext()
	ctx.Compiler = "gc"
	return ctx
}()
