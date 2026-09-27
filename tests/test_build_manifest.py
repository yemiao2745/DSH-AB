"""The payload build record (dshab_build/manifest.py): where it lives, the four
keys it holds, and that it round-trips as UTF-8 without a BOM."""

import json
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
        self.dir = Path(tempfile.mkdtemp(prefix="manifest-", dir=str(scratch)))
        self.addCleanup(shutil.rmtree, str(self.dir), ignore_errors=True)

from dshab_build import manifest, paths

FIELDS = {
    "dshab_version": "0.0.1",
    "dsh_version": "0.1.7-rc.2",
    "exe_sha256": "a" * 64,
    "install_files": ["DSH_AB.exe", "README.md"],
}


def read_record(payload):
    """The record as written: the manifest file text through json.loads."""
    return json.loads(manifest.path_of(payload).read_text(encoding="utf-8"))


class ManifestTest(ScratchTest):
    def test_the_record_lives_in_the_payload_root_as_payload_manifest_json(self):
        payload = self.dir / "payload"
        self.assertEqual(manifest.path_of(payload), payload / paths.MANIFEST_NAME)
        self.assertEqual(manifest.path_of(payload).name, "payload-manifest.json")

    def test_write_then_read_gives_back_the_same_record(self):
        payload = self.dir / "payload"
        manifest.write(payload, FIELDS)
        self.assertEqual(read_record(payload), FIELDS)

    def test_the_record_has_exactly_the_four_documented_keys(self):
        payload = self.dir / "payload"
        manifest.write(payload, FIELDS)
        self.assertEqual(set(read_record(payload)),
                         {"dshab_version", "dsh_version", "exe_sha256", "install_files"})

    def test_write_creates_a_missing_payload_directory(self):
        payload = self.dir / "payload"
        target = manifest.write(payload, FIELDS)
        self.assertTrue(target.is_file())
        self.assertEqual(target.parent, payload)

    def test_write_returns_the_file_it_wrote(self):
        payload = self.dir / "payload"
        self.assertEqual(manifest.write(payload, FIELDS), manifest.path_of(payload))

    def test_the_file_is_utf8_without_a_bom_and_ends_with_a_newline(self):
        payload = self.dir / "payload"
        target = manifest.write(payload, FIELDS)
        raw = target.read_bytes()
        self.assertFalse(raw.startswith(b"\xef\xbb\xbf"))
        self.assertTrue(raw.endswith(b"\n"))

    def test_non_ascii_values_stay_readable_instead_of_being_escaped(self):
        payload = self.dir / "payload"
        fields = dict(FIELDS, dsh_version="\u4e2d\u6587")
        target = manifest.write(payload, fields)
        self.assertIn("\u4e2d\u6587".encode("utf-8"), target.read_bytes())
        self.assertEqual(read_record(payload)["dsh_version"], "\u4e2d\u6587")

    def test_a_program_only_payload_records_an_empty_dsh_version(self):
        payload = self.dir / "payload"
        manifest.write(payload, dict(FIELDS, dsh_version=""))
        self.assertEqual(read_record(payload)["dsh_version"], "")
