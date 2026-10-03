# Size-build correctness regressions

Run from the repository root:

```sh
python3 test/sizebuild/runtest.py --target=wasi
python3 test/sizebuild/runtest.py --target=esp32c3
```

The runner builds its own development LLGo so the full-LTO Go metadata and
`-deadcodedrop` paths are exercised. The WASI lane requires WAMR (`iwasm`, or
`IWASM=/path/to/iwasm`) with threads and legacy exception handling. It builds and
runs println with full/ThinLTO and fmt.Printf with full LTO, first forcing
compilation and then permitting package-cache reuse. A full-LTO build of the
existing threaded-GC fixture also checks goroutines, panic/recover, C blocking,
and arena growth.

The ESP32-C3 lane checks C-only DCE firmware compilation before and after cache
reuse. It does not claim hardware or emulator execution. Both lanes use LLGo's
normal target-toolchain setup. These are correctness checks, not size budgets.
