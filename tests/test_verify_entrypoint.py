"""Unit tests for the acceptance entry point (build/py/verify.py).

Contract: BUILD_CONTRACT.md section 4 - the preconditions (a payload manifest with a non-empty
install_files, a setup whose name is DSH-AB-<dshab version>-<dsh version>-setup.exe, an existing
installer script) and the parameter defaults the previous acceptance script documented
(production port 3190, 900 s per install/uninstall run, 300 s for the two runs that must be
refused). Everything here resolves against a fixture repository tree; no installer is ever run.
"""

from __future__ import annotations

import contextlib
import io
import json
import sys
import unittest
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = REPO_ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))

import verify  # noqa: E402

from dshab_verify import win_rmtree  # noqa: E402
from dshab_verify.report import Report  # noqa: E402
from dshab_verify.scenarios import Config, VerifyError  # noqa: E402

WORK = REPO_ROOT / ".tmp-tests" / "verify-entrypoint"

MANIFEST = {
    "dshab_version": "1.0",
    "dsh_version": "2.0",
    "install_files": ["DSH_AB.exe", r"docs\PLUGINS.md"],
    "exe_sha256": "0" * 64,
}
EXPECTED_SETUP_NAME = "DSH-AB-1.0-2.0-setup.exe"


def make_repo(root, manifest=MANIFEST, iss=True, setup_name=None):
    (root / "payload").mkdir(parents=True, exist_ok=True)
    if manifest is not None:
        (root / "payload" / "payload-manifest.json").write_text(
            json.dumps(manifest), encoding="utf-8"
        )
    if iss:
        (root / "installer").mkdir(parents=True, exist_ok=True)
        (root / "installer" / "dsh-ab.iss").write_text("[Setup]\n", encoding="utf-8")
    if setup_name:
        (root / "dist").mkdir(parents=True, exist_ok=True)
        (root / "dist" / setup_name).write_bytes(b"stub")
    return root


def parsed_args(**overrides):
    args = verify._parse_args([])
    for name, value in overrides.items():
        setattr(args, name, value)
    return args


class PreconditionTests(unittest.TestCase):
    def setUp(self):
        win_rmtree.rmtree(WORK, retries=5)
        WORK.mkdir(parents=True)

    def tearDown(self):
        win_rmtree.rmtree(WORK, retries=5)

    # ---- defaults ---------------------------------------------------------------------------------

    def test_the_parameters_match_the_contract_defaults(self):
        args = verify._parse_args([])
        self.assertEqual(args.port, 3190)
        self.assertEqual(args.timeout_sec, 900)
        self.assertEqual(args.refuse_sec, 300)
        self.assertIsNone(args.dir)
        self.assertIsNone(args.setup)
        self.assertIsNone(args.iss)
        self.assertIsNone(args.report)

    # ---- preconditions ----------------------------------------------------------------------------

    def test_a_missing_manifest_fails_before_anything_else(self):
        repo = make_repo(WORK / "no-manifest", manifest=None, setup_name=EXPECTED_SETUP_NAME)
        with self.assertRaises(VerifyError) as caught:
            verify._resolve(parsed_args(), repo)
        self.assertIn("找不到 payload 清单", str(caught.exception))

    def test_a_manifest_without_install_files_fails(self):
        repo = make_repo(WORK / "old-manifest", manifest={"dshab_version": "1.0"}, iss=True)
        with self.assertRaises(VerifyError) as caught:
            verify._resolve(parsed_args(), repo)
        self.assertIn("install_files", str(caught.exception))

    def test_a_missing_installer_script_fails(self):
        repo = make_repo(WORK / "no-iss", iss=False, setup_name=EXPECTED_SETUP_NAME)
        with self.assertRaises(VerifyError) as caught:
            verify._resolve(parsed_args(), repo)
        self.assertIn("找不到安装器脚本", str(caught.exception))

    def test_the_artifact_name_has_to_match_the_manifest(self):
        repo = make_repo(WORK / "wrong-name", setup_name="DSH-AB-9.9-9.9-setup.exe")
        args = parsed_args(setup=str(repo / "dist" / "DSH-AB-9.9-9.9-setup.exe"))
        with self.assertRaises(VerifyError) as caught:
            verify._resolve(args, repo)
        message = str(caught.exception)
        self.assertIn("产物名不符合", message)
        self.assertIn(EXPECTED_SETUP_NAME, message)

    def test_a_missing_setup_in_dist_fails(self):
        repo = make_repo(WORK / "empty-dist")
        with self.assertRaises(VerifyError) as caught:
            verify._resolve(parsed_args(), repo)
        self.assertIn("找不到安装程序", str(caught.exception))
        self.assertIn(EXPECTED_SETUP_NAME, str(caught.exception))

    def test_a_setup_path_that_does_not_exist_fails(self):
        repo = make_repo(WORK / "absent-setup")
        args = parsed_args(setup=str(repo / "dist" / EXPECTED_SETUP_NAME))
        with self.assertRaises(VerifyError) as caught:
            verify._resolve(args, repo)
        self.assertIn("找不到安装程序", str(caught.exception))

    # ---- the resolved paths -----------------------------------------------------------------------

    def test_the_defaults_are_derived_from_the_repository_root(self):
        repo = make_repo(WORK / "good", setup_name=EXPECTED_SETUP_NAME)
        resolved = verify._resolve(parsed_args(), repo)

        self.assertEqual(resolved.expected_setup_name, EXPECTED_SETUP_NAME)
        self.assertEqual(resolved.setup, (repo / "dist" / EXPECTED_SETUP_NAME).resolve())
        self.assertEqual(resolved.iss, (repo / "installer" / "dsh-ab.iss").resolve())
        self.assertEqual(resolved.install_dir, (repo / ".tmp-verify" / "install").resolve())
        self.assertEqual(resolved.manifest_path, repo / "payload" / "payload-manifest.json")
        self.assertEqual(resolved.manifest["install_files"], MANIFEST["install_files"])

    def test_an_explicit_install_directory_wins(self):
        repo = make_repo(WORK / "explicit", setup_name=EXPECTED_SETUP_NAME)
        custom = WORK / "here"
        resolved = verify._resolve(parsed_args(dir=str(custom)), repo)
        self.assertEqual(resolved.install_dir, custom.resolve())


