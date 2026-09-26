"""The project paths (dshab_build/paths.py) are derived from the module's own
location and no constant is an absolute literal."""

import shutil
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))


class ScratchTest(unittest.TestCase):
    """Every test works in its own throwaway directory inside the workspace."""

    def setUp(self):
        scratch = ROOT / ".tmp-test"
        scratch.mkdir(exist_ok=True)
        self.dir = Path(tempfile.mkdtemp(prefix="paths-", dir=str(scratch)))
        self.addCleanup(shutil.rmtree, str(self.dir), ignore_errors=True)

import os
import re
from pathlib import Path

from dshab_build import paths


class RepoRootTest(unittest.TestCase):
    def test_the_repo_root_is_derived_from_this_module_location(self):
        module_dir = Path(paths.__file__).resolve().parent
        self.assertEqual(module_dir, paths.ROOT / "build" / "py" / "dshab_build")
        self.assertEqual(module_dir.parents[2], paths.ROOT)

    def test_the_repo_root_is_the_directory_the_tests_run_from(self):
        self.assertEqual(paths.ROOT, Path(__file__).resolve().parents[1])

    def test_the_repo_root_really_holds_the_project(self):
        self.assertTrue((paths.ROOT / "build" / "py" / "dshab_build" / "paths.py").is_file())
        self.assertTrue((paths.ROOT / "build" / "payload-sources.json").is_file())
        self.assertTrue((paths.ROOT / "installer" / "dsh-ab.iss").is_file())

    def test_the_intermediate_directories_are_the_package_and_the_python_home(self):
        self.assertEqual(paths.PACKAGE_DIR, paths.ROOT / "build" / "py" / "dshab_build")
        self.assertEqual(paths.PY_DIR, paths.ROOT / "build" / "py")


class PathConstantsTest(unittest.TestCase):
    def constants(self):
        return {name: value for name, value in vars(paths).items()
                if isinstance(value, Path) and not name.startswith("_")}

    def test_every_path_constant_lives_under_the_repo_root(self):
        for name, value in self.constants().items():
            with self.subTest(constant=name):
                self.assertTrue(value == paths.ROOT or paths.ROOT in value.parents,
                                "%s is outside the repo root: %s" % (name, value))

    def test_the_module_source_holds_no_absolute_path_literal(self):
        source = Path(paths.__file__).read_text(encoding="utf-8")
        self.assertIsNone(re.search(r"[A-Za-z]:" + re.escape(os.sep), source),
                          "paths.py names a drive letter")
        self.assertNotIn("/Users/", source)
        self.assertNotIn("AppData", source)

    def test_the_named_project_paths_are_the_expected_ones(self):
        self.assertEqual(paths.BUILD_DIR, paths.ROOT / "build")
        self.assertEqual(paths.SRC_DIR, paths.ROOT / "src" / "dsh-ab")
        self.assertEqual(paths.INSTALLER_DIR, paths.ROOT / "installer")
        self.assertEqual(paths.PIN_FILE, paths.ROOT / "build" / "payload-sources.json")
        self.assertEqual(paths.ISS, paths.ROOT / "installer" / "dsh-ab.iss")
        self.assertEqual(paths.TEMPLATES, paths.ROOT / "build" / "templates")

    def test_the_scratch_directories_are_inside_the_project(self):
        for name in ("SCRATCH_BUILD", "SCRATCH_TMP", "SCRATCH_GO", "SCRATCH_GOCACHE",
                     "SCRATCH_UPDATE", "SCRATCH_RSRCGEN"):
            with self.subTest(scratch=name):
                value = getattr(paths, name)
                self.assertTrue(value == paths.ROOT or paths.ROOT in value.parents)

    def test_the_manifest_name_is_the_one_file_that_is_never_installed(self):
        self.assertEqual(paths.MANIFEST_NAME, "payload-manifest.json")
