#!/usr/bin/env python3

import pathlib
import tempfile
import unittest

from ubuntu_apt import configure


class UbuntuAPTTest(unittest.TestCase):
    def test_runner_mirror_lists(self):
        for arch, archive, security in (
            ("amd64", "https://archive.ubuntu.com/ubuntu/\tpriority:1\n"
             "http://archive.ubuntu.com/ubuntu/\tpriority:2",
             "https://security.ubuntu.com/ubuntu/\tpriority:1\n"
             "http://security.ubuntu.com/ubuntu/\tpriority:2"),
            ("i386", "https://archive.ubuntu.com/ubuntu/\tpriority:1\n"
             "http://archive.ubuntu.com/ubuntu/\tpriority:2",
             "https://security.ubuntu.com/ubuntu/\tpriority:1\n"
             "http://security.ubuntu.com/ubuntu/\tpriority:2"),
            ("arm64", "https://ports.ubuntu.com/ubuntu-ports/\tpriority:1\n"
             "http://ports.ubuntu.com/ubuntu-ports/\tpriority:2",
             "https://ports.ubuntu.com/ubuntu-ports/\tpriority:1\n"
             "http://ports.ubuntu.com/ubuntu-ports/\tpriority:2"),
            ("armhf", "https://ports.ubuntu.com/ubuntu-ports/\tpriority:1\n"
             "http://ports.ubuntu.com/ubuntu-ports/\tpriority:2",
             "https://ports.ubuntu.com/ubuntu-ports/\tpriority:1\n"
             "http://ports.ubuntu.com/ubuntu-ports/\tpriority:2"),
        ):
            with self.subTest(arch=arch), tempfile.TemporaryDirectory() as tmp:
                root = pathlib.Path(tmp)
                (root / "apt.conf.d").mkdir()
                for name in ("apt-mirrors.txt", "apt-mirrors-security.txt"):
                    (root / name).write_text(
                        "https:/us.archive.ubuntu.com/ubuntu\n"
                        "https://mirrors.edge.kernel.org/ubuntu\n")
                sources = root / "ubuntu.sources"
                source_text = "URIs: mirror+file:/etc/apt/apt-mirrors.txt\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n"
                sources.write_text(source_text)
                third_party = root / "custom-mirrors.txt"
                third_party.write_text("https://apt.llvm.org/noble/\n")
                configure(root, arch)
                configure(root, arch)
                self.assertEqual((root / "apt-mirrors.txt").read_text(), archive + "\n")
                self.assertEqual((root / "apt-mirrors-security.txt").read_text(), security + "\n")
                self.assertEqual(sources.read_text(), source_text)
                self.assertEqual(third_party.read_text(), "https://apt.llvm.org/noble/\n")

    def test_direct_sources_need_no_mirror_lists(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "apt.conf.d").mkdir()
            sources = root / "sources.list"
            source_text = "deb https://archive.ubuntu.com/ubuntu/ noble main\n"
            sources.write_text(source_text)
            third_party = root / "sources.list.d" / "llvm.list"
            third_party.parent.mkdir()
            third_party_text = "deb https://apt.llvm.org/noble/ llvm-toolchain-noble-22 main\n"
            third_party.write_text(third_party_text)
            configure(root, "amd64")
            self.assertEqual(sources.read_text(), source_text)
            self.assertEqual(third_party.read_text(), third_party_text)
            self.assertFalse((root / "apt-mirrors.txt").exists())
            self.assertFalse((root / "apt-mirrors-security.txt").exists())
            self.assertTrue((root / "apt.conf.d" / "80-llgo-downloads").is_file())

    def test_transport_fallback_preserves_authentication_and_strict_failures(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "apt.conf.d").mkdir()
            configure(root, "amd64")
            settings = (root / "apt.conf.d" / "80-llgo-downloads").read_text()
            self.assertIn('APT::Update::Error-Mode "any";', settings)
            self.assertIn('Acquire::ForceIPv4 "true";', settings)
            self.assertIn('Acquire::Retries "3";', settings)
            self.assertIn('Acquire::http::Timeout "30";', settings)
            self.assertIn('Acquire::https::Timeout "30";', settings)
            self.assertIn('Acquire::Languages "none";', settings)
            self.assertNotIn("AllowUnauthenticated", settings)
            self.assertNotIn("AllowInsecure", settings)
            self.assertNotIn("Verify-Peer", settings)
            self.assertNotIn("Verify-Host", settings)


if __name__ == "__main__":
    unittest.main()
