"""Unit tests for the scenario runner (build/py/dshab_verify/scenarios.py) and the report
(build/py/dshab_verify/report.py).

Contract: BUILD_CONTRACT.md section 4 "Verification contract" - the five scenarios, their assertion
labels and their order, the installer command lines, and "user-visible state is put back no matter
what". Nothing here installs anything: the installs themselves are the acceptance run's job, so the
tests cover the pure parts (command line construction, assertion naming and wiring) plus the
snapshot/restore round trip against a throwaway uninstall entry.
"""

from __future__ import annotations

import ast
import contextlib
import ctypes
import io
import os
import re
import subprocess
import sys
import unittest
import winreg
from ctypes import wintypes
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = REPO_ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))

from dshab_build import steplog  # noqa: E402
from dshab_verify import scenarios, win_reg, win_rmtree  # noqa: E402
from dshab_verify.report import Report  # noqa: E402

WORK = REPO_ROOT / ".tmp-tests" / "verify-scenarios"
SCENARIOS_PY = BUILD_PY / "dshab_verify" / "scenarios.py"

# Every assertion label, in the order the scenario body records them. The contract numbers the
# scenarios' assertions by clause; this table is the mapping from clause to label, so a renumbered or
# reordered assertion fails here instead of silently drifting away from the contract.
EXPECTED_ASSERTIONS = {
    "_scenario_1": [
        ("1.1", "silent install exits 0"),
        ("1.2", "install-root file set == the recorded install_files"),
        ("1.3", "live payload == the record, both directions"),
        ("1.4", "state/ and logs/ exist as directories"),
        ("1.5", "no AGENTS.md / git leftovers in the install root"),
        ("1.6", "slot-b exists and is empty"),
        ("1.7", "the three baked files have no BOM and equal the template with the root substituted"),
        ("1.8", "the shipped skill passes for build/templates/skills"),
        ("1.9", "the shipped skill passes for <Dir>/slot-a/data/skills"),
        ("1.10", "sha256(build/DSH_AB.exe) == sha256(<Dir>/DSH_AB.exe)"),
        ("1.11", "the manifest's exe_sha256 agrees"),
        ("1.12", "the installed exe's --version contains both versions"),
        ("1.13", "no new DSH_AB.exe process was started"),
        ("1.14", "the bundled runtime git --version exits 0"),
        ("1.15", "dsh-ab.toml has no test key and production == the port"),
        ("1.16", "this installation has its own uninstall entry"),
        ("1.17", "EstimatedSize is within +/-10% of the measured directory size"),
        ("1.18", "DisplayName has the DSH-AB shape"),
        ("1.19", "DisplayName does not contain the version"),
        ("1.20", "DisplayVersion == the manifest version"),
        ("1.21", "both recorded shortcuts are usable and the start-menu one exists"),
        ("1.22", "the stand-in process runs inside the install root"),
        ("1.23", "the uninstaller exists"),
        ("1.24", "silent uninstall exits 0"),
        ("1.25", "the uninstaller really finished"),
        ("1.26", "the uninstall ended the process under the install root"),
        ("1.27", "no process is left under the install root"),
        ("1.28", "the uninstall deleted the shortcuts it recorded"),
        ("1.29", "DSH_AB.exe is gone after the uninstall"),
        ("1.30", "the uninstall kept the user-data marker"),
        ("1.31", "the uninstall kept every recorded slot-a data file"),
    ],
    "_scenario_2": [
        ("2.1", "silent install exits 0"),
        ("2.2", "the uninstaller exists"),
        ("2.3", "the /DELETEUSERDATA=1 uninstall exits 0"),
        ("2.4", "the uninstaller really finished"),
        ("2.5", "the uninstaller deleted the user data"),
        ("2.6", "nothing is left in the install root"),
        ("2.7", "the uninstall entry is gone"),
    ],
    "_scenario_3": [
        ("3.1", "a non-empty directory with silent flags is refused with a non-zero exit code"),
        ("3.2", "the refusal wrote a log"),
        ("3.3", "the log records the refusal"),
        ("3.4", "the file that was already there survived"),
        ("3.5", "nothing was installed into the non-empty directory"),
    ],
    "_scenario_4": [
        ("4.0", "the production port can be held"),
        ("4.1", "a taken port with silent flags is refused with a non-zero exit code"),
        ("4.2", "the refusal wrote a log"),
        ("4.3", "the log explains the taken port"),
        ("4.4", "the refused installation left no files"),
    ],
}

