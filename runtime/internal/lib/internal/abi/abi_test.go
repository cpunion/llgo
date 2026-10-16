package abi

import runtimeabi "github.com/xgo-dev/llgo/runtime/abi"

// Keep the replacement package's public Kind identity aligned with the
// runtime ABI package. Packages such as internal/reflectlite expose this type
// through interface methods, so falling back to the original GOROOT Kind
// declaration creates an incompatible dynamic-call signature.
var _ Kind = runtimeabi.Invalid
