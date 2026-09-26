"""Resource compile and the native exe build: icon/manifest resource, go build with both
versions injected, go vet, the PE subsystem check and a read-back of --version."""

import os
import struct
from pathlib import Path

from . import steplog

RSRC = "github.com/akavel/rsrc@v0.10.2"
GUI_SUBSYSTEM = 2


def _env():
    env = dict(os.environ)
    env["GOFLAGS"] = ""
    return env


def ensure_resource(go_exe, icon, manifest, syso, scratch):
    """Rebuild the .syso whenever the icon or the manifest is newer: a stale resource is
    how a fixed icon stays broken inside the shipped exe."""
    icon, manifest, syso, scratch = Path(icon), Path(manifest), Path(syso), Path(scratch)
    stale = False
    if syso.exists():
        stamp = syso.stat().st_mtime
        stale = any(p.stat().st_mtime > stamp for p in (icon, manifest))
    if syso.exists() and not stale:
        steplog.info("using the existing %s" % syso)
        return False
    steplog.info("icon or manifest is newer than the .syso, regenerating it"
                 if stale else "no %s, generating it" % syso.name)
    scratch.mkdir(parents=True, exist_ok=True)
    steplog.run([go_exe, "run", RSRC, "-arch", "amd64", "-ico", icon,
                 "-manifest", manifest, "-o", syso], cwd=scratch, env=_env())
    return True


def build(go_exe, src_dir, exe_path, dshab_version, dsh_version, product_name):
    """go build into exe_path and go vet the package."""
    ldflags = ("-H=windowsgui -s -w"
               " -X main.dshabVersion=%s -X main.dshVersion=%s -X main.appName=%s"
               % (dshab_version, dsh_version, product_name))
    steplog.run([go_exe, "build", "-buildvcs=false", "-trimpath", "-ldflags", ldflags,
                 "-o", Path(exe_path).resolve(), "."], cwd=src_dir, env=_env())
    steplog.run([go_exe, "vet", "./..."], cwd=src_dir, env=_env())
    return Path(exe_path)


def pe_subsystem(exe_path):
    with open(exe_path, "rb") as handle:
        handle.seek(0x3C)
        pe_offset = struct.unpack("<I", handle.read(4))[0]
        handle.seek(pe_offset + 0x5C)
        return struct.unpack("<H", handle.read(2))[0]


def check_gui_subsystem(exe_path):
    subsystem = pe_subsystem(exe_path)
    if subsystem != GUI_SUBSYSTEM:
        steplog.fail("%s is not a GUI-subsystem binary (subsystem=%d)"
                     % (Path(exe_path).name, subsystem))
    size_mb = Path(exe_path).stat().st_size / (1024 * 1024)
    steplog.info("%s: GUI subsystem, %.2f MB" % (Path(exe_path).name, size_mb))
    return subsystem


def version_line(exe_path, cwd=None):
    """Run the built exe with --version and return what it printed."""
    reported = steplog.run([exe_path, "--version"], cwd=cwd).stdout.strip()
    steplog.info("%s --version: %s" % (Path(exe_path).name, reported))
    return reported


def check_versions(exe_path, versions, what="exe"):
    """The injected versions are read back out of the binary, never assumed: the artifact
    name and the installation pages are derived from exactly this report."""
    reported = version_line(exe_path)
    for version in versions:
        if version not in reported:
            steplog.fail("the %s does not report version %s (it printed: %s)"
                         % (what, version, reported))
    return reported
