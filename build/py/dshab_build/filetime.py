"""One fixed instant for every payload and pack file, and the relative-path gate.

ISCC and every zip writer record the source timestamps, and the payload is rewritten on
every run, so without normalization two builds of the same commit produce byte-different
artifacts. 2000-01-01T00:00:00Z is after 1980 (the DOS epoch a zip stores) and before any
real build."""

import os
from datetime import datetime, timezone
from pathlib import Path

from . import steplog

FIXED_TIME = datetime(2000, 1, 1, 0, 0, 0, tzinfo=timezone.utc)
FIXED_STAMP = FIXED_TIME.timestamp()
MAX_RELATIVE_PATH = 180


def normalize(root):
    """Set one fixed mtime on every file, every directory and the root itself.
    Deepest first: touching a directory again would only mtime it again."""
    root = Path(root)
    for dirpath, dirnames, filenames in os.walk(root, topdown=False):
        for name in filenames:
            os.utime(os.path.join(dirpath, name), (FIXED_STAMP, FIXED_STAMP))
        for name in dirnames:
            os.utime(os.path.join(dirpath, name), (FIXED_STAMP, FIXED_STAMP))
    os.utime(root, (FIXED_STAMP, FIXED_STAMP))


def worst_relative_path(root):
    """(length, relative path) of the longest file path below root."""
    root = Path(root)
    prefix = len(str(root)) + 1
    worst = (0, "")
    for dirpath, _dirnames, filenames in os.walk(root):
        for name in filenames:
            full = os.path.join(dirpath, name)
            length = len(full) - prefix
            if length > worst[0]:
                worst = (length, full[prefix:])
    return worst


def assert_relative_paths(root, limit=MAX_RELATIVE_PATH):
    """The installer compiles these paths, and ISCC cannot read a source path beyond
    MAX_PATH, so every payload-relative path has to stay short."""
    length, rel = worst_relative_path(root)
    if length > limit:
        steplog.fail("payload path is %d characters, over the %d limit - the installer "
                     "could not compile it: %s" % (length, limit, rel))
    return length, rel
