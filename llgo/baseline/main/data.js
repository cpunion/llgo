window.BENCHMARK_DATA = {
  "lastUpdate": 1785209271620,
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
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "18e4ba595415435c7e1cba53dab4c47679a8bda7",
          "message": "Merge pull request #76 from cpunion/codex/benchmark-baseline\n\nci: add continuous baseline benchmarks",
          "timestamp": "2026-07-28T03:19:32Z",
          "url": "https://github.com/cpunion/llgo/commit/18e4ba595415435c7e1cba53dab4c47679a8bda7"
        },
        "date": 1785209264548,
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
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "18e4ba595415435c7e1cba53dab4c47679a8bda7",
          "message": "Merge pull request #76 from cpunion/codex/benchmark-baseline\n\nci: add continuous baseline benchmarks",
          "timestamp": "2026-07-28T03:19:32Z",
          "url": "https://github.com/cpunion/llgo/commit/18e4ba595415435c7e1cba53dab4c47679a8bda7"
        },
        "date": 1785209265957,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "compile/cprintf",
            "value": 346341765,
            "range": "344547989..10435285478",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/cprintf",
            "value": 1269944,
            "range": "1221043..1442779",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/println",
            "value": 346721727,
            "range": "337727721..349816878",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/println",
            "value": 1600565,
            "range": "1542839..1656952",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/fmtprintf",
            "value": 3506954078,
            "range": "3430633314..30490724544",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/fmtprintf",
            "value": 2548810,
            "range": "2467273..2635319",
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
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "18e4ba595415435c7e1cba53dab4c47679a8bda7",
          "message": "Merge pull request #76 from cpunion/codex/benchmark-baseline\n\nci: add continuous baseline benchmarks",
          "timestamp": "2026-07-28T03:19:32Z",
          "url": "https://github.com/cpunion/llgo/commit/18e4ba595415435c7e1cba53dab4c47679a8bda7"
        },
        "date": 1785209267318,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkMergeCompilerFlags (github.com/goplus/llgo/internal/clang)",
            "value": 150.7,
            "unit": "ns/op",
            "extra": "2020552 times"
          },
          {
            "name": "BenchmarkMergeLinkerFlags (github.com/goplus/llgo/internal/clang)",
            "value": 95.3,
            "unit": "ns/op",
            "extra": "3146272 times"
          },
          {
            "name": "BenchmarkLookupPCRandom (github.com/goplus/llgo/internal/build/funcinfo)",
            "value": 13.4,
            "unit": "ns/op",
            "extra": "22756502 times"
          },
          {
            "name": "BenchmarkGlobalRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.573,
            "unit": "ns/op",
            "extra": "192411871 times\n4 procs"
          },
          {
            "name": "BenchmarkTLSRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.557,
            "unit": "ns/op",
            "extra": "192762660 times\n4 procs"
          },
          {
            "name": "BenchmarkGLSRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.556,
            "unit": "ns/op",
            "extra": "192755473 times\n4 procs"
          },
          {
            "name": "BenchmarkGlobalWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 2.481,
            "unit": "ns/op",
            "extra": "120946068 times\n4 procs"
          },
          {
            "name": "BenchmarkTLSWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 2.487,
            "unit": "ns/op",
            "extra": "120609754 times\n4 procs"
          },
          {
            "name": "BenchmarkGLSWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 2.486,
            "unit": "ns/op",
            "extra": "120637402 times\n4 procs"
          },
          {
            "name": "BenchmarkDirectCall (github.com/goplus/llgo/test/llgoext)",
            "value": 1.557,
            "unit": "ns/op",
            "extra": "192759010 times\n4 procs"
          },
          {
            "name": "BenchmarkInterfaceCall (github.com/goplus/llgo/test/llgoext)",
            "value": 7.787,
            "unit": "ns/op",
            "extra": "38533759 times\n4 procs"
          },
          {
            "name": "BenchmarkDefer (github.com/goplus/llgo/test/llgoext)",
            "value": 53.06,
            "unit": "ns/op",
            "extra": "4806157 times\n4 procs"
          },
          {
            "name": "BenchmarkChannelBuffered (github.com/goplus/llgo/test/llgoext)",
            "value": 35.58,
            "unit": "ns/op",
            "extra": "8446430 times\n4 procs"
          },
          {
            "name": "BenchmarkChannelHandoff (github.com/goplus/llgo/test/llgoext)",
            "value": 26389,
            "unit": "ns/op",
            "extra": "10000 times\n4 procs"
          },
          {
            "name": "BenchmarkRuntimeGetG (github.com/goplus/llgo/test/llgoext)",
            "value": 4.98,
            "unit": "ns/op",
            "extra": "60042100 times\n4 procs"
          },
          {
            "name": "BenchmarkGoroutine (github.com/goplus/llgo/test/llgoext)",
            "value": 42078,
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
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "18e4ba595415435c7e1cba53dab4c47679a8bda7",
          "message": "Merge pull request #76 from cpunion/codex/benchmark-baseline\n\nci: add continuous baseline benchmarks",
          "timestamp": "2026-07-28T03:19:32Z",
          "url": "https://github.com/cpunion/llgo/commit/18e4ba595415435c7e1cba53dab4c47679a8bda7"
        },
        "date": 1785209268722,
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
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "18e4ba595415435c7e1cba53dab4c47679a8bda7",
          "message": "Merge pull request #76 from cpunion/codex/benchmark-baseline\n\nci: add continuous baseline benchmarks",
          "timestamp": "2026-07-28T03:19:32Z",
          "url": "https://github.com/cpunion/llgo/commit/18e4ba595415435c7e1cba53dab4c47679a8bda7"
        },
        "date": 1785209270077,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "compile/cprintf",
            "value": 489277875,
            "range": "459336208..8034815958",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/cprintf",
            "value": 3428208,
            "range": "3252917..3549125",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/println",
            "value": 475350791,
            "range": "428415959..540793875",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/println",
            "value": 5125708,
            "range": "4312958..6903500",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          },
          {
            "name": "compile/fmtprintf",
            "value": 3318211500,
            "range": "3275313292..29056575709",
            "unit": "ns",
            "extra": "median of 3 consecutive runs"
          },
          {
            "name": "run/fmtprintf",
            "value": 19865625,
            "range": "18946958..21560167",
            "unit": "ns",
            "extra": "median of 7 consecutive runs"
          }
        ]
      }
    ],
    "macOS compiler and core language": [
      {
        "commit": {
          "author": {
            "name": "Li Jie",
            "username": "cpunion",
            "email": "cpunion@gmail.com"
          },
          "committer": {
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "18e4ba595415435c7e1cba53dab4c47679a8bda7",
          "message": "Merge pull request #76 from cpunion/codex/benchmark-baseline\n\nci: add continuous baseline benchmarks",
          "timestamp": "2026-07-28T03:19:32Z",
          "url": "https://github.com/cpunion/llgo/commit/18e4ba595415435c7e1cba53dab4c47679a8bda7"
        },
        "date": 1785209271478,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkMergeCompilerFlags (github.com/goplus/llgo/internal/clang)",
            "value": 136.2,
            "unit": "ns/op",
            "extra": "1917721 times"
          },
          {
            "name": "BenchmarkMergeLinkerFlags (github.com/goplus/llgo/internal/clang)",
            "value": 83.82,
            "unit": "ns/op",
            "extra": "4075639 times"
          },
          {
            "name": "BenchmarkLookupPCRandom (github.com/goplus/llgo/internal/build/funcinfo)",
            "value": 11.9,
            "unit": "ns/op",
            "extra": "21540891 times"
          },
          {
            "name": "BenchmarkGlobalRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.038,
            "unit": "ns/op",
            "extra": "249654644 times\n3 procs"
          },
          {
            "name": "BenchmarkTLSRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.036,
            "unit": "ns/op",
            "extra": "281256372 times\n3 procs"
          },
          {
            "name": "BenchmarkGLSRead (github.com/goplus/llgo/test/llgoext)",
            "value": 1.057,
            "unit": "ns/op",
            "extra": "298121091 times\n3 procs"
          },
          {
            "name": "BenchmarkGlobalWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 1.038,
            "unit": "ns/op",
            "extra": "286473841 times\n3 procs"
          },
          {
            "name": "BenchmarkTLSWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 1.082,
            "unit": "ns/op",
            "extra": "291272739 times\n3 procs"
          },
          {
            "name": "BenchmarkGLSWrite (github.com/goplus/llgo/test/llgoext)",
            "value": 1.258,
            "unit": "ns/op",
            "extra": "219744802 times\n3 procs"
          },
          {
            "name": "BenchmarkDirectCall (github.com/goplus/llgo/test/llgoext)",
            "value": 1.085,
            "unit": "ns/op",
            "extra": "266023774 times\n3 procs"
          },
          {
            "name": "BenchmarkInterfaceCall (github.com/goplus/llgo/test/llgoext)",
            "value": 5.165,
            "unit": "ns/op",
            "extra": "58426389 times\n3 procs"
          },
          {
            "name": "BenchmarkDefer (github.com/goplus/llgo/test/llgoext)",
            "value": 43.13,
            "unit": "ns/op",
            "extra": "8904213 times\n3 procs"
          },
          {
            "name": "BenchmarkChannelBuffered (github.com/goplus/llgo/test/llgoext)",
            "value": 25.83,
            "unit": "ns/op",
            "extra": "13382985 times\n3 procs"
          },
          {
            "name": "BenchmarkChannelHandoff (github.com/goplus/llgo/test/llgoext)",
            "value": 7861,
            "unit": "ns/op",
            "extra": "41557 times\n3 procs"
          },
          {
            "name": "BenchmarkRuntimeGetG (github.com/goplus/llgo/test/llgoext)",
            "value": 2.899,
            "unit": "ns/op",
            "extra": "100000000 times\n3 procs"
          },
          {
            "name": "BenchmarkGoroutine (github.com/goplus/llgo/test/llgoext)",
            "value": 27830,
            "unit": "ns/op",
            "extra": "100 times\n3 procs"
          }
        ]
      }
    ]
  }
}