SECTIONS = {
    "_scenario_1": "[1/5]",
    "_scenario_2": "[2/5]",
    "_scenario_3": "[3/5]",
    "_scenario_4": "[4/5]",
    "_scenario_5": "[5/5]",
}


def parse_windows_command_line(command_line):
    """The argv the installer itself would see: the shell's own CommandLineToArgvW."""
    shell32 = ctypes.WinDLL("shell32", use_last_error=True)
    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    shell32.CommandLineToArgvW.restype = ctypes.POINTER(ctypes.c_wchar_p)
    shell32.CommandLineToArgvW.argtypes = [wintypes.LPCWSTR, ctypes.POINTER(ctypes.c_int)]
    kernel32.LocalFree.argtypes = [ctypes.c_void_p]
    count = ctypes.c_int()
    argv = shell32.CommandLineToArgvW(command_line, ctypes.byref(count))
    if not argv:
        raise OSError("CommandLineToArgvW 失败")
    try:
        return [argv[index] for index in range(count.value)]
    finally:
        kernel32.LocalFree(ctypes.cast(argv, ctypes.c_void_p))


def scenario_source():
    source = SCENARIOS_PY.read_text(encoding="utf-8")
    tree = ast.parse(source)
    functions = {
        node.name: node for node in tree.body if isinstance(node, ast.FunctionDef)
    }
    return source, functions


def assertion_numbers(source, functions, function_name):
    """The 1.2-style labels in the body of one scenario, in source order, duplicates collapsed."""
    segment = ast.get_source_segment(source, functions[function_name])
    wanted = function_name.rsplit("_", 1)[1] + "."
    numbers = []
    for match in re.finditer(r"(\d+\.\d+)(?=\s)", segment):
        number = match.group(1)
        if not number.startswith(wanted):
            continue
        if not numbers or numbers[-1] != number:
            numbers.append(number)
    return numbers


def make_config(**overrides):
    settings = {
        "repo_root": WORK / "repo",
        "install_dir": WORK / "install",
        "setup": WORK / "dist" / "DSH-AB-1.0-2.0-setup.exe",
        "log_dir": WORK / "logs",
        "manifest": {
            "dshab_version": "1.0",
            "dsh_version": "2.0",
            "install_files": ["DSH_AB.exe"],
        },
        "port": 3190,
    }
    settings.update(overrides)
    return scenarios.Config(**settings)


class TempWork(unittest.TestCase):
    def setUp(self):
        win_rmtree.rmtree(WORK, retries=5)
        WORK.mkdir(parents=True)

    def tearDown(self):
        win_rmtree.rmtree(WORK, retries=5)


class CommandLineTests(TempWork):
    def test_the_plain_install_directory_is_passed_as_one_bare_argument(self):
        cfg = make_config()
        argv = [str(item) for item in scenarios._install_argv(cfg, WORK / "s1.log", cfg.install_dir)]

        self.assertEqual(
            argv,
            [
                str(cfg.setup),
                "/VERYSILENT",
                "/SUPPRESSMSGBOXES",
                "/NORESTART",
                f"/LOG={WORK / 's1.log'}",
                f"/DIR={cfg.install_dir}",
                f"/PRODUCTION={cfg.port}",
            ],
        )
        command_line = subprocess.list2cmdline(argv)
        self.assertIn(f"/DIR={cfg.install_dir}", command_line)
        self.assertNotIn(f'"/DIR={cfg.install_dir}"', command_line)
        self.assertEqual(
            parse_windows_command_line(command_line)[5], f"/DIR={cfg.install_dir}"
        )

    def test_a_directory_with_spaces_stays_one_quoted_argument(self):
        cfg = make_config()
        target = scenarios._spaced_install_dir(cfg)
        self.assertEqual(target.name, cfg.install_dir.name + " with space")

        argv = [str(item) for item in scenarios._install_argv(cfg, WORK / "s3.log", target)]
        for item in argv:
            self.assertNotIn('"', item, "引号是 subprocess 的事，参数里不该自带引号")

        command_line = subprocess.list2cmdline(argv)
        self.assertIn(f'"/DIR={target}"', command_line)
        self.assertEqual(command_line.count(str(target)), 1)
        parsed = parse_windows_command_line(command_line)
        self.assertIn(f"/DIR={target}", parsed)
        self.assertEqual(len(parsed), len(argv), parsed)

    def test_the_recorded_command_is_the_same_list_that_was_run(self):
        argv = ["a", "b c"]
        self.assertEqual(scenarios._command(argv), "a b c")


