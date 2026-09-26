"""The single rule for "which files belong in an installation root" and the
case-insensitive set comparison (dshab_build/install_layout.py)."""

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
        self.dir = Path(tempfile.mkdtemp(prefix="layout-", dir=str(scratch)))
        self.addCleanup(shutil.rmtree, str(self.dir), ignore_errors=True)

import os

from dshab_build import install_layout


def join(*parts):
    return os.path.join(*parts)


def payload_tree(root):
    """A miniature payload: the build record, hidden files, a slot, the docs seeds."""
    (root / "docs").mkdir(parents=True)
    (root / "slot-a" / "app").mkdir(parents=True)
    (root / "slot-a" / "node").mkdir(parents=True)
    (root / "runtime" / "git" / "cmd").mkdir(parents=True)
    (root / "DSH_AB.exe").write_text("exe", encoding="utf-8")
    (root / "README.md").write_text("readme", encoding="utf-8")
    (root / "dsh-ab.toml").write_text("toml", encoding="utf-8")
    (root / ".gitignore").write_text("ignore", encoding="utf-8")
    (root / "docs" / "TODO.md").write_text("todo", encoding="utf-8")
    (root / "docs" / ".hidden").write_text("hidden", encoding="utf-8")
    (root / "slot-a" / "node" / "node.exe").write_text("node", encoding="utf-8")
    (root / "slot-a" / "app" / "package.json").write_text("{}", encoding="utf-8")
    (root / "runtime" / "git" / "cmd" / "git.exe").write_text("git", encoding="utf-8")
    (root / "payload-manifest.json").write_text("{}", encoding="utf-8")
    return root


class InstallRootFilesTest(ScratchTest):
    def test_the_payload_structure_becomes_the_installation_root_structure(self):
        root = payload_tree(self.dir / "payload")
        files = install_layout.install_root_files(root)
        self.assertIn(".gitignore", files)
        self.assertIn("README.md", files)
        self.assertIn("dsh-ab.toml", files)
        self.assertIn("DSH_AB.exe", files)
        self.assertIn(join("docs", "TODO.md"), files)
        self.assertIn(join("slot-a", "node", "node.exe"), files)

    def test_hidden_files_are_installed(self):
        root = payload_tree(self.dir / "payload")
        files = install_layout.install_root_files(root)
        self.assertIn(".gitignore", files)
        self.assertIn(join("docs", ".hidden"), files)

    def test_the_build_record_at_the_payload_root_is_never_installed(self):
        root = payload_tree(self.dir / "payload")
        self.assertNotIn("payload-manifest.json", install_layout.install_root_files(root))

    def test_the_build_record_is_recognised_whatever_its_case(self):
        root = payload_tree(self.dir / "payload")
        (root / "payload-manifest.json").unlink()
        (root / "Payload-Manifest.JSON").write_text("{}", encoding="utf-8")
        self.assertNotIn("Payload-Manifest.JSON", install_layout.install_root_files(root))

    def test_a_nested_file_that_shares_the_build_record_name_is_installed(self):
        root = payload_tree(self.dir / "payload")
        (root / "docs" / "payload-manifest.json").write_text("{}", encoding="utf-8")
        self.assertIn(join("docs", "payload-manifest.json"),
                      install_layout.install_root_files(root))

    def test_paths_come_back_relative_backslash_separated_and_sorted(self):
        root = payload_tree(self.dir / "payload")
        files = install_layout.install_root_files(root)
        self.assertEqual(files, sorted(files, key=lambda rel: (rel.lower(), rel)))
        for rel in files:
            with self.subTest(rel=rel):
                self.assertFalse(os.path.isabs(rel))
                self.assertNotIn(":", rel)

    def test_the_slot_ignore_pattern_drops_a_whole_slot_tree(self):
        root = payload_tree(self.dir / "payload")
        files = install_layout.install_root_files(root, ignore=("slot-*",))
        self.assertFalse([rel for rel in files if rel.startswith("slot-")])

    def test_the_update_pack_ignores_the_slots_the_user_config_and_the_docs_ledgers(self):
        root = payload_tree(self.dir / "payload")
        files = install_layout.install_root_files(
            root, ignore=("slot-*", "dsh-ab.toml", join("docs", "*")))
        self.assertIn("DSH_AB.exe", files)
        self.assertIn("README.md", files)
        self.assertIn(".gitignore", files)
        self.assertIn(join("runtime", "git", "cmd", "git.exe"), files)
        self.assertNotIn("dsh-ab.toml", files)
        self.assertFalse([rel for rel in files if rel.startswith("docs" + os.sep)])
        self.assertFalse([rel for rel in files if rel.startswith("slot-")])

    def test_an_ignore_pattern_matches_a_whole_subtree_not_only_one_level(self):
        root = payload_tree(self.dir / "payload")
        files = install_layout.install_root_files(root, ignore=("docs*",))
        self.assertFalse([rel for rel in files if rel.startswith("docs")])

    def test_a_root_that_is_not_a_directory_is_an_error(self):
        target = self.dir / "afile.txt"
        target.write_text("x", encoding="utf-8")
        with self.assertRaises(FileNotFoundError):
            install_layout.install_root_files(target)


class CompareFileSetsTest(unittest.TestCase):
    def test_two_equal_sets_have_no_difference(self):
        diff = install_layout.compare_file_sets(
            ["a.txt", join("b", "c.txt")], ["a.txt", join("b", "c.txt")])
        self.assertEqual(diff, {"missing": [], "unexpected": []})

    def test_the_comparison_ignores_case_in_both_directions(self):
        diff = install_layout.compare_file_sets(
            ["A.txt", "b" + os.sep + "C.txt"], ["a.TXT", "B" + os.sep + "c.TXT"])
        self.assertEqual(diff, {"missing": [], "unexpected": []})

    def test_a_file_the_installation_does_not_have_is_missing(self):
        diff = install_layout.compare_file_sets(["a.txt", "b.txt"], ["a.txt"])
        self.assertEqual(diff["missing"], ["b.txt"])
        self.assertEqual(diff["unexpected"], [])

    def test_a_file_the_installation_has_but_the_record_does_not_is_unexpected(self):
        diff = install_layout.compare_file_sets(["a.txt"], ["a.txt", "z.txt"])
        self.assertEqual(diff["missing"], [])
        self.assertEqual(diff["unexpected"], ["z.txt"])

    def test_both_directions_can_be_reported_at_once(self):
        diff = install_layout.compare_file_sets(["a.txt", "b.txt"], ["a.txt", "z.txt"])
        self.assertEqual(diff["missing"], ["b.txt"])
        self.assertEqual(diff["unexpected"], ["z.txt"])

    def test_format_diff_names_the_first_five_and_counts_the_rest(self):
        entries = ["f%d.txt" % index for index in range(8)]
        line = install_layout.format_diff(entries)
        self.assertIn("f0.txt", line)
        self.assertIn("f4.txt", line)
        self.assertNotIn("f5.txt", line)
        self.assertIn("and 3 more", line)

    def test_format_diff_of_a_short_list_has_no_tail(self):
        self.assertEqual(install_layout.format_diff(["a.txt", "b.txt"]), "a.txt; b.txt")
