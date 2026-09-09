import sys
import time
import unittest

from test_gc_embedded import run_until_marker


class EmbeddedCollectorWitnessTests(unittest.TestCase):
    def test_completion_and_exit(self):
        verdict, output = run_until_marker([sys.executable, "-c", "print('gc standalone ok')"])
        self.assertEqual(verdict["result"], "pass", output)

    def test_successful_exit_without_witness_fails(self):
        verdict, _ = run_until_marker([sys.executable, "-c", "print('emulator started')"])
        self.assertEqual(verdict["result"], "fail")

    def test_diagnostic_substring_is_not_completion(self):
        verdict, _ = run_until_marker([sys.executable, "-c", "print('error: gc standalone ok')"])
        self.assertEqual(verdict["result"], "fail")

    def test_firmware_stops_after_checked_marker(self):
        started = time.monotonic()
        verdict, _ = run_until_marker([sys.executable, "-c",
                                       "import time; print('gc standalone ok',flush=True); time.sleep(20)"])
        self.assertEqual(verdict["result"], "pass")
        self.assertEqual(verdict["reason"], "completion-marker")
        self.assertLess(time.monotonic() - started, 5)

    def test_timeout_is_never_a_pass(self):
        verdict, _ = run_until_marker([sys.executable, "-c", "import time; time.sleep(20)"], 0.2)
        self.assertEqual(verdict["result"], "timeout")


if __name__ == "__main__":
    unittest.main()
