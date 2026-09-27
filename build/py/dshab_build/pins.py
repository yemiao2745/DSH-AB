"""Read and write build/payload-sources.json - the single source of the input
versions (node, MinGit, dsh) and of their expected sha256 values."""

import json

from . import paths, steplog


def read(path=None):
    path = path or paths.PIN_FILE
    if not path.exists():
        return {}
    return json.loads(path.read_text(encoding="utf-8"))


def write(data, path=None):
    r"""Write the pins back, LF on every platform.

    This file is committed and no .gitattributes normalises it, so text mode would rewrite
    every line as CRLF the first time remember() runs on Windows - a whole-file byte change
    (and a changed CI cache key) that says nothing. newline="\n" pins the terminator; the
    encoding stays UTF-8 without BOM."""
    path = path or paths.PIN_FILE
    path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8",
                    newline="\n")


def entry(kind, key, path=None):
    """The pin record for one version, or None."""
    return read(path).get(kind, {}).get(key)


def remember(kind, key, path=None, **fields):
    """Record (or update) the pin for one version and write the file back."""
    data = read(path)
    records = data.setdefault(kind, {})
    record = records.setdefault(key, {})
    record.update({name: value for name, value in fields.items() if value is not None})
    write(data, path)


def sole_version(kind, path=None):
    """The one pinned version of an input, used as the command-line default."""
    keys = list(read(path).get(kind, {}))
    if len(keys) == 1:
        return keys[0]
    if not keys:
        steplog.fail("build/payload-sources.json has no %s pin; pass the version explicitly" % kind)
    steplog.fail("build/payload-sources.json pins %d %s versions (%s); pass the version explicitly"
                 % (len(keys), kind, ", ".join(keys)))
