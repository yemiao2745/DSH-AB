r"""The release-time rule behind the first dependency resolve.

npm resolves "now" by default, which lets the ^-ranged sibling packages of a dsh release
drift to a much newer generation; that pushes whole subtrees into
node_modules\@deepseek-ai\dsh\node_modules\ and blows the 180-character path gate. The
first resolve is therefore cut off at an instant just after the dsh release itself: the
release time plus one hour, clamped to the midpoint towards the next release."""

import json
import re
import urllib.parse
from datetime import datetime, timedelta, timezone

from . import download, steplog

PACKAGE = "@deepseek-ai/dsh"
_VERSION = re.compile(r"^\d+\.\d+\.\d+")


def _parse(stamp):
    return datetime.fromisoformat(stamp.replace("Z", "+00:00")).astimezone(timezone.utc)


def packument_url(registry):
    # npm addresses a scoped package as @scope%2Fname.
    return "%s/%s" % (registry.rstrip("/"), urllib.parse.quote(PACKAGE, safe="@"))


def release_times(registry):
    """{version: published ISO stamp} straight from the registry."""
    body = download.text(packument_url(registry))
    try:
        times = json.loads(body).get("time", {})
    except ValueError as exc:
        steplog.fail("the registry answered with something that is not JSON (%s): %s"
                     % (exc, packument_url(registry)))
    return {version: stamp for version, stamp in times.items() if _VERSION.match(version)}


def published(version, times):
    stamp = times.get(version)
    if not stamp:
        steplog.fail("%s %s is not in the registry release list" % (PACKAGE, version))
    return stamp


def before_instant(version, times):
    """published + 1h, or the midpoint to the next release when that is closer."""
    published(version, times)   # a version that is not in the table fails like the rest of us
    ordered = sorted(times.items(), key=lambda item: _parse(item[1]))
    names = [name for name, _stamp in ordered]
    index = names.index(version)
    start = _parse(ordered[index][1])
    end = start + timedelta(hours=1)
    if index + 1 < len(ordered):
        middle = start + (_parse(ordered[index + 1][1]) - start) / 2
        if middle < end:
            end = middle
    return end.strftime("%Y-%m-%dT%H:%M:%SZ")


def from_pin(stamp):
    """The recorded release instant plus one hour - the offline-capable equivalent of
    before_instant() for a version whose publish time is already pinned."""
    return (_parse(stamp) + timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ")