class AssertionNamingTests(unittest.TestCase):
    """The contract's assertion labels, their order and the order of the five scenarios."""

    @classmethod
    def setUpClass(cls):
        cls.source, cls.functions = scenario_source()

    def test_every_scenario_uses_the_contract_labels_in_order(self):
        for name, expected in EXPECTED_ASSERTIONS.items():
            with self.subTest(scenario=name):
                numbers = assertion_numbers(self.source, self.functions, name)
                self.assertEqual(numbers, [number for number, _clause in expected])

    def test_every_scenario_starts_with_its_section_header(self):
        for name, prefix in SECTIONS.items():
            with self.subTest(scenario=name):
                segment = ast.get_source_segment(self.source, self.functions[name])
                self.assertIn(f'rep.section("{prefix}', segment)

    def test_the_run_function_calls_the_scenarios_in_order(self):
        calls = sorted(
            (
                node.lineno,
                node.func.id,
            )
            for node in ast.walk(self.functions["run"])
            if isinstance(node, ast.Call)
            and isinstance(node.func, ast.Name)
            and re.match(r"^_scenario_\d$", node.func.id)
        )
        self.assertEqual([name for _line, name in calls], [f"_scenario_{n}" for n in range(1, 6)])

    def test_scenario_5_records_one_assertion_per_outside_path(self):
        segment = ast.get_source_segment(self.source, self.functions["_scenario_5"])
        self.assertIn('f"5.{index} 工作区外没有安装痕迹', segment)
        self.assertIn("cfg.install_dir", segment)


class ScenarioFiveTests(TempWork):
    def make_rep(self):
        return Report(WORK / "report.txt", REPO_ROOT, echo=False)

    def test_it_passes_when_nothing_was_left_outside(self):
        cfg = make_config()
        outside = [WORK / "outside-one", WORK / "outside-two"]
        rep = self.make_rep()

        scenarios._scenario_5(cfg, rep, outside, {path: False for path in outside})

        self.assertEqual(rep.failures, [])
        self.assertTrue(any("5.1 工作区外没有安装痕迹" in line for line in rep.lines), rep.lines)

    def test_it_fails_when_something_was_left_outside(self):
        cfg = make_config()
        residue = WORK / "outside-one"
        residue.mkdir()
        outside = [residue]
        rep = self.make_rep()

        with self.assertRaises(scenarios.VerifyError) as caught:
            scenarios._scenario_5(cfg, rep, outside, {residue: False})

        self.assertIn("工作区外留下了安装痕迹", str(caught.exception))
        self.assertEqual(rep.failures, ["5.1 工作区外没有安装痕迹：%s" % residue])

    def test_a_preexisting_path_is_not_counted_as_residue(self):
        cfg = make_config()
        preexisting = WORK / "was-already-there"
        preexisting.mkdir()
        rep = self.make_rep()

        scenarios._scenario_5(cfg, rep, [preexisting], {preexisting: True})

        self.assertEqual(rep.failures, [])


