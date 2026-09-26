"""Unit tests for the recursive delete (build/py/dshab_verify/win_rmtree.py).

Contract: BUILD_CONTRACT.md section 4 - every scenario starts from "clear the target directory and
assert it really is empty". The previous implementation fell back to the command interpreter when a
delete failed; this one has to do it with the standard library and the extended-length path prefix,
and must never report success while something is left behind.
"""

from __future__ import annotations

import os
import sys
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = REPO_ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))

from dshab_verify import win_rmtree  # noqa: E402

WORK = REPO_ROOT / ".tmp-tests" / "verify-rmtree"


class RmtreeTests(unittest.TestCase):
    def setUp(self):
        win_rmtree.rmtree(WORK, retries=5)
        WORK.mkdir(parents=True)
        self.open_handles = []

    def tearDown(self):
        for handle in self.open_handles:
            handle.close()
        win_rmtree.rmtree(WORK, retries=5)

    def test_removes_a_nested_tree_including_a_read_only_file(self):
        tree = WORK / "tree"
        (tree / "sub").mkdir(parents=True)
        read_only = tree / "sub" / "readonly.txt"
        read_only.write_text("locked down", encoding="ascii")
        os.chmod(read_only, 0o444)
        (tree / "top.txt").write_text("x", encoding="ascii")

        win_rmtree.rmtree(tree)

        self.assertFalse(tree.exists())

    def test_removes_a_path_longer_than_the_classic_limit(self):
        deep = WORK / ("d" * 40)
        for _ in range(6):
            deep = deep / ("n" * 40)
        self.assertGreater(len(str(deep)), 260, "这条用例需要一条超过 260 字符的路径")
        os.makedirs(win_rmtree.long_path(deep))
        payload = deep / "file.txt"
        with open(win_rmtree.long_path(payload), "w", encoding="ascii") as handle:
            handle.write("deep")
        self.assertTrue(Path(win_rmtree.long_path(payload)).is_file())

        win_rmtree.rmtree(WORK / ("d" * 40))

        self.assertFalse(os.path.exists(win_rmtree.long_path(deep)))

    def test_long_path_prefixes_an_absolute_path_exactly_once(self):
        prefixed = win_rmtree.long_path(WORK)
        self.assertTrue(prefixed.startswith("\\\\?\\"), prefixed)
        self.assertEqual(win_rmtree.long_path(prefixed), prefixed)
        self.assertTrue(prefixed.endswith(str(WORK)))

    def test_removing_a_missing_path_is_a_noop(self):
        win_rmtree.rmtree(WORK / "never-existed")

    def test_clear_dir_empties_a_dirty_directory(self):
        dirty = WORK / "dirty"
        (dirty / "keep").mkdir(parents=True)
        (dirty / "keep" / "file.txt").write_text("x", encoding="ascii")

        win_rmtree.clear_dir(dirty)

        self.assertFalse(dirty.exists())

    def test_clear_and_assert_empty_leaves_an_empty_directory(self):
        target = WORK / "target"
        (target / "old").mkdir(parents=True)
        (target / "old" / "stale.txt").write_text("x", encoding="ascii")

        win_rmtree.clear_and_assert_empty(target)

        self.assertTrue(target.is_dir())
        self.assertEqual(list(target.iterdir()), [])

    def test_clear_and_assert_empty_fails_while_a_file_cannot_be_deleted(self):
        target = WORK / "locked"
        nested = target / "nested"
        nested.mkdir(parents=True)
        held = nested / "held.bin"
        held.write_text("still open", encoding="ascii")
        # An open handle without delete sharing is what a running install leaves behind.
        self.open_handles.append(open(held, "rb"))

        with self.assertRaises(OSError) as caught:
            win_rmtree.clear_and_assert_empty(target)

        self.assertIn("没清干净", str(caught.exception))
        self.assertTrue(held.exists(), "报错时不能假装删掉了")

    def test_rmtree_reports_a_directory_it_could_not_empty(self):
        target = WORK / "locked-rmtree"
        target.mkdir()
        held = target / "held.bin"
        held.write_text("still open", encoding="ascii")
        self.open_handles.append(open(held, "rb"))

        with self.assertRaises(OSError) as caught:
            win_rmtree.rmtree(target)

        self.assertIn("没清干净", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
