window.BENCHMARK_DATA = {
  "lastUpdate": 1785231129032,
  "repoUrl": "https://github.com/cpunion/llgo",
  "entries": {
    "Linux program binary size": [
      {
        "commit": {
          "author": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "committer": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "id": "68973b962c086541f2c0153a718ba20f10e55822",
          "message": "ci: use benchmark action v1",
          "timestamp": "2026-07-28T09:21:34Z",
          "url": "https://github.com/cpunion/llgo/commit/68973b962c086541f2c0153a718ba20f10e55822"
        },
        "date": 1785231117987,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "binary/cprintf/file",
            "value": 18264,
            "unit": "bytes"
          },
          {
            "name": "binary/cprintf/text",
            "value": 410,
            "unit": "bytes"
          },
          {
            "name": "binary/cprintf/data",
            "value": 12587,
            "unit": "bytes"
          },
          {
            "name": "binary/cprintf/bss",
            "value": 2897,
            "unit": "bytes"
          },
          {
            "name": "binary/println/file",
            "value": 71056,
            "unit": "bytes"
          },
          {
            "name": "binary/println/text",
            "value": 20617,
            "unit": "bytes"
          },
          {
            "name": "binary/println/data",
            "value": 28665,
            "unit": "bytes"
          },
          {
            "name": "binary/println/bss",
            "value": 2181,
            "unit": "bytes"
          },
          {
            "name": "binary/fmtprintf/file",
            "value": 2211088,
            "unit": "bytes"
          },
          {
            "name": "binary/fmtprintf/text",
            "value": 758745,
            "unit": "bytes"
          },
          {
            "name": "binary/fmtprintf/data",
            "value": 980229,
            "unit": "bytes"
          },
          {
            "name": "binary/fmtprintf/bss",
            "value": 320972,
            "unit": "bytes"
          }
        ]
      }
    ],
    "Linux program build and run time": [
      {
        "commit": {
          "author": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "committer": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "id": "68973b962c086541f2c0153a718ba20f10e55822",
          "message": "ci: use benchmark action v1",
          "timestamp": "2026-07-28T09:21:34Z",
          "url": "https://github.com/cpunion/llgo/commit/68973b962c086541f2c0153a718ba20f10e55822"
        },
        "date": 1785231120447,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "compile/cprintf",
            "value": 357570827,
            "range": "343514228..3921926446",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/cprintf",
            "value": 1367639,
            "range": "1267951..1396122",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/println",
            "value": 340232940,
            "range": "337611148..341373594",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/println",
            "value": 1573193,
            "range": "1544610..1618318",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/fmtprintf",
            "value": 3233590577,
            "range": "3167722504..29510171872",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/fmtprintf",
            "value": 2444542,
            "range": "2420198..2577531",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          }
        ]
      }
    ],
    "Linux compiler and core language": [
      {
        "commit": {
          "author": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "committer": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "id": "68973b962c086541f2c0153a718ba20f10e55822",
          "message": "ci: use benchmark action v1",
          "timestamp": "2026-07-28T09:21:34Z",
          "url": "https://github.com/cpunion/llgo/commit/68973b962c086541f2c0153a718ba20f10e55822"
        },
        "date": 1785231122719,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkMergeCompilerFlags (github.com/goplus/llgo/internal/clang)",
            "value": 150,
            "unit": "ns/op",
            "extra": "2045149 times"
          },
          {
            "name": "BenchmarkMergeLinkerFlags (github.com/goplus/llgo/internal/clang)",
            "value": 94.48,
            "unit": "ns/op",
            "extra": "3132208 times"
          },
          {
            "name": "BenchmarkLookupPCRandom (github.com/goplus/llgo/internal/build/funcinfo)",
            "value": 13.35,
            "unit": "ns/op",
            "extra": "21474627 times"
          },
          {
            "name": "BenchmarkGlobalRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.561,
            "unit": "ns/op",
            "extra": "192796418 times\n4 procs"
          },
          {
            "name": "BenchmarkTLSRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.557,
            "unit": "ns/op",
            "extra": "192763317 times\n4 procs"
          },
          {
            "name": "BenchmarkGLSRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.556,
            "unit": "ns/op",
            "extra": "192877114 times\n4 procs"
          },
          {
            "name": "BenchmarkGlobalWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 2.483,
            "unit": "ns/op",
            "extra": "120782053 times\n4 procs"
          },
          {
            "name": "BenchmarkTLSWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 2.488,
            "unit": "ns/op",
            "extra": "120612721 times\n4 procs"
          },
          {
            "name": "BenchmarkGLSWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 2.486,
            "unit": "ns/op",
            "extra": "120718441 times\n4 procs"
          },
          {
            "name": "BenchmarkDirectCall (github.com/goplus/llgo/test/llgoext)",
            "value": 1.559,
            "unit": "ns/op",
            "extra": "192533131 times\n4 procs"
          },
          {
            "name": "BenchmarkInterfaceCall (github.com/goplus/llgo/test/llgoext)",
            "value": 7.788,
            "unit": "ns/op",
            "extra": "38549118 times\n4 procs"
          },
          {
            "name": "BenchmarkDefer (github.com/goplus/llgo/test/llgoext)",
            "value": 52.93,
            "unit": "ns/op",
            "extra": "4817097 times\n4 procs"
          },
          {
            "name": "BenchmarkChannelBuffered (github.com/goplus/llgo/test/llgoext)",
            "value": 35.58,
            "unit": "ns/op",
            "extra": "8454367 times\n4 procs"
          },
          {
            "name": "BenchmarkChannelHandoff (github.com/goplus/llgo/test/llgoext)",
            "value": 31106,
            "unit": "ns/op",
            "extra": "10000 times\n4 procs"
          },
          {
            "name": "BenchmarkRuntimeGetG (github.com/goplus/llgo/test/llgoext)",
            "value": 4.985,
            "unit": "ns/op",
            "extra": "60251428 times\n4 procs"
          },
          {
            "name": "BenchmarkGoroutine (github.com/goplus/llgo/test/llgoext)",
            "value": 44817,
            "unit": "ns/op",
            "extra": "100 times\n4 procs"
          }
        ]
      }
    ],
    "macOS program binary size": [
      {
        "commit": {
          "author": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "committer": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "id": "68973b962c086541f2c0153a718ba20f10e55822",
          "message": "ci: use benchmark action v1",
          "timestamp": "2026-07-28T09:21:34Z",
          "url": "https://github.com/cpunion/llgo/commit/68973b962c086541f2c0153a718ba20f10e55822"
        },
        "date": 1785231125306,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "binary/cprintf/file",
            "value": 84752,
            "unit": "bytes"
          },
          {
            "name": "binary/cprintf/text",
            "value": 15221,
            "unit": "bytes"
          },
          {
            "name": "binary/cprintf/data",
            "value": 192,
            "unit": "bytes"
          },
          {
            "name": "binary/cprintf/bss",
            "value": 17,
            "unit": "bytes"
          },
          {
            "name": "binary/println/file",
            "value": 126768,
            "unit": "bytes"
          },
          {
            "name": "binary/println/text",
            "value": 36252,
            "unit": "bytes"
          },
          {
            "name": "binary/println/data",
            "value": 8833,
            "unit": "bytes"
          },
          {
            "name": "binary/println/bss",
            "value": 244,
            "unit": "bytes"
          },
          {
            "name": "binary/fmtprintf/file",
            "value": 2345856,
            "unit": "bytes"
          },
          {
            "name": "binary/fmtprintf/text",
            "value": 1153837,
            "unit": "bytes"
          },
          {
            "name": "binary/fmtprintf/data",
            "value": 365688,
            "unit": "bytes"
          },
          {
            "name": "binary/fmtprintf/bss",
            "value": 320332,
            "unit": "bytes"
          }
        ]
      }
    ],
    "macOS program build and run time": [
      {
        "commit": {
          "author": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "committer": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "id": "68973b962c086541f2c0153a718ba20f10e55822",
          "message": "ci: use benchmark action v1",
          "timestamp": "2026-07-28T09:21:34Z",
          "url": "https://github.com/cpunion/llgo/commit/68973b962c086541f2c0153a718ba20f10e55822"
        },
        "date": 1785231128394,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "compile/cprintf",
            "value": 463400708,
            "range": "451352000..6058150000",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/cprintf",
            "value": 3323583,
            "range": "3046167..4164834",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/println",
            "value": 482477041,
            "range": "436588875..490450000",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/println",
            "value": 5236541,
            "range": "4461250..5327583",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/fmtprintf",
            "value": 4553050917,
            "range": "3929928292..34966215750",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/fmtprintf",
            "value": 23712125,
            "range": "20697375..35664250",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          }
        ]
      }
    ]
  }
}