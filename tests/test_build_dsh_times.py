"""The release-time rule behind the first dependency resolve (dshab_build/dsh_times.py), tested on injected release tables - no network."""

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
        self.dir = Path(tempfile.mkdtemp(prefix="times-", dir=str(scratch)))
        self.addCleanup(shutil.rmtree, str(self.dir), ignore_errors=True)

from datetime import datetime, timedelta

from dshab_build import dsh_times, pins, steplog

NEAR = {
    "1.0.0": "2026-01-01T10:00:00.000Z",
    "1.0.1": "2026-01-01T10:30:00.000Z",
}
FAR = {
    "1.0.0": "2026-01-01T10:00:00.000Z",
    "1.0.1": "2026-01-05T10:00:00.000Z",
}


class BeforeInstantTest(unittest.TestCase):
    def test_the_cut_off_is_one_hour_after_the_release(self):
        self.assertEqual(dsh_times.before_instant("1.0.0", FAR), "2026-01-01T11:00:00Z")

    def test_the_cut_off_is_the_midpoint_when_the_next_release_is_closer(self):
        self.assertEqual(dsh_times.before_instant("1.0.0", NEAR), "2026-01-01T10:15:00Z")

    def test_the_next_release_is_found_by_time_not_by_the_order_of_the_table(self):
        times = {"1.0.1": NEAR["1.0.1"], "1.0.0": NEAR["1.0.0"]}
        self.assertEqual(dsh_times.before_instant("1.0.0", times), "2026-01-01T10:15:00Z")

    def test_the_newest_release_gets_the_full_hour(self):
        self.assertEqual(dsh_times.before_instant("1.0.0", {"1.0.0": NEAR["1.0.0"]}),
                         "2026-01-01T11:00:00Z")

    def test_a_release_that_is_not_the_first_uses_its_own_time(self):
        times = {"1.0.0": "2026-01-01T10:00:00Z", "1.0.1": "2026-01-01T10:30:00Z",
                 "1.0.2": "2026-01-01T10:50:00Z"}
        self.assertEqual(dsh_times.before_instant("1.0.1", times), "2026-01-01T10:40:00Z")

    def test_the_instant_is_normalised_to_utc(self):
        times = {"1.0.0": "2026-01-01T12:00:00+02:00", "1.0.1": "2026-01-09T12:00:00+02:00"}
        self.assertEqual(dsh_times.before_instant("1.0.0", times), "2026-01-01T11:00:00Z")

    def test_the_instant_is_always_written_as_utc_seconds(self):
        stamp = dsh_times.before_instant("1.0.0", FAR)
        self.assertEqual(datetime.strptime(stamp, "%Y-%m-%dT%H:%M:%SZ").year, 2026)


class PublishedTest(unittest.TestCase):
    def test_the_published_instant_is_the_registry_stamp(self):
        self.assertEqual(dsh_times.published("1.0.0", FAR), FAR["1.0.0"])

    def test_a_version_the_registry_does_not_list_fails(self):
        with self.assertRaises(steplog.BuildError) as caught:
            dsh_times.published("9.9.9", FAR)
        self.assertIn("@deepseek-ai/dsh 9.9.9", str(caught.exception))


class PackumentUrlTest(unittest.TestCase):
    def test_the_scoped_package_is_addressed_with_an_escaped_slash(self):
        self.assertEqual(dsh_times.packument_url("https://registry.npmjs.org/"),
                         "https://registry.npmjs.org/@deepseek-ai%2Fdsh")

    def test_a_registry_without_a_trailing_slash_works_the_same(self):
        self.assertEqual(dsh_times.packument_url("https://registry.npmjs.org"),
                         dsh_times.packument_url("https://registry.npmjs.org/"))


class FromPinTest(unittest.TestCase):
    def test_the_recorded_instant_plus_one_hour(self):
        self.assertEqual(dsh_times.from_pin("2026-09-24T14:18:11.337Z"),
                         "2026-09-24T15:18:11Z")

    def test_an_instant_without_sub_second_digits_works_too(self):
        self.assertEqual(dsh_times.from_pin("2026-09-24T14:18:11Z"), "2026-09-24T15:18:11Z")

    def test_the_pinned_dsh_release_gives_a_cut_off_one_hour_after_it_was_published(self):
        _version, record = next(iter(pins.read()["dsh"].items()))
        published = record["published"]
        moment = datetime.fromisoformat(published.replace("Z", "+00:00"))
        expected = (moment.replace(microsecond=0) + timedelta(hours=1))
        self.assertEqual(dsh_times.from_pin(published),
                         expected.strftime("%Y-%m-%dT%H:%M:%SZ"))
