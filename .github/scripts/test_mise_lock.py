from pathlib import Path
import tomllib
import unittest


ROOT = Path(__file__).resolve().parents[2]

# Official Go release archives: https://go.dev/dl/?mode=json&include=all.
# Refresh these checksums together with .go-version and the Go lock entry.
GO_RELEASE = "1.27.3"
GO_ARCHIVES = {
    "linux-arm64": ("linux-arm64.tar.gz", "a06e99309dbcbdca1a8ccd2282c3ee146e55930886e26b4f1193269587165f49"),
    "linux-x64": ("linux-amd64.tar.gz", "65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40"),
    "macos-arm64": ("darwin-arm64.tar.gz", "dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2"),
    "macos-x64": ("darwin-amd64.tar.gz", "b6f8e931e4ab497fcde6da8ae4499a58f9d258e55c8a80824a516eae5950a750"),
    "windows-x64": ("windows-amd64.zip", "f26081f438319757f8a1b46d7d0b2c5ec7583d4038e485e0aa725cfde7885fff"),
}


class MiseLockTests(unittest.TestCase):
    def test_go_lock_matches_selected_sdk_and_official_archives(self):
        version = (ROOT / ".go-version").read_text().strip()
        self.assertEqual(version, GO_RELEASE)
        config = tomllib.loads((ROOT / "dev/mise.toml").read_text())
        self.assertTrue(config["tool_config"]["locked"])
        self.assertEqual(config["tools"]["go"], "{{ read_file(path='../.go-version') | trim }}")

        lock = tomllib.loads((ROOT / "dev/mise.lock").read_text())
        self.assertEqual(lock["lockfile_version"], 3)
        self.assertEqual(len(lock["tools"]["go"]), 1)
        go = lock["tools"]["go"][0]
        self.assertEqual(go["version"], version)
        self.assertEqual(go["specifiers"], [version])
        self.assertEqual(go["backend"], "core:go")
        self.assertEqual({key for key in go if key.startswith("platforms.")},
                         {"platforms." + platform for platform in GO_ARCHIVES})
        for platform, (archive, checksum) in GO_ARCHIVES.items():
            with self.subTest(platform=platform):
                self.assertEqual(go["platforms." + platform], {
                    "url": "https://dl.google.com/go/go" + version + "." + archive,
                    "checksum": "sha256:" + checksum,
                })


if __name__ == "__main__":
    unittest.main()