class UserStateTests(TempWork):
    """The snapshot/restore of the user's own installation, exercised on a throwaway entry."""

    def setUp(self):
        super().setUp()
        self.created_keys = []

    def tearDown(self):
        for key_path in (self.created_keys + [r"HKCU\Software\DSH-AB-tests\%d" % os.getpid()]):
            try:
                win_reg.delete_tree(key_path)
            except OSError:
                pass
        super().tearDown()

    def make_throwaway_key(self, label):
        sub = r"Software\DSH-AB-tests\%d\%s" % (os.getpid(), label)
        try:
            with winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, sub, 0, winreg.KEY_WRITE):
                pass
        except OSError as exc:
            raise unittest.SkipTest(f"HKCU 不可写，跳过注册表用例（{sub}：{exc}）")
        key_path = "HKCU\\" + sub
        self.created_keys.append(key_path)
        return key_path

    def test_it_restores_the_entry_and_the_shortcut_bytes(self):
        key_path = self.make_throwaway_key("user-state")
        link = WORK / "Start Menu" / "DSH-AB.lnk"
        link.parent.mkdir(parents=True)
        link.write_bytes(b"\x4c\x00\x00\x00 original")
        _, sub = win_reg.split_path(key_path)
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, sub, 0, winreg.KEY_SET_VALUE) as key:
            winreg.SetValueEx(key, "DshAbStartMenuLink", 0, winreg.REG_SZ, str(link))

        state = scenarios.UserState(make_config(), Report(WORK / "report.txt", REPO_ROOT, echo=False))
        state.key = key_path  # never the user's real entry
        state.save()

        self.assertIsNotNone(state.saved)
        self.assertIn("DshAbStartMenuLink", state.saved)
        self.assertEqual(len(state.entries), 1)
        path, backup = state.entries[0]
        self.assertEqual(path, link)
        self.assertIsNotNone(backup)

        # What the acceptance run may do to the user's installation while it is installing.
        link.write_bytes(b"\x4c\x00\x00\x00 rewritten")
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, sub, 0, winreg.KEY_SET_VALUE) as key:
            winreg.SetValueEx(key, "UninstallString", 0, winreg.REG_SZ, "added by the run")

        state.restore()

        self.assertEqual(link.read_bytes(), b"\x4c\x00\x00\x00 original")
        self.assertEqual(win_reg.read_values(key_path), state.saved)
        self.assertIsNone(win_reg.read_value(key_path, "UninstallString"))

    def test_it_deletes_a_shortcut_that_was_not_there_before_the_run(self):
        key_path = self.make_throwaway_key("link-added")
        appeared = WORK / "Start Menu" / "DSH-AB (new).lnk"
        appeared.parent.mkdir(parents=True)

        state = scenarios.UserState(make_config(), Report(WORK / "report.txt", REPO_ROOT, echo=False))
        state.key = key_path
        state.entries = [(appeared, None)]
        state.saved = None

        appeared.write_bytes(b"\x4c\x00\x00\x00 created by the run")
        state.restore()

        self.assertFalse(appeared.exists())
        self.assertFalse(win_reg.key_exists(key_path))


def no_environment_preflight():
    """run()'s environment preflight, swapped for a no-op and put back when the with block exits.

    The orchestration tests below cover what run() does around the scenarios (save, run all five,
    restore, raise); whether this machine can take an install at all is the real preflight's
    question and is covered by its own test. Leaving the real one in place would make these three
    depend on the machine's shortcut folders being writable.
    """
    return mock.patch.object(scenarios, "PREFLIGHT", lambda rep: None)


