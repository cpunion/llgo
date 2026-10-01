# Explicit external assembly source selection

`llgo build -native-asm-pkgs=example.com/library/package` opts exact external
import paths into native Go assembly source selection. Comma-separated paths
are allowed; patterns, the standard library, and LLGo's compiler/runtime
modules are rejected. The default behavior is unchanged when the option is
absent.

The native profile omits `llgo`, `purego`, and `math_big_pure_go` only while
matching files in those explicitly selected package directories. It uses the
resolved source Go version, target, feature tags, and `gc` frontend context.
Global source-selection flags retain their existing values for the standard
library, runtime, and every other package.

The implementation passes header-only overlays to the same package-driver
load that selects Go declarations, assembly files, and imports. It never
invents assembly signatures or changes function bodies/instructions. The
LLGo parser and assembly translator consume the same overlay bytes, which
also enter the existing source fingerprints.

Go 1.27 rejects overlays for files beneath `GOMODCACHE`. This experimental
option therefore requires source directories outside that cache, such as an
owned module prepared with the standard `go mod vendor` command. It does not
automatically vendor dependencies, alter a module cache, or add replacements.
Ordinary module-cache builds fail rather than silently compiling pure Go.
Vendored source must be checked against the pinned module's original bytes;
the module version printed by `go list` alone does not attest a modified vendor
tree.

Successful selection is not an assembly compatibility pass. Verification
still needs original-source Go `gensymabis`, actual LLGo selected-file and
bodyless-declaration evidence, final-link symbols, native feature/input
preconditions, and successful executed API results. In particular, the local
Darwin/arm64 xxhash experiment selected real assembly and then failed its
oracle. Do not treat that selection or link as runtime success.
