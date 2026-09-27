"""The bundled portable Git (MinGit). Upstream publishes no checksum, so the first
download is trusted and pinned, and every later build has to match that pin."""

from pathlib import Path

from . import download, hashutil, pins, steplog, ziputil

_RELEASE = r"^(\d+\.\d+\.\d+)\.(\d+)$"


def tag(version):
    """2.55.0.5 -> v2.55.0.windows.5 (the GitHub release tag)."""
    import re

    match = re.match(_RELEASE, version)
    if not match:
        steplog.fail("MinGit version %s is not in <major>.<minor>.<patch>.<build> form" % version)
    return "v%s.windows.%s" % (match.group(1), match.group(2))


def asset_name(version):
    return "MinGit-%s-64-bit.zip" % version


def url(version):
    return ("https://github.com/git-for-windows/git/releases/download/%s/%s"
            % (tag(version), asset_name(version)))


def fetch(version, cache_dir, offline):
    """The verified MinGit zip in the cache."""
    pin = pins.entry("mingit", version)
    source = (pin or {}).get("url") or url(version)
    target = Path(cache_dir) / asset_name(version)
    if pin and pin.get("sha256"):
        return download.fetch(source, target, pin["sha256"], "MinGit %s" % version, offline)
    if offline:
        steplog.fail("--offline was given and %s has no pin in build/payload-sources.json"
                     % asset_name(version))
    steplog.info("upstream publishes no checksum for %s: trusting this download and pinning "
                 "it - review and commit build/payload-sources.json" % asset_name(version))
    if target.exists():
        target.unlink()
    download.download_raw(source, target)
    digest = hashutil.sha256_file(target)
    pins.remember("mingit", version, url=source, sha256=digest)
    steplog.info("recorded the pin for MinGit %s (%s)" % (version, digest))
    return target


def install(zip_path, runtime_dir, version, cache_dir):
    r"""Unzip MinGit to <runtime>\git and assert the extracted git reports this release."""
    extracted = ziputil.extract(zip_path, Path(cache_dir) / "git-extract")
    git_exe = extracted / "cmd" / "git.exe"
    if not git_exe.is_file():
        steplog.fail("the MinGit zip has no cmd\\git.exe")
    expected = tag(version).lstrip("v")
    reported = steplog.run([git_exe, "--version"], cwd=extracted).stdout.strip()
    reported = reported.replace("git version", "").strip()
    if reported != expected:
        steplog.fail("MinGit reports %s, expected %s (release %s)" % (reported, expected, tag(version)))
    target = Path(runtime_dir) / "git"
    if target.exists():
        steplog.fail("MinGit is already unpacked at %s" % target)
    extracted.rename(target)
    steplog.info("runtime\\git reports %s" % reported)
    return target
