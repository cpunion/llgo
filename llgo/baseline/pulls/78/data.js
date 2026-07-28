window.BENCHMARK_DATA = {
  "lastUpdate": 1785210465425,
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
          "id": "89bcac57cfd9b15ede58f96c9f9a62e3cf4c6b2b",
          "message": "ci: exercise benchmark publishing",
          "timestamp": "2026-07-28T03:33:07Z",
          "url": "https://github.com/cpunion/llgo/commit/89bcac57cfd9b15ede58f96c9f9a62e3cf4c6b2b"
        },
        "date": 1785210458885,
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
            "value": 2211096,
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
          "id": "89bcac57cfd9b15ede58f96c9f9a62e3cf4c6b2b",
          "message": "ci: exercise benchmark publishing",
          "timestamp": "2026-07-28T03:33:07Z",
          "url": "https://github.com/cpunion/llgo/commit/89bcac57cfd9b15ede58f96c9f9a62e3cf4c6b2b"
        },
        "date": 1785210460938,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "compile/cprintf",
            "value": 354925570,
            "range": "350821575..10454021865",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/cprintf",
            "value": 1274190,
            "range": "1249291..1296499",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/println",
            "value": 334826673,
            "range": "332761658..348179945",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/println",
            "value": 1575880,
            "range": "1529914..1626254",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/fmtprintf",
            "value": 3302074606,
            "range": "3267214280..29765182618",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/fmtprintf",
            "value": 2465128,
            "range": "2412782..2590842",
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
          "id": "89bcac57cfd9b15ede58f96c9f9a62e3cf4c6b2b",
          "message": "ci: exercise benchmark publishing",
          "timestamp": "2026-07-28T03:33:07Z",
          "url": "https://github.com/cpunion/llgo/commit/89bcac57cfd9b15ede58f96c9f9a62e3cf4c6b2b"
        },
        "date": 1785210462926,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkMergeCompilerFlags (github.com/goplus/llgo/internal/clang)",
            "value": 150.1,
            "unit": "ns/op",
            "extra": "2043735 times"
          },
          {
            "name": "BenchmarkMergeLinkerFlags (github.com/goplus/llgo/internal/clang)",
            "value": 94.49,
            "unit": "ns/op",
            "extra": "3147312 times"
          },
          {
            "name": "BenchmarkLookupPCRandom (github.com/goplus/llgo/internal/build/funcinfo)",
            "value": 13.39,
            "unit": "ns/op",
            "extra": "21792961 times"
          },
          {
            "name": "BenchmarkGlobalRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.557,
            "unit": "ns/op",
            "extra": "192462057 times\n4 procs"
          },
          {
            "name": "BenchmarkTLSRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.556,
            "unit": "ns/op",
            "extra": "192793294 times\n4 procs"
          },
          {
            "name": "BenchmarkGLSRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.579,
            "unit": "ns/op",
            "extra": "192038035 times\n4 procs"
          },
          {
            "name": "BenchmarkGlobalWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 2.483,
            "unit": "ns/op",
            "extra": "120982650 times\n4 procs"
          },
          {
            "name": "BenchmarkTLSWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 2.488,
            "unit": "ns/op",
            "extra": "120610006 times\n4 procs"
          },
          {
            "name": "BenchmarkGLSWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 2.488,
            "unit": "ns/op",
            "extra": "120290409 times\n4 procs"
          },
          {
            "name": "BenchmarkDirectCall (github.com/goplus/llgo/test/llgoext)",
            "value": 1.56,
            "unit": "ns/op",
            "extra": "190475424 times\n4 procs"
          },
          {
            "name": "BenchmarkInterfaceCall (github.com/goplus/llgo/test/llgoext)",
            "value": 7.786,
            "unit": "ns/op",
            "extra": "38528251 times\n4 procs"
          },
          {
            "name": "BenchmarkDefer (github.com/goplus/llgo/test/llgoext)",
            "value": 54.37,
            "unit": "ns/op",
            "extra": "5109777 times\n4 procs"
          },
          {
            "name": "BenchmarkChannelBuffered (github.com/goplus/llgo/test/llgoext)",
            "value": 35.5,
            "unit": "ns/op",
            "extra": "8434838 times\n4 procs"
          },
          {
            "name": "BenchmarkChannelHandoff (github.com/goplus/llgo/test/llgoext)",
            "value": 25548,
            "unit": "ns/op",
            "extra": "10000 times\n4 procs"
          },
          {
            "name": "BenchmarkRuntimeGetG (github.com/goplus/llgo/test/llgoext)",
            "value": 4.982,
            "unit": "ns/op",
            "extra": "60233694 times\n4 procs"
          },
          {
            "name": "BenchmarkGoroutine (github.com/goplus/llgo/test/llgoext)",
            "value": 41751,
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
          "id": "89bcac57cfd9b15ede58f96c9f9a62e3cf4c6b2b",
          "message": "ci: exercise benchmark publishing",
          "timestamp": "2026-07-28T03:33:07Z",
          "url": "https://github.com/cpunion/llgo/commit/89bcac57cfd9b15ede58f96c9f9a62e3cf4c6b2b"
        },
        "date": 1785210464984,
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
    ]
  }
}