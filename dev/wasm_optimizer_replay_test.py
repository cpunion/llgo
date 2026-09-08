import importlib.util
import json
from pathlib import Path
import signal
import tempfile
import unittest
from unittest.mock import MagicMock, patch


spec = importlib.util.spec_from_file_location("replay", Path(__file__).with_name("wasm_optimizer_replay.py"))
replay = importlib.util.module_from_spec(spec)
spec.loader.exec_module(replay)


class ReplayTest(unittest.TestCase):
    def test_only_measured_process_group_counts(self):
        with patch.object(replay.subprocess, "check_output", return_value="12 200\n12 300\n13 9000\n"):
            self.assertEqual(replay.group_rss(12), 500)

    def test_success_preserves_command_and_status(self):
        with tempfile.TemporaryDirectory() as directory:
            process = MagicMock(pid=123)
            process.poll.side_effect = [None, 0, 0]
            process.wait.return_value = 0
            command = ["wasm-opt", "--asyncify", "--translate-to-exnref", "-Os", "input with spaces.wasm"]
            with patch.object(replay.subprocess, "Popen", return_value=process) as spawn, \
                 patch.object(replay, "group_rss", return_value=100), patch.object(replay.time, "sleep"):
                self.assertTrue(replay.bounded_run("test", command, Path(directory)))
            self.assertEqual(spawn.call_args.args[0][4:], command)
            self.assertTrue(spawn.call_args.kwargs["start_new_session"])
            result = json.loads((Path(directory) / "test/result.json").read_text())
            self.assertEqual(result["peak_group_rss_kib"], 100)
            self.assertEqual(result["returncode"], 0)
            self.assertIsNone(result["limit"])

    def test_limits_kill_only_the_measured_group_and_remain_failures(self):
        for name, rss, elapsed in (("RSS limit", 4096*1024+1, 0), ("timeout", 1, 181)):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                process = MagicMock(pid=321)
                process.poll.return_value = None
                process.wait.return_value = -9
                with patch.object(replay.subprocess, "Popen", return_value=process), \
                     patch.object(replay, "group_rss", return_value=rss), \
                     patch.object(replay.time, "monotonic", side_effect=[0, elapsed, elapsed]), \
                     patch.object(replay.os, "killpg") as kill:
                    self.assertFalse(replay.bounded_run("test", ["wasm-opt"], Path(directory)))
                kill.assert_called_once_with(321, signal.SIGKILL)
                result = json.loads((Path(directory) / "test/result.json").read_text())
                self.assertEqual(result["limit"], name)
                self.assertEqual(result["returncode"], -9)


if __name__ == "__main__":
    unittest.main()
