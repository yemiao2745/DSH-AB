r"""Unit tests for the HKCU uninstall-entry helpers (build/py/dshab_verify/win_reg.py).

Contract: BUILD_CONTRACT.md section 4, scenario 1 assertions 1.16-1.21 and scenario 2 assertion 2.7 -
the entry of one installation is {6F1D2A74-3B58-4C9E-8A17-5E0C4D3B9A62}_<tag>_is1 under
HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall, and the tag is twelve hex characters of the
SHA-256 of the lowercased install root encoded as UTF-16LE. The previous generation's entry
{8F3A1C42-...}_is1 is only ever snapshotted and restored, never asserted on.

Every test that writes uses a throwaway key below HKCU\Software\DSH-AB-tests\<pid> which tearDown
deletes; no test touches a real uninstall entry. A machine that refuses to write to HKCU (restricted
session, policy, sandbox) makes those tests skip instead of fail.
"""

from __future__ import annotations

import os
import sys
import unittest
import winreg
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = REPO_ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))

from dshab_verify import win_reg  # noqa: E402

UNINSTALL_SUBKEY = r"Software\Microsoft\Windows\CurrentVersion\Uninstall"

# Pinned literals: the AppIds are what the installer writes and what the previous acceptance script
# looked up, so they must not drift.
INSTALL_APP_ID = "{6F1D2A74-3B58-4C9E-8A17-5E0C4D3B9A62}"
LEGACY_APP_ID = "{8F3A1C42-6D7E-4B9A-9E15-2C4F7A0B6D31}"

# A fixed root makes the tag a literal: it is the value the Go runtime's installTag test pins for the
# same root, and the installer's InstallTag derives the same twelve characters.
KNOWN_ROOT = r"D:\test\DSH-AB"
KNOWN_TAG = "6e34a04e1de4"

THROWAWAY_PARENT = r"HKCU\Software\DSH-AB-tests\%d" % os.getpid()