class MainTests(unittest.TestCase):
    def setUp(self):
        win_rmtree.rmtree(WORK, retries=5)
        WORK.mkdir(parents=True)
        self.repo = make_repo(WORK / "repo", setup_name=EXPECTED_SETUP_NAME)
        self.report = WORK / "report.txt"
        self.paths = verify.Paths(
            install_dir=WORK / "install",
            setup=(self.repo / "dist" / EXPECTED_SETUP_NAME),
            iss=(self.repo / "installer" / "dsh-ab.iss"),
            manifest=dict(MANIFEST),
            manifest_path=self.repo / "payload" / "payload-manifest.json",
            expected_setup_name=EXPECTED_SETUP_NAME,
        )

    def tearDown(self):
        win_rmtree.rmtree(WORK, retries=5)

    def run_main(self, argv):
        stdout, stderr = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            code = verify.main(argv)
        return code, stdout.getvalue(), stderr.getvalue()

    def test_a_failing_gate_stops_before_any_install_and_exits_non_zero(self):
        installs = []
        with mock.patch.object(verify, "_resolve", return_value=self.paths), mock.patch.object(
            verify.isscheck, "check", return_value=["门禁一", "门禁二"]
        ), mock.patch.object(verify.scenarios, "run", lambda *a: installs.append(a)):
            code, _stdout, stderr = self.run_main(["--report", str(self.report)])

        self.assertEqual(code, 1)
        self.assertIn("2 条源码级门禁没过", stderr)
        self.assertEqual(installs, [], "门禁没过时一个场景都不许跑")
        text = self.report.read_text(encoding="utf-8")
        self.assertIn("源码级门禁 #1", text)
        self.assertIn("源码级门禁 #2", text)

    def test_a_passing_run_hands_the_parameters_to_the_scenarios(self):
        recorded = {}

        def run(cfg, rep):
            recorded["cfg"] = cfg
            recorded["rep"] = rep

        argv = [
            "--report", str(self.report),
            "--dir", str(WORK / "install"),
            "--port", "3210",
            "--timeout-sec", "42",
            "--refuse-sec", "7",
        ]
        with mock.patch.object(verify, "_resolve", return_value=self.paths), mock.patch.object(
            verify.isscheck, "check", return_value=[]
        ), mock.patch.object(verify.scenarios, "run", run):
            code, stdout, stderr = self.run_main(argv)

        self.assertEqual(code, 0, stderr)
        self.assertIn("OK install+uninstall exit=0 dir=", stdout)
        cfg = recorded["cfg"]
        self.assertIsInstance(cfg, Config)
        self.assertEqual(cfg.repo_root, Path(verify.__file__).resolve().parents[2])
        self.assertEqual(cfg.install_dir, self.paths.install_dir)
        self.assertEqual(cfg.setup, self.paths.setup)
        self.assertEqual(cfg.manifest, self.paths.manifest)
        self.assertEqual(cfg.port, 3210)
        self.assertEqual(cfg.timeout_sec, 42)
        self.assertEqual(cfg.refuse_sec, 7)
        self.assertIsInstance(recorded["rep"], Report)

    def test_a_precondition_failure_exits_non_zero_and_runs_nothing(self):
        code, _stdout, stderr = self.run_main(
            ["--setup", str(WORK / "not-a-setup.exe"), "--report", str(self.report)]
        )

        self.assertEqual(code, 1)
        self.assertIn("report:", stderr)
        text = self.report.read_text(encoding="utf-8")
        self.assertNotIn("静默安装", text)
        self.assertNotIn("[ok  ]", text)

    def test_an_unexpected_error_still_exits_non_zero(self):
        with mock.patch.object(verify, "_resolve", side_effect=RuntimeError("boom")):
            code, _stdout, stderr = self.run_main(["--report", str(self.report)])

        self.assertEqual(code, 1)
        self.assertIn("boom", stderr)


if __name__ == "__main__":
    unittest.main()
