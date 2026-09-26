"""The one fixed instant every payload and pack file gets, and the
relative-path length gate (dshab_build/filetime.py)."""

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
        self.dir = Path(tempfile.mkdtemp(prefix="filetime-", dir=str(scratch)))
        self.addCleanup(shutil.rmtree, str(self.dir), ignore_errors=True)

import os
from datetime import datetime, timezone

from dshab_build import filetime, steplog


def mtree(root):
    """root/a.txt, root/sub/b.txt and root/sub/empty/ - files and directories alike."""
    (root / "sub" / "empty").mkdir(parents=True)
    (root / "a.txt").write_text("a", encoding="utf-8")
    (root / "sub" / "b.txt").write_text("b", encoding="utf-8")
    return root


class FixedInstantTest(ScratchTest):
    def test_the_fixed_instant_is_2000_01_01t00_00_00z(self):
        moment = datetime.fromtimestamp(filetime.FIXED_STAMP, timezone.utc)
        self.assertEqual(moment.strftime("%Y-%m-%dT%H:%M:%SZ"), "2000-01-01T00:00:00Z")

    def test_the_fixed_instant_produces_a_fixed_time_and_stamp_that_agree(self):
        self.assertEqual(filetime.FIXED_TIME.timestamp(), filetime.FIXED_STAMP)

    def test_normalize_stamps_every_file_every_directory_and_the_root(self):
        root = mtree(self.dir / "tree")
        old = 1000000000.0
        for path in [root, root / "a.txt", root / "sub", root / "sub" / "b.txt",
                     root / "sub" / "empty"]:
            os.utime(path, (old, old))

        filetime.normalize(root)

        for path in [root, root / "a.txt", root / "sub", root / "sub" / "b.txt",
                     root / "sub" / "empty"]:
            with self.subTest(path=path.name):
                self.assertEqual(os.stat(path).st_mtime, filetime.FIXED_STAMP)

    def test_normalize_leaves_the_file_contents_alone(self):
        root = mtree(self.dir / "tree")
        filetime.normalize(root)
        self.assertEqual((root / "sub" / "b.txt").read_text(encoding="utf-8"), "b")


class RelativePathGateTest(ScratchTest):
    def test_the_limit_is_180_characters(self):
        self.assertEqual(filetime.MAX_RELATIVE_PATH, 180)

    def test_the_worst_path_is_the_longest_file_path_below_the_root(self):
        root = mtree(self.dir / "tree")
        long_name = "c" * 90 + ".txt"
        (root / "sub" / long_name).write_text("c", encoding="utf-8")
        length, rel = filetime.worst_relative_path(root)
        self.assertEqual(rel, os.path.join("sub", long_name))
        self.assertEqual(length, len(rel))

    def test_the_worst_path_counts_files_only(self):
        root = self.dir / "tree"
        (root / ("d" * 120)).mkdir(parents=True)
        (root / "a.txt").write_text("a", encoding="utf-8")
        self.assertEqual(filetime.worst_relative_path(root), (5, "a.txt"))

    def test_a_path_exactly_at_the_limit_passes(self):
        root = self.dir / "tree"
        root.mkdir()
        name = "x" * 180
        (root / name).write_text("x", encoding="utf-8")
        self.assertEqual(filetime.assert_relative_paths(root), (180, name))

    def test_a_path_one_character_over_the_limit_fails_and_names_it(self):
        root = self.dir / "tree"
        root.mkdir()
        name = "y" * 181
        (root / name).write_text("y", encoding="utf-8")
        with self.assertRaises(steplog.BuildError) as caught:
            filetime.assert_relative_paths(root)
        message = str(caught.exception)
        self.assertIn("181 characters", message)
        self.assertIn("180 limit", message)
        self.assertIn(name, message)