class UninstallEntryTests(unittest.TestCase):
    def setUp(self):
        self.created = []

    def tearDown(self):
        for key_path in (THROWAWAY_PARENT,) + tuple(self.created):
            try:
                win_reg.delete_tree(key_path)
            except OSError:
                pass

    def make_throwaway_key(self, label):
        """Create (and remember) a key nobody but this test can reach; skip when HKCU is read-only."""
        sub = r"Software\DSH-AB-tests\%d\%s" % (os.getpid(), label)
        try:
            with winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, sub, 0, winreg.KEY_WRITE):
                pass
        except OSError as exc:
            raise unittest.SkipTest(f"HKCU 不可写，跳过注册表用例（{sub}：{exc}）")
        key_path = "HKCU\\" + sub
        self.created.append(key_path)
        return key_path

    def set_value(self, key_path, name, kind, data):
        _, sub = win_reg.split_path(key_path)
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, sub, 0, winreg.KEY_SET_VALUE) as key:
            winreg.SetValueEx(key, name, 0, kind, data)

    # ---- the derived key name and the tag -------------------------------------------------------

    def test_the_tag_of_the_known_root_is_the_pinned_literal(self):
        self.assertEqual(win_reg.install_tag(KNOWN_ROOT), KNOWN_TAG)

    def test_the_tag_ignores_case_and_trailing_separators_of_a_normal_directory(self):
        for spelling in (
            KNOWN_ROOT.lower(),
            KNOWN_ROOT.upper(),
            KNOWN_ROOT + "\\",
            KNOWN_ROOT + "\\\\",
        ):
            with self.subTest(spelling=spelling):
                self.assertEqual(win_reg.install_tag(spelling), KNOWN_TAG)

    def test_install_entry_key_has_the_contract_shape(self):
        expected = (
            "HKCU\\" + UNINSTALL_SUBKEY + "\\" + INSTALL_APP_ID + "_" + KNOWN_TAG + "_is1"
        )
        self.assertEqual(win_reg.install_entry_key(KNOWN_ROOT), expected)

    def test_install_entry_key_follows_a_custom_subkey(self):
        key = win_reg.install_entry_key(KNOWN_ROOT, subkey=r"Software\DSH-AB-tests\Elsewhere")
        self.assertTrue(key.startswith("HKCU\\Software\\DSH-AB-tests\\Elsewhere\\"))
        self.assertTrue(key.endswith(INSTALL_APP_ID + "_" + KNOWN_TAG + "_is1"), key)

    def test_user_entry_key_is_the_previous_generation_entry(self):
        self.assertEqual(
            win_reg.user_entry_key(),
            "HKCU\\" + UNINSTALL_SUBKEY + "\\" + LEGACY_APP_ID + "_is1",
        )

    def test_the_user_entry_is_never_an_install_entry(self):
        self.assertNotEqual(win_reg.user_entry_key(), win_reg.install_entry_key(KNOWN_ROOT))
        self.assertNotIn(INSTALL_APP_ID, win_reg.user_entry_key())

    def test_split_path_accepts_both_hives_and_rejects_anything_else(self):
        self.assertEqual(win_reg.split_path(r"HKCU\Software"), ("HKCU", "Software"))
        self.assertEqual(win_reg.split_path(r"HKLM\Software"), ("HKLM", "Software"))
        for bad in ("HKCU", r"HKCR\Software", r"Software\Thing", ""):
            with self.subTest(path=bad):
                with self.assertRaises(ValueError):
                    win_reg.split_path(bad)

    # ---- reading ---------------------------------------------------------------------------------

    def test_reading_a_missing_key_yields_none(self):
        missing = r"HKCU\Software\DSH-AB-tests\%d\not-there" % os.getpid()
        self.assertIsNone(win_reg.read_values(missing))
        self.assertIsNone(win_reg.read_value(missing, "DisplayName"))
        self.assertFalse(win_reg.key_exists(missing))
        self.assertIsNone(win_reg.snapshot(missing))

    def test_the_real_uninstall_list_is_readable(self):
        # Read-only on purpose: the acceptance run has to read the real list to find the other
        # installations. Nothing here asserts on a particular entry's contents.
        values = win_reg.read_values("HKCU\\" + UNINSTALL_SUBKEY)
        self.assertIsInstance(values, dict)

    # ---- snapshot / restore ----------------------------------------------------------------------

    def test_snapshot_and_restore_round_trip(self):
        key_path = self.make_throwaway_key("round-trip")
        self.set_value(key_path, "DisplayName", winreg.REG_SZ, "DSH-AB (verify)")
        self.set_value(key_path, "EstimatedSize", winreg.REG_DWORD, 1234)
        saved = win_reg.snapshot(key_path)
        self.assertEqual(saved["DisplayName"], (winreg.REG_SZ, "DSH-AB (verify)"))
        self.assertEqual(saved["EstimatedSize"], (winreg.REG_DWORD, 1234))

        # Whatever the acceptance run does to the entry afterwards must be undone exactly.
        self.set_value(key_path, "DisplayName", winreg.REG_SZ, "changed by the run")
        self.set_value(key_path, "UninstallString", winreg.REG_SZ, "added by the run")
        win_reg.restore(key_path, saved)

        self.assertEqual(win_reg.read_values(key_path), saved)
        self.assertTrue(win_reg.key_exists(key_path))
        self.assertIsNone(win_reg.read_value(key_path, "UninstallString"))

    def test_restore_creates_a_key_that_did_not_exist(self):
        self.make_throwaway_key("writable-probe")  # skip when HKCU cannot be written at all
        key_path = "HKCU\\" + r"Software\DSH-AB-tests\%d\was-absent" % os.getpid()
        self.created.append(key_path)
        self.assertFalse(win_reg.key_exists(key_path))
        win_reg.restore(key_path, {"DisplayName": (winreg.REG_SZ, "DSH-AB (verify)")})
        self.assertTrue(win_reg.key_exists(key_path))
        self.assertEqual(win_reg.read_value(key_path, "DisplayName"), "DSH-AB (verify)")

    def test_restore_of_none_deletes_the_key(self):
        key_path = self.make_throwaway_key("goes-away")
        self.set_value(key_path, "DisplayName", winreg.REG_SZ, "DSH-AB (verify)")
        self.assertTrue(win_reg.key_exists(key_path))
        win_reg.restore(key_path, None)
        self.assertFalse(win_reg.key_exists(key_path))

    def test_delete_tree_removes_children_too(self):
        key_path = self.make_throwaway_key("parent")
        child = key_path + "\\Child"
        _, sub = win_reg.split_path(child)
        with winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, sub, 0, winreg.KEY_WRITE):
            pass
        self.assertTrue(win_reg.key_exists(child))
        win_reg.delete_tree(key_path)
        self.assertFalse(win_reg.key_exists(key_path))
        self.assertFalse(win_reg.key_exists(child))

    def test_delete_tree_of_a_missing_key_is_a_noop(self):
        win_reg.delete_tree(r"HKCU\Software\DSH-AB-tests\%d\never-existed" % os.getpid())


if __name__ == "__main__":
    unittest.main()
