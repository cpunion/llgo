package runtime

// Patch the public constant before type checking and SSA construction. The
// runtime replacement alone cannot change constants folded in its callers.
const Compiler = "llgo"
