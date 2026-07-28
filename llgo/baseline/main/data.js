window.BENCHMARK_DATA = {
  "lastUpdate": 1785209266099,
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
    ]
  }
}