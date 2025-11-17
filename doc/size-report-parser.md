# Size Report Parser

This document captures the current approach for `llgo build -size`, the
motivation for the latest parser rewrite, and the practical steps for
verifying the feature locally.

## Background

- The `-size` flag produces a TinyGo-like breakdown of code/data/BSS usage per
  Go package. Text output is the default format; JSON can be requested with
  `-size:format=json` or `-size:format json`.
- Earlier iterations tried to consume `llvm-readelf`/`llvm-readobj` JSON, but
  Mach-O builds frequently emitted inconsistent payloads, causing
  `llgo build -size` to fail with “no allocatable sections found”.
- We now rely on the plain text output of `llvm-readelf --all`, which is
  available across all platforms and file formats that LLVM supports.

## Parsing Strategy

1. Invoke `llvm-readelf --all <binary>` and stream stdout into
   `parseReadelfOutput`.
2. Interpret the structured text with an indentation-sensitive state machine.
   The parser tracks the following contexts on a stack:
   - `Sections` list → `Section` entries
   - `Symbols` list → `Symbol` entries
3. Each context transition requires the expected leading-space indentation
   (two spaces deeper than the parent). A closing brace/bracket only affects
   the parser if its indentation matches the context on the stack.
4. For sections we capture `Index`, `Name`, `Segment`, `Address`, and `Size`.
   For symbols we record `Name`, the referenced section, and the symbol
   address. Names are normalized (trim `_`, split on `.`/`@`) to recover the Go
   package path used for aggregation.
5. `buildSizeReport` walks each classified section, sorts its symbols by
   address, and accumulates the byte ranges per package/module. Sections with
   no symbols fall back to `(unknown <section>)`, matching the table layout
   shown by `llgo build -size`.

This state machine avoids brittle substring searches and guarantees that we
exit a context only when the indentation matches exactly, which is critical on
Mach-O dumps where nested structures such as `Attributes [` / `Flags [` are
also present.

## Known Constraints & Next Steps

- The parser currently groups symbols solely by address order. A follow-up
  step is to refine `moduleNameFromSymbol` so that generic instantiations like
  `slices.partitionCmpFunc[io/fs` collapse into their originating package.
- We only use text output today; JSON parsing remains disabled because LLVM’s
  JSON schema is still in flux for Mach-O.
- If future targets emit deeper indentation levels that matter to us
  (additional collections of sections/symbols), extend the state stack with a
  new context rather than reusing the existing ones.

## Validation Checklist

- Sample parser test:
  ```sh
  go test ./internal/build -run TestParseReadelfOutput -count=1
  ```
- Real binary parser test (requires a compiled artifact):
  ```sh
  cd cl/_testgo/rewrite
  ../../../llgo.sh build .
  cd -
  LLGO_SIZE_REPORT_BIN=$(pwd)/cl/_testgo/rewrite/rewrite \
    go test ./internal/build -run TestParseReadelfRealBinary -count=1
  ```
- End-to-end smoke test:
  ```sh
  cd cl/_testgo/rewrite
  ../../../llgo.sh build -size .
  ```

Following these steps should produce the grouped package table (text by
default, JSON via `-size:format=json`) without the “no allocatable sections”
error on macOS or Linux builds.
