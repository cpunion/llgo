# Native fault and boundary debugging

This fixture builds one final native LLGo artifact and drives it through stock
LLDB. It verifies that explicit panic, integer division by zero, and invalid
memory retain their exact Go source locations at the shared panic path; that a
real host trap stops on both its C source and Go caller; and that a callback
stack preserves ordered Go-to-C-to-Go boundary frames. A separate `-O2`
artifact must expose nested LLVM inline frames and step from the inline leaf
back to the non-inline caller's next source line.

Run it after installing LLGo and LLDB 18 or newer:

```sh
bash cmd/llgo/debugtest/native/runtest.sh
```
