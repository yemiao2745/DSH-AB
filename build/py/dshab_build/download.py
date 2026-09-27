"""Fetch a URL to a verified local file: reuse a cached file whose sha256 matches,
otherwise download to '<target>.part' and rename it into place atomically.

Offline mode never touches the network: a cache entry that is missing or has the wrong
hash is a hard error, because there is nothing else to fall back to."""

import os
import shutil
import time
import urllib.request
from pathlib import Path

from . import hashutil, steplog

TRIES = 5
_USER_AGENT = "dshab-build/1.0"


def _open(url, timeout):
    request = urllib.request.Request(url, headers={"User-Agent": _USER_AGENT})
    return urllib.request.urlopen(request, timeout=timeout)


def _attempt(action, what):
    """Run a network action up to TRIES times with a growing backoff."""
    for attempt in range(1, TRIES + 1):
        try:
            return action()
        except Exception as exc:  # noqa: BLE001 - any transport failure is retryable
            if attempt == TRIES:
                steplog.fail("%s failed after %d attempts: %s" % (what, TRIES, exc))
            steplog.warn("%s failed (%s), retrying in %ds" % (what, exc, 5 * attempt))
            time.sleep(5 * attempt)


def download_raw(url, target, timeout=1800):
    """Download url to target (through '<target>.part'), with retries."""
    target = Path(target)
    part = Path(str(target) + ".part")
    target.parent.mkdir(parents=True, exist_ok=True)

    def once():
        if part.exists():
            part.unlink()
        steplog.info("downloading " + url)
        with _open(url, timeout) as response, open(part, "wb") as handle:
            shutil.copyfileobj(response, handle, 1 << 20)
        os.replace(part, target)

    def guarded():
        try:
            once()
        except Exception:
            if part.exists():
                part.unlink()
            raise

    _attempt(guarded, "download " + url)
    return target


def text(url, timeout=300):
    """Fetch a text resource (e.g. SHASUMS256.txt) with retries."""

    def once():
        with _open(url, timeout) as response:
            return response.read().decode("utf-8", "replace")

    return _attempt(once, "fetch " + url)


def fetch(url, target, sha256, what, offline):
    """Return a local file with the pinned sha256, downloading it if necessary."""
    target = Path(target)
    wanted = sha256.lower()
    if target.exists():
        have = hashutil.sha256_file(target)
        if have == wanted:
            steplog.info("cached %s (%s)" % (what, have))
            return target
        steplog.info("%s cache hash mismatch (pinned %s, actual %s); downloading again"
                     % (what, wanted, have))
        target.unlink()
    if offline:
        steplog.fail("--offline was given but the cache has no usable %s: %s" % (what, target))
    download_raw(url, target)
    got = hashutil.sha256_file(target)
    if got != wanted:
        target.unlink()
        steplog.fail("%s does not match the pin (pinned %s, actual %s): %s"
                     % (what, wanted, got, url))
    steplog.info("verified %s (%s)" % (what, got))
    return target
