window.BENCHMARK_DATA = {
  "lastUpdate": 1785231120985,
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
    ]
  }
}