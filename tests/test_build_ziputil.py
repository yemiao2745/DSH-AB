"""Deterministic packing and extraction (dshab_build/ziputil.py): two packs of one
tree are byte-identical, the entry order and the entry timestamps are explicit,
and directory entries are written."""

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
        self.dir = Path(tempfile.mkdtemp(prefix="ziputil-", dir=str(scratch)))
        self.addCleanup(shutil.rmtree, str(self.dir), ignore_errors=True)

import hashlib
import os
import zipfile

from dshab_build import ziputil

# b.txt, a tree with a nested file, a file that is already sorted against the
# directory entry it lives in, a dotfile and an empty directory.
TREE = {
    "b.txt": b"b",
    ".gitignore": b"ignore",
    os.path.join("a", "x.txt"): b"x",
    os.path.join("a", "sub", "deep.txt"): b"deep",
}


def tree(root, files=None):
    for name, body in (TREE if files is None else files).items():
        target = root / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(body)
    (root / "empty").mkdir(parents=True, exist_ok=True)
    return root


def expected_entries():
    return [
        ".gitignore",
        "a/",
        "a/sub/",
        "a/sub/deep.txt",
        "a/x.txt",
        "b.txt",
        "empty/",
    ]


class PackTreeTest(ScratchTest):
    def test_two_packs_of_one_tree_are_byte_identical(self):
        root = tree(self.dir / "src")
        first = ziputil.pack_tree(root, self.dir / "one.zip")
        for dirpath, _dirnames, filenames in os.walk(root):
            for name in filenames:
                target = os.path.join(dirpath, name)
                os.utime(target, (1000000000.0, 1000000000.0))
        second = ziputil.pack_tree(root, self.dir / "two.zip")

        self.assertEqual(first.read_bytes(), second.read_bytes())
        self.assertEqual(hashlib.sha256(first.read_bytes()).hexdigest(),
                         hashlib.sha256(second.read_bytes()).hexdigest())

    def test_two_packs_of_the_same_content_in_different_directories_are_byte_identical(self):
        first = ziputil.pack_tree(tree(self.dir / "one"), self.dir / "one.zip")
        second = ziputil.pack_tree(tree(self.dir / "two"), self.dir / "two.zip")
        self.assertEqual(first.read_bytes(), second.read_bytes())

    def test_the_entry_order_is_explicit_and_sorted_case_insensitively(self):
        archive = ziputil.pack_tree(tree(self.dir / "src"), self.dir / "pack.zip")
        with zipfile.ZipFile(archive) as opened:
            self.assertEqual(opened.namelist(), expected_entries())

    def test_directories_get_their_own_entry_including_an_empty_one(self):
        archive = ziputil.pack_tree(tree(self.dir / "src"), self.dir / "pack.zip")
        with zipfile.ZipFile(archive) as opened:
            infos = {info.filename: info for info in opened.infolist()}
        for name in ("a/", "a/sub/", "empty/"):
            with self.subTest(name=name):
                self.assertTrue(infos[name].is_dir())
        self.assertTrue(all(not infos[name].is_dir()
                            for name in (".gitignore", "a/x.txt", "b.txt")))

    def test_the_packed_root_itself_is_not_an_entry(self):
        archive = ziputil.pack_tree(tree(self.dir / "src"), self.dir / "pack.zip")
        with zipfile.ZipFile(archive) as opened:
            self.assertNotIn("./", opened.namelist())

    def test_every_entry_carries_the_fixed_zip_timestamp(self):
        archive = ziputil.pack_tree(tree(self.dir / "src"), self.dir / "pack.zip")
        with zipfile.ZipFile(archive) as opened:
            self.assertEqual(set(info.date_time for info in opened.infolist()),
                             {(2000, 1, 1, 0, 0, 0)})

    def test_the_entry_timestamp_does_not_come_from_the_file_system(self):
        root = tree(self.dir / "src")
        os.utime(root / "b.txt", (1234567890.0, 1234567890.0))
        archive = ziputil.pack_tree(root, self.dir / "pack.zip")
        with zipfile.ZipFile(archive) as opened:
            self.assertEqual(opened.getinfo("b.txt").date_time, (2000, 1, 1, 0, 0, 0))

    def test_the_pack_keeps_the_file_content(self):
        archive = ziputil.pack_tree(tree(self.dir / "src"), self.dir / "pack.zip")
        with zipfile.ZipFile(archive) as opened:
            self.assertEqual(opened.read("a/sub/deep.txt"), b"deep")
            self.assertEqual(opened.read("empty/"), b"")

    def test_packing_creates_the_output_directory_and_replaces_an_older_pack(self):
        target = self.dir / "out" / "pack.zip"
        ziputil.pack_tree(tree(self.dir / "src"), target)
        other = self.dir / "other"
        other.mkdir()
        (other / "only.txt").write_bytes(b"only")
        stale = ziputil.pack_tree(other, target)
        with zipfile.ZipFile(stale) as opened:
            self.assertEqual(opened.namelist(), ["only.txt"])


class ExtractTest(ScratchTest):
    def test_extract_replaces_the_destination_with_the_archive_contents(self):
        archive = ziputil.pack_tree(tree(self.dir / "src"), self.dir / "pack.zip")
        dest = self.dir / "out"
        dest.mkdir()
        (dest / "stale.txt").write_bytes(b"stale")

        ziputil.extract(archive, dest)

        self.assertFalse((dest / "stale.txt").exists())
        self.assertEqual((dest / "a" / "sub" / "deep.txt").read_bytes(), b"deep")
        self.assertEqual((dest / ".gitignore").read_bytes(), b"ignore")
        self.assertTrue((dest / "empty").is_dir())
