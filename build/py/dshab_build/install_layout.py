r"""The one implementation of "which files belong in an installation root".

The payload directory structure IS the installation root structure (installer\dsh-ab.iss
[Files] mirrors it 1:1), with a single exception: payload-manifest.json is the build record
and is never installed. Three callers need the same rule - the payload manifest records it,
the update pack is checked against it, and the installer verification compares the installed
tree with it - so it lives here once."""

import fnmatch
import os
from pathlib import Path

from . import paths


def install_root_files(root, ignore=()):
    """Relative paths (backslash separated, as Windows and the .iss write them) of every
    file below root, without the build record and without anything an ignore pattern
    matches. Patterns are the caller's own globs, e.g. 'slot-*', 'docs\\*'."""
    root = Path(root)
    if not root.is_dir():
        raise FileNotFoundError("not a directory: %s" % root)
    never_installed = root / paths.MANIFEST_NAME
    found = []
    for dirpath, _dirnames, filenames in os.walk(root):
        for name in filenames:
            full = Path(dirpath) / name
            if full == never_installed:
                continue
            rel = str(full.relative_to(root))
            if any(fnmatch.fnmatch(rel, pattern) for pattern in ignore):
                continue
            found.append(rel)
    return sorted(found, key=lambda rel: (rel.lower(), rel))


def compare_file_sets(expected, actual):
    """{missing, unexpected}; Windows paths are case-insensitive, so the comparison is too."""
    expected_set = {rel.casefold() for rel in expected}
    actual_set = {rel.casefold() for rel in actual}
    return {
        "missing": [rel for rel in expected if rel.casefold() not in actual_set],
        "unexpected": [rel for rel in actual if rel.casefold() not in expected_set],
    }


def format_diff(entries, limit=5):
    head = list(entries[:limit])
    line = "; ".join(head)
    if len(entries) > len(head):
        line += "; and %d more" % (len(entries) - len(head))
    return line
