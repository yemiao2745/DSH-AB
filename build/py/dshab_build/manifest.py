"""The payload manifest: the build record the installer verification reads back.

Only what has a reader: the two versions that name the artifact, the hash of the exe that
went into the payload, and the file list that becomes the installation."""

import json
from pathlib import Path

from . import paths


def path_of(payload_dir):
    return Path(payload_dir) / paths.MANIFEST_NAME


def write(payload_dir, fields):
    target = path_of(payload_dir)
    target.parent.mkdir(parents=True, exist_ok=True)
    # newline="\n" for the same reason as the pins: byte-for-byte the same file on every
    # platform, instead of a CRLF rewrite on Windows.
    target.write_text(json.dumps(fields, indent=2, ensure_ascii=False) + "\n", encoding="utf-8",
                      newline="\n")
    return target
