from pathlib import Path
import subprocess
import tempfile
import unittest


class RestoreCoverageTest(unittest.TestCase):
    script = Path(__file__).with_name("r4_extract_coverage.sh")

    def extract(self, text, count):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "reports"
            result = subprocess.run(["bash", str(self.script), str(output), str(count)],
                                    input=text, text=True, capture_output=True, timeout=5)
            files = {path.name: path.read_text() for path in output.iterdir()}
            return result, files

    def test_original_records_and_empty_package_report_are_preserved(self):
        report = "mode: atomic\ngithub.com/xgo-dev/llgo/cl/expr.go:12.1,16.2 3 987654321\n"
        archive = ("source/file.go\n<<<<<< network\n\n"
                   "# path=D:\\a\\llgo\\llgo\\coverage-main.txt\n" + report +
                   "<<<<<< EOF\n\n# path=/repo/coverage-test-go.txt\nmode: atomic\n<<<<<< EOF\n")
        result, files = self.extract(archive, 2)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(files, {"coverage-main.txt": report,
                                 "coverage-test-go.txt": "mode: atomic\n"})

    def test_malformed_or_incomplete_archives_fail(self):
        header = "<<<<<< network\n# path=/repo/coverage-main.txt\n"
        valid = header + "mode: atomic\n<<<<<< EOF\n"
        for archive, count in (
            (valid, 2),
            (header + "mode: atomic\n", 1),
            (header + "mode: set\n<<<<<< EOF\n", 1),
            (header + "mode: atomic\nnot coverage\n<<<<<< EOF\n", 1),
            (valid + valid, 2),
            (valid.replace("coverage-main.txt", "unexpected.txt"), 1),
            (valid.replace("<<<<<< network\n", ""), 1),
        ):
            with self.subTest(archive=archive, count=count):
                result, _ = self.extract(archive, count)
                self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
