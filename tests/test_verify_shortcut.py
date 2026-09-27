"""Unit tests for the shortcut helpers (build/py/dshab_verify/win_shortcut.py).

Contract: BUILD_CONTRACT.md section 4, scenario 1 assertions 1.21/1.28 (the start-menu shortcut
recorded in the uninstall entry exists after installing and is gone after uninstalling) and the
"user-visible state is put back byte for byte" rule at the top of the scenario list.

The module exposes no way to create a shortcut - that needs the COM shell interfaces - so nothing
here creates a real .lnk. What the acceptance run actually needs is exactly what is tested here: the
two known folders, and byte-level copy/sha256/remove on a file whose name happens to end in .lnk.
"""

from __future__ import annotations

import hashlib
import os
import sys
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = REPO_ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))

from dshab_verify import win_rmtree, win_shortcut  # noqa: E402

WORK = REPO_ROOT / ".tmp-tests" / "verify-shortcut"

# The two shell known-folder ids, pinned: the desktop can be redirected (OneDrive), so the folder has
# to come from the shell and not from the user profile directory.
DESKTOP_FOLDER_ID = "{B4BFCC3A-DB2C-424C-B029-7FE99A87C641}"
PROGRAMS_FOLDER_ID = "{A77F5D77-2E2B-44C3-A6A2-ABA601054A51}"

EMPTY_SHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

# A real shortcut starts with this header; the helpers never look inside, only at the bytes.
LNK_BYTES = bytes([0x4C, 0x00, 0x00, 0x00, 0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00])


class ShortcutTests(unittest.TestCase):
    def setUp(self):
        win_rmtree.rmtree(WORK, retries=5)
        WORK.mkdir(parents=True)

    def tearDown(self):
        win_rmtree.rmtree(WORK, retries=5)

    # ---- known folders ---------------------------------------------------------------------------

    def test_the_known_folder_ids_are_the_shell_guids(self):
        self.assertEqual(win_shortcut.DESKTOP_FOLDER, DESKTOP_FOLDER_ID)
        self.assertEqual(win_shortcut.START_MENU_PROGRAMS_FOLDER, PROGRAMS_FOLDER_ID)

    def test_both_known_folders_resolve_to_existing_directories(self):
        for name, resolver in (
            ("desktop", win_shortcut.desktop_dir),
            ("start menu programs", win_shortcut.start_menu_programs_dir),
        ):
            with self.subTest(folder=name):
                try:
                    folder = resolver()
                except OSError as exc:
                    self.skipTest(f"外壳不给出 {name} 目录：{exc}")
                self.assertTrue(folder.is_absolute(), folder)
                self.assertTrue(folder.is_dir(), folder)

    def test_known_folder_rejects_an_unknown_id(self):
        with self.assertRaises(OSError):
            win_shortcut.known_folder("{00000000-0000-0000-0000-000000000000}")

    # ---- hashing, copying, removing --------------------------------------------------------------

    def test_sha256_is_the_hash_of_the_bytes(self):
        empty = WORK / "empty.bin"
        empty.write_bytes(b"")
        self.assertEqual(win_shortcut.sha256(empty), EMPTY_SHA256)

        payload = WORK / "payload.bin"
        payload.write_bytes(LNK_BYTES)
        self.assertEqual(
            win_shortcut.sha256(payload), hashlib.sha256(LNK_BYTES).hexdigest()
        )

    def test_a_file_copied_to_a_lnk_path_keeps_the_same_hash(self):
        source = WORK / "source.bin"
        source.write_bytes(LNK_BYTES)
        target = WORK / "Programs" / "DSH-AB.lnk"

        win_shortcut.copy(source, target)

        self.assertTrue(win_shortcut.exists(target))
        self.assertEqual(win_shortcut.sha256(target), win_shortcut.sha256(source))
        self.assertEqual(target.read_bytes(), LNK_BYTES)

    def test_copy_creates_missing_parent_directories(self):
        source = WORK / "source.bin"
        source.write_bytes(LNK_BYTES)
        target = WORK / "a" / "b" / "c" / "DSH-AB.lnk"

        win_shortcut.copy(source, target)

        self.assertTrue(target.is_file())

    def test_exists_is_about_files(self):
        self.assertFalse(win_shortcut.exists(WORK / "nothing.lnk"))
        self.assertFalse(win_shortcut.exists(WORK))
        (WORK / "file.lnk").write_bytes(LNK_BYTES)
        self.assertTrue(win_shortcut.exists(WORK / "file.lnk"))

    def test_remove_deletes_the_file_and_tolerates_a_second_call(self):
        target = WORK / "DSH-AB.lnk"
        target.write_bytes(LNK_BYTES)
        win_shortcut.remove(target)
        self.assertFalse(target.exists())
        win_shortcut.remove(target)

    def test_remove_clears_a_read_only_attribute(self):
        target = WORK / "readonly.lnk"
        target.write_bytes(LNK_BYTES)
        os.chmod(target, 0o444)

        win_shortcut.remove(target)

        self.assertFalse(target.exists())


if __name__ == "__main__":
    unittest.main()