class RunOrchestrationTests(TempWork):
    """run() must restore the user's state whatever happens, and must not swallow a failure."""

    def setUp(self):
        super().setUp()
        self.state_log = []
        # run() looks for the residue of a finished run outside the workspace: give it a private place
        # to look instead of depending on the ambient user profile.
        self._saved_local_appdata = os.environ.get("LOCALAPPDATA")
        os.environ["LOCALAPPDATA"] = str(WORK / "localappdata")

    def tearDown(self):
        if self._saved_local_appdata is None:
            os.environ.pop("LOCALAPPDATA", None)
        else:
            os.environ["LOCALAPPDATA"] = self._saved_local_appdata
        super().tearDown()

    def fake_state(self, restore_error=None):
        log = self.state_log

        class FakeState:
            def __init__(self, cfg, rep):
                self.rep = rep

            def save(self):
                log.append("save")

            def restore(self):
                log.append("restore")
                if restore_error is not None:
                    raise restore_error

        return FakeState

    def test_the_user_state_is_saved_and_restored_around_all_five_scenarios(self):
        order = []

        def recorder(name):
            def call(*_args):
                order.append(name)

            return call

        rep = Report(WORK / "report.txt", REPO_ROOT, echo=False)
        cfg = make_config(log_dir=WORK)
        with no_environment_preflight(), mock.patch.object(
            scenarios, "UserState", self.fake_state()
        ), mock.patch.object(
            scenarios, "_scenario_1", recorder("1")
        ), mock.patch.object(scenarios, "_scenario_2", recorder("2")), mock.patch.object(
            scenarios, "_scenario_3", recorder("3")
        ), mock.patch.object(scenarios, "_scenario_4", recorder("4")), mock.patch.object(
            scenarios, "_scenario_5", recorder("5")
        ):
            scenarios.run(cfg, rep)

        self.assertEqual(order, ["1", "2", "3", "4", "5"])
        self.assertEqual(self.state_log, ["save", "restore"])
        self.assertEqual(rep.failures, [])
        self.assertTrue(any("用户可见状态已还原" in line for line in rep.lines), rep.lines)

    def test_a_failing_scenario_still_restores_and_still_raises(self):
        def boom(*_args):
            raise scenarios.VerifyError("3.1 非空目录被拒（非 0 退出码）：退出码 0")

        rep = Report(WORK / "report.txt", REPO_ROOT, echo=False)
        cfg = make_config(log_dir=WORK)
        good = self.fake_state()
        with no_environment_preflight(), mock.patch.object(
            scenarios, "UserState", good
        ), mock.patch.object(
            scenarios, "_scenario_3", boom
        ), mock.patch.object(scenarios, "_scenario_1", lambda *a: None), mock.patch.object(
            scenarios, "_scenario_2", lambda *a: None
        ), mock.patch.object(scenarios, "_scenario_4", lambda *a: None), mock.patch.object(
            scenarios, "_scenario_5", lambda *a: None
        ):
            with self.assertRaises(scenarios.VerifyError) as caught:
                scenarios.run(cfg, rep)

        self.assertIn("非空目录被拒", str(caught.exception))
        self.assertEqual(self.state_log, ["save", "restore"])

    def test_a_restore_failure_is_recorded_and_raised(self):
        rep = Report(WORK / "report.txt", REPO_ROOT, echo=False)
        cfg = make_config(log_dir=WORK)
        with no_environment_preflight(), mock.patch.object(
            scenarios, "UserState", self.fake_state(restore_error=OSError("拿不回来"))
        ), mock.patch.object(scenarios, "_scenario_1", lambda *a: None), mock.patch.object(
            scenarios, "_scenario_2", lambda *a: None
        ), mock.patch.object(scenarios, "_scenario_3", lambda *a: None), mock.patch.object(
            scenarios, "_scenario_4", lambda *a: None
        ), mock.patch.object(scenarios, "_scenario_5", lambda *a: None):
            with self.assertRaises(scenarios.VerifyError) as caught:
                scenarios.run(cfg, rep)

        self.assertIn("用户可见状态没有还原干净", str(caught.exception))
        self.assertEqual(rep.failures, ["用户可见状态已还原"])

    def preflight_directories(self):
        """The three directories the real preflight probes, with the labels it reports them under."""
        return (
            ("%TEMP%", Path(steplog.tool_env()["TEMP"])),
            (r"开始菜单\程序", scenarios.start_menu_programs_dir()),
            ("桌面", scenarios.desktop_dir()),
        )

    def test_an_environment_that_cannot_take_the_install_blocks_the_run_before_any_scenario(self):
        """The real preflight, on a machine it must refuse: a per-directory report and zero scenarios.

        This is what the preflight is for. On a machine whose shortcut folders are read-only the
        installer dies before it can write a log, and the report then reads as a product failure -
        so run() has to refuse up front and say which directory it could not write. The machine
        decides which side of that line this test is on: one that can write all three directories
        skips, because the refusing path cannot be reached there without faking the probe.
        """
        directories = self.preflight_directories()
        unwritable = []
        for label, directory in directories:
            try:
                scenarios._probe_writable(directory)
            except OSError:
                unwritable.append(label)
        if not unwritable:
            self.skipTest(
                "这台机器能写 %TEMP%、开始菜单和桌面：真实预检不会拒绝，这一路在这里跑不到"
            )

        rep = Report(WORK / "report.txt", REPO_ROOT, echo=False)
        cfg = make_config(log_dir=WORK)
        started = []

        def counter(name):
            def call(*_args):
                started.append(name)

            return call

        with mock.patch.object(scenarios, "UserState", self.fake_state()), mock.patch.object(
            scenarios, "_scenario_1", counter("1")
        ), mock.patch.object(scenarios, "_scenario_2", counter("2")), mock.patch.object(
            scenarios, "_scenario_3", counter("3")
        ), mock.patch.object(scenarios, "_scenario_4", counter("4")), mock.patch.object(
            scenarios, "_scenario_5", counter("5")
        ):
            with self.assertRaises(scenarios.VerifyError) as caught:
                scenarios.run(cfg, rep)

        message = str(caught.exception)
        self.assertIn("环境挡住了这一轮", message)
        self.assertEqual(started, [])
        # Refusing costs nothing: the user's own installation was not even snapshotted.
        self.assertEqual(self.state_log, [])

        # Every directory is named, each with the verdict that directory really got.
        for label, _directory in directories:
            line = next((q for q in rep.lines if f"环境预检：{label} 可写（" in q), None)
            self.assertIsNotNone(line, f"报告里没有 {label} 的预检行：{rep.lines}")
            self.assertEqual(line.startswith("[FAIL]"), label in unwritable, line)
        self.assertEqual(
            [name for name in rep.failures if not name.startswith("环境预检：")], [], rep.failures
        )
        for label in unwritable:
            self.assertIn(label, message)


