"""The bundled Node.js runtime: fetch it against the publisher's own checksum, unzip it
into the slot, and prove the extracted binary is the requested release."""

from pathlib import Path

from . import download, pins, steplog, ziputil

SHASUMS_URL = "https://nodejs.org/dist/v%(version)s/SHASUMS256.txt"


def zip_name(version):
    return "node-v%s-win-x64.zip" % version


def url(version):
    return "https://nodejs.org/dist/v%s/%s" % (version, zip_name(version))


def publisher_sha256(version):
    """The hash the publisher publishes for this asset, from SHASUMS256.txt."""
    wanted = zip_name(version)
    listing = download.text(SHASUMS_URL % {"version": version})
    for line in listing.splitlines():
        parts = line.split()
        if len(parts) == 2 and parts[1] == wanted:
            return parts[0].lower()
    steplog.fail("SHASUMS256.txt for node %s has no entry for %s" % (version, wanted))


def fetch(version, cache_dir, offline):
    """The verified node zip in the cache. The publisher's hash wins over the pin; a pin
    that disagrees with it stops the build; a first fetch records the pin."""
    pin = pins.entry("node", version)
    source = (pin or {}).get("url") or url(version)
    if not offline:
        official = publisher_sha256(version)
        if pin and pin.get("sha256", "").lower() != official:
            steplog.fail("node %s: the pin disagrees with the publisher (pin %s, SHASUMS256 %s)"
                         % (version, pin.get("sha256"), official))
        if not pin:
            pins.remember("node", version, url=source, sha256=official)
    pin = pins.entry("node", version)
    if not pin or not pin.get("sha256"):
        steplog.fail("no sha256 pin for node %s in build/payload-sources.json; without one "
                     "this build cannot verify what it downloaded" % version)
    return download.fetch(source, Path(cache_dir) / zip_name(version),
                          pin["sha256"], "node %s" % version, offline)


def install(zip_path, slot_dir, version, cache_dir):
    r"""Unzip node into <slot>\node and assert the binary reports this version."""
    extracted = ziputil.extract(zip_path, Path(cache_dir) / "node-extract")
    inner = extracted / ("node-v%s-win-x64" % version)
    node_exe = inner / "node.exe"
    if not node_exe.is_file():
        steplog.fail("the node zip has no node.exe at %s" % node_exe)
    target = Path(slot_dir) / "node"
    if target.exists():
        steplog.fail("node is already unpacked at %s" % target)
    inner.rename(target)
    reported = steplog.run([target / "node.exe", "--version"], cwd=slot_dir).stdout.strip()
    if reported.lstrip("v") != version:
        steplog.fail("the bundled node reports %s, expected %s" % (reported, version))
    steplog.info("slot-a\\node reports %s" % reported)
    return target
