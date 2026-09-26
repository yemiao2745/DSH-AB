"""Unzip, and pack a tree into a deterministic zip.

Determinism: one fixed DOS timestamp for every entry, an explicit entry order
(case-insensitive path order) and explicit directory entries. Nothing else is read
from the file system, so two packs of the same tree are byte-identical."""

import os
import shutil
import zipfile
from pathlib import Path

EPOCH = (2000, 1, 1, 0, 0, 0)  # after 1980 (the DOS epoch), before any real build


def extract(zip_path, dest):
    """Replace dest with the contents of a zip archive."""
    dest = Path(dest)
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True)
    with zipfile.ZipFile(zip_path) as archive:
        archive.extractall(dest)
    return dest


def pack_tree(src, zip_path):
    """Write src into zip_path: sorted entry order, directory entries included."""
    src = Path(src)
    zip_path = Path(zip_path)
    entries = []
    for dirpath, _dirnames, filenames in os.walk(src):
        rel = Path(dirpath).relative_to(src)
        if rel.parts:
            entries.append((rel.as_posix() + "/", True))
        for name in filenames:
            entries.append(((rel / name).as_posix(), False))
    entries.sort(key=lambda item: (item[0].lower(), item[0]))

    zip_path.parent.mkdir(parents=True, exist_ok=True)
    if zip_path.exists():
        zip_path.unlink()
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for name, is_dir in entries:
            item = zipfile.ZipInfo(name, date_time=EPOCH)
            item.compress_type = zipfile.ZIP_DEFLATED
            item.external_attr = (0o40755 << 16 | 0x10) if is_dir else (0o644 << 16)
            if is_dir:
                archive.writestr(item, b"")
            else:
                with open(src / name, "rb") as source, archive.open(item, "w") as target:
                    shutil.copyfileobj(source, target, 1 << 20)
    return zip_path
