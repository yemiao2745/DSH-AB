"""sha256 of a file, read in chunks, so a 150 MB artifact never has to fit in memory."""

import hashlib


def sha256_file(path):
    with open(path, "rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()
