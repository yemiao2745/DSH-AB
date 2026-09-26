"""Unit tests for process enumeration and bounded runs (build/py/dshab_verify/win_proc.py).

Contract: BUILD_CONTRACT.md section 4, scenario 1 assertions 1.12-1.14 and 1.22-1.27 - "no new
DSH_AB.exe process", the stand-in process started from inside the install root, "the uninstall ended
the process under this install root", "no process is left under this install root", and "a run that
never exits is a timeout, not a hang" (a modal dialog in a silent install). The previous acceptance
script asked the shell for all of this; this module does it with the process API directly, so the
tests must never need a shell either.

The test process is the project interpreter started directly (never through a shell) and every
process started here is killed in tearDown.
"""

from __future__ import annotations

import subprocess
import sys
import time
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = REPO_ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))

from dshab_verify import win_proc, win_rmtree  # noqa: E402

WORK = REPO_ROOT / ".tmp-tests" / "verify-procs"
SLEEP_FOREVER = "import time; time.sleep(60)"


def start_sleeper():
    process = subprocess.Popen(
        [sys.executable, "-c", SLEEP_FOREVER],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline and not win_proc.is_alive(process.pid):
        time.sleep(0.1)
    if not win_proc.is_alive(process.pid):
        raise AssertionError(f"sleeper {process.pid} never came up")
    return process


class ProcessTests(unittest.TestCase):
    def setUp(self):
        win_rmtree.rmtree(WORK)
        WORK.mkdir(parents=True)
        self.sleeper = start_sleeper()
        self.pids = [self.sleeper.pid]

    def tearDown(self):
        for pid in self.pids:
            if win_proc.is_alive(pid):
                win_proc.kill_tree(pid)
        try:
            self.sleeper.wait(timeout=30)  # reap the child instead of leaving a live handle behind
        except Exception:
            pass
        win_rmtree.rmtree(WORK, retries=5)

    # ---- enumeration -----------------------------------------------------------------------------

    def test_the_snapshot_finds_the_started_process_by_name(self):
        for name in ("python", "python.exe", "PYTHON.EXE"):
            with self.subTest(name=name):
                self.assertIn(self.sleeper.pid, win_proc.pids_by_name(name))

    def test_the_image_path_names_the_running_interpreter(self):
        path = win_proc.image_path(self.sleeper.pid)
        self.assertIsNotNone(path)
        self.assertTrue(path.lower().endswith("python.exe"), path)
        self.assertTrue(Path(path).is_file(), path)

    def test_the_image_path_of_a_pid_that_does_not_exist_is_none(self):
        self.assertIsNone(win_proc.image_path(999999))

    def test_pids_under_matches_only_processes_below_the_root(self):
        root = Path(win_proc.image_path(self.sleeper.pid)).parent
        found = win_proc.pids_under(root)
        self.assertIn(self.sleeper.pid, [pid for pid, _path in found])
        prefix = str(root).lower().rstrip("\\") + "\\"
        for pid, path in found:
            self.assertTrue(path.lower().startswith(prefix), f"{pid} 的 {path} 不在 {root} 下")

    def test_pids_under_ignores_other_roots(self):
        elsewhere = WORK / "elsewhere"
        elsewhere.mkdir()
        self.assertEqual(win_proc.pids_under(elsewhere), [])
        self.assertNotIn(self.sleeper.pid, [pid for pid, _path in win_proc.pids_under(elsewhere)])

    def test_pids_under_a_missing_directory_is_empty(self):
        self.assertEqual(win_proc.pids_under(WORK / "not-there"), [])

    # ---- liveness and ending a tree --------------------------------------------------------------

    def test_is_alive_tracks_the_process(self):
        self.assertTrue(win_proc.is_alive(self.sleeper.pid))
        win_proc.kill_tree(self.sleeper.pid)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline and win_proc.is_alive(self.sleeper.pid):
            time.sleep(0.1)
        self.assertFalse(win_proc.is_alive(self.sleeper.pid))

    def test_is_alive_is_false_for_a_pid_that_does_not_exist(self):
        self.assertFalse(win_proc.is_alive(999999))

    # ---- bounded runs ----------------------------------------------------------------------------

    def test_run_bounded_returns_the_exit_code(self):
        result = win_proc.run_bounded([sys.executable, "-c", "import sys; sys.exit(3)"], 60)
        self.assertEqual(result.returncode, 3)

    def test_run_bounded_captures_combined_output_on_request(self):
        result = win_proc.run_bounded(
            [sys.executable, "-c", "import sys; print('out'); print('err', file=sys.stderr)"],
            60,
            capture=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assertIn("out", result.stdout)
        self.assertIn("err", result.stdout)

    def test_run_bounded_times_out_and_ends_the_child_it_started(self):
        pid_file = WORK / "child.pid"
        command = [
            sys.executable,
            "-c",
            "import os, sys, time; open(sys.argv[1], 'w').write(str(os.getpid())); time.sleep(60)",
            str(pid_file),
        ]
        with self.assertRaises(win_proc.ProcessTimeout) as caught:
            win_proc.run_bounded(command, 3)
        self.assertIn("超时", str(caught.exception))
        if not pid_file.is_file():
            # The child never reached its first statement, so there is no pid to check; that is this
            # environment's problem, not the module's, and it must not be reported as a pass.
            self.skipTest("子进程没来得及写下自己的 pid（环境不支持这条用例）")
        pid = int(pid_file.read_text(encoding="ascii").strip())
        self.pids.append(pid)
        self.assertFalse(win_proc.is_alive(pid), f"超时后 {pid} 还活着")

    # ---- waiting for the uninstaller -------------------------------------------------------------

    def test_wait_for_uninstaller_returns_once_nothing_is_left(self):
        if any(
            name.lower().startswith(("unins", "_unins"))
            for _pid, _ppid, name in win_proc.iter_processes()
        ):
            self.skipTest("本机此刻有卸载程序在跑，等它结束的用例不适用")
        started = time.monotonic()
        win_proc.wait_for_uninstaller(WORK / "gone", 30)
        self.assertGreater(time.monotonic() - started, 0.5)

    def test_wait_for_uninstaller_times_out_while_the_uninstaller_is_still_there(self):
        directory = WORK / "still-there"
        directory.mkdir()
        (directory / "unins000.exe").write_bytes(b"stub")
        with self.assertRaises(win_proc.ProcessTimeout):
            win_proc.wait_for_uninstaller(directory, 0.5)


if __name__ == "__main__":
    unittest.main()