class CheckHelperTests(TempWork):
    def test_a_failed_check_raises_and_is_recorded(self):
        rep = Report(WORK / "report.txt", REPO_ROOT, echo=False)
        with self.assertRaises(scenarios.VerifyError) as caught:
            scenarios._check(rep, "1.1 静默安装退出码 0", False, "退出码 1")
        self.assertIn("1.1 静默安装退出码 0：退出码 1", str(caught.exception))
        self.assertEqual(rep.failures, ["1.1 静默安装退出码 0"])

    def test_a_passing_check_records_success(self):
        rep = Report(WORK / "report.txt", REPO_ROOT, echo=False)
        scenarios._check(rep, "1.1 静默安装退出码 0", True, "退出码 0")
        self.assertEqual(rep.failures, [])
        self.assertTrue(any(line.startswith("[ok  ] 1.1") for line in rep.lines), rep.lines)

    def test_fail_raises_immediately(self):
        rep = Report(WORK / "report.txt", REPO_ROOT, echo=False)
        with self.assertRaises(scenarios.VerifyError):
            scenarios._fail(rep, "1.27 本安装根下没有残留进程", "1 个残留")
        self.assertEqual(rep.failures, ["1.27 本安装根下没有残留进程"])


class HelperTests(TempWork):
    def test_recorded_files_comes_from_the_manifest(self):
        cfg = make_config(manifest={"install_files": ["a", "b"]})
        self.assertEqual(cfg.recorded_files, ["a", "b"])
        self.assertEqual(make_config(manifest={}).recorded_files, [])

    def test_entries_lists_everything_below_a_directory_and_tolerates_a_missing_one(self):
        tree = WORK / "tree"
        (tree / "sub").mkdir(parents=True)
        (tree / "sub" / "file.txt").write_text("x", encoding="ascii")
        self.assertEqual(len(scenarios._entries(tree)), 2)
        self.assertEqual(scenarios._entries(WORK / "not-there"), [])

    def test_dir_size_sums_files_only(self):
        tree = WORK / "size"
        tree.mkdir()
        (tree / "one.bin").write_bytes(b"x" * 100)
        (tree / "sub").mkdir()
        (tree / "sub" / "two.bin").write_bytes(b"y" * 23)
        self.assertEqual(scenarios._dir_size(tree), 123)
        self.assertEqual(scenarios._dir_size(WORK / "not-there"), 0)


class ReportTests(TempWork):
    def test_the_report_lists_assertions_and_the_conclusion(self):
        path = WORK / "report.txt"
        rep = Report(path, REPO_ROOT, echo=False)
        rep.header({"安装目录": WORK / "install"})
        rep.section("前置条件")
        rep.record("P1 payload 清单存在", True, "ok")
        rep.record("P2 产物名", False, "坏了")
        rep.write()

        text = path.read_text(encoding="utf-8")
        self.assertIn("DSH-AB 验收报告", text)
        self.assertIn("== 前置条件", text)
        self.assertIn("[ok  ] P1 payload 清单存在 —— ok", text)
        self.assertIn("失败 1 条", text)
        self.assertEqual(rep.failures, ["P2 产物名"])

    def test_the_conclusion_says_everything_passed(self):
        path = WORK / "report-ok.txt"
        rep = Report(path, REPO_ROOT, echo=False)
        rep.record("P1 payload 清单存在", True)
        rep.write()
        self.assertIn("全部通过", path.read_text(encoding="utf-8"))

    def test_echo_writes_to_stdout(self):
        path = WORK / "report-echo.txt"
        rep = Report(path, REPO_ROOT, echo=True)
        buffer = io.StringIO()
        with contextlib.redirect_stdout(buffer):
            rep.record("P1", True, "ok")
        self.assertIn("P1", buffer.getvalue())


if __name__ == "__main__":
    unittest.main()
