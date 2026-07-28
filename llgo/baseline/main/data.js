window.BENCHMARK_DATA = {
  "lastUpdate": 1785209264708,
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
    ]
  }
}