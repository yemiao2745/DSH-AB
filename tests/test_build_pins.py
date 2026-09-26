"""The version pins in build/payload-sources.json, as read and written by
dshab_build/pins.py: which inputs are pinned, what a record holds, and that a
read-modify-write cycle keeps the file in its canonical shape."""

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
        self.dir = Path(tempfile.mkdtemp(prefix="pins-", dir=str(scratch)))
        self.addCleanup(shutil.rmtree, str(self.dir), ignore_errors=True)

import json

from dshab_build import paths, pins, steplog

INPUTS = ("node", "mingit", "dsh")


class PinFileTest(ScratchTest):
    def test_a_missing_pin_file_reads_as_no_pins(self):
        self.assertEqual(pins.read(self.dir / "absent.json"), {})

    def test_the_pin_file_names_the_three_inputs_with_exactly_one_version_each(self):
        data = pins.read()
        self.assertEqual(set(data), set(INPUTS))
        for kind in INPUTS:
            with self.subTest(kind=kind):
                self.assertEqual(len(data[kind]), 1)

    def test_sole_version_is_the_default_the_build_uses_for_each_input(self):
        for kind in INPUTS:
            with self.subTest(kind=kind):
                self.assertEqual(pins.sole_version(kind), list(pins.read()[kind])[0])

    def test_node_and_mingit_pins_name_their_own_version_url_and_sha256(self):
        data = pins.read()
        for kind in ("node", "mingit"):
            with self.subTest(kind=kind):
                version, record = next(iter(data[kind].items()))
                self.assertTrue(record["url"].endswith(".zip"), record["url"])
                self.assertIn(version, record["url"])
                self.assertRegex(record["sha256"], r"^[0-9a-f]{64}$")

    def test_the_dsh_pin_names_its_source_registry_and_release_instant(self):
        version, record = next(iter(pins.read()["dsh"].items()))
        self.assertRegex(version, r"^\d+\.\d+\.\d+")
        self.assertEqual(record["source"], "npm:@deepseek-ai/dsh")
        self.assertTrue(record["registry"].startswith("https://"))
        self.assertRegex(record["published"],
                         r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$")

    def test_entry_is_the_record_of_one_version(self):
        version = pins.sole_version("dsh")
        record = pins.entry("dsh", version)
        self.assertEqual(record, pins.read()["dsh"][version])
        self.assertIsNone(pins.entry("dsh", "9.9.9-nope"))

    def test_sole_version_fails_when_the_input_is_not_pinned(self):
        path = self.dir / "pins.json"
        pins.write({"node": {}}, path)
        with self.assertRaises(steplog.BuildError) as caught:
            pins.sole_version("node", path)
        self.assertIn("no node pin", str(caught.exception))

    def test_sole_version_fails_when_two_versions_are_pinned(self):
        path = self.dir / "pins.json"
        pins.write({"node": {"1.0.0": {}, "2.0.0": {}}}, path)
        with self.assertRaises(steplog.BuildError) as caught:
            pins.sole_version("node", path)
        self.assertIn("2 node versions", str(caught.exception))
        self.assertIn("1.0.0, 2.0.0", str(caught.exception))

    def test_write_read_round_trip_keeps_the_shape_of_the_committed_file(self):
        # read_text() hides line endings, so this compares the JSON text itself:
        # indentation, key order, the absence of \\u escapes and the trailing newline.
        target = self.dir / "payload-sources.json"
        pins.write(pins.read(), target)
        self.assertEqual(target.read_text(encoding="utf-8"),
                         paths.PIN_FILE.read_text(encoding="utf-8"))

    def test_the_written_pin_file_is_the_committed_one_up_to_line_endings(self):
        # write_text() uses the platform line separator; the committed file uses LF.
        target = self.dir / "payload-sources.json"
        pins.write(pins.read(), target)

        def text(raw):
            return raw.replace(b"\r\n", b"\n")

        self.assertEqual(text(target.read_bytes()), text(paths.PIN_FILE.read_bytes()))

    def test_write_read_round_trip_keeps_every_record_intact(self):
        target = self.dir / "payload-sources.json"
        pins.write(pins.read(), target)
        self.assertEqual(pins.read(target), pins.read())

    def test_write_is_utf8_without_a_bom_and_ends_with_a_newline(self):
        target = self.dir / "payload-sources.json"
        pins.write(pins.read(), target)
        raw = target.read_bytes()
        self.assertFalse(raw.startswith(b"\xef\xbb\xbf"))
        self.assertTrue(raw.endswith(b"\n"))

    def test_write_keeps_non_ascii_readable_instead_of_escaping_it(self):
        target = self.dir / "pins.json"
        pins.write({"dsh": {"1.0.0": {"note": "\u4e2d\u6587"}}}, target)
        self.assertIn("\u4e2d\u6587".encode("utf-8"), target.read_bytes())
        self.assertEqual(pins.read(target)["dsh"]["1.0.0"]["note"], "\u4e2d\u6587")

    def test_remember_updates_one_field_and_keeps_the_rest_of_the_file(self):
        path = self.dir / "pins.json"
        pins.write({"node": {"1.0.0": {"url": "u", "sha256": "a" * 64}}}, path)
        pins.remember("node", "1.0.0", path, sha256="b" * 64)
        self.assertEqual(pins.read(path)["node"]["1.0.0"],
                         {"url": "u", "sha256": "b" * 64})

    def test_remember_records_a_new_input_without_touching_the_others(self):
        path = self.dir / "pins.json"
        pins.write({"node": {"1.0.0": {"url": "u"}}}, path)
        pins.remember("mingit", "2.0.0", path, url="g")
        data = pins.read(path)
        self.assertEqual(data["mingit"], {"2.0.0": {"url": "g"}})
        self.assertEqual(data["node"], {"1.0.0": {"url": "u"}})

    def test_remember_leaves_a_field_it_was_not_given(self):
        path = self.dir / "pins.json"
        pins.write({"dsh": {"1.0.0": {"source": "npm:@deepseek-ai/dsh", "published": "t"}}}, path)
        pins.remember("dsh", "1.0.0", path, published="t2", registry=None)
        self.assertEqual(pins.read(path)["dsh"]["1.0.0"],
                         {"source": "npm:@deepseek-ai/dsh", "published": "t2"})
