"""Locate the Inno Setup compiler and compile installer\\dsh-ab.iss."""

import os
import shutil
from pathlib import Path

from . import hashutil, paths, steplog

ISCC_NAME = "ISCC.exe"


def find_iscc():
    """The usual per-user install location first, then the machine locations."""
    candidates = []
    local = os.environ.get("LOCALAPPDATA")
    if local:
        candidates.append(Path(local) / "Programs" / "Inno Setup 6" / ISCC_NAME)
    for variable in ("ProgramFiles", "ProgramFiles(x86)"):
        base = os.environ.get(variable)
        if base:
            candidates.append(Path(base) / "Inno Setup 6" / ISCC_NAME)
    on_path = shutil.which(ISCC_NAME)
    if on_path:
        candidates.append(Path(on_path))
    for candidate in candidates:
        if candidate.is_file():
            return candidate
    steplog.fail("%s not found; looked in: %s"
                 % (ISCC_NAME, ", ".join(str(c) for c in candidates)))


def compile(iss_path, defines, artifact):
    """Run ISCC and verify the artifact it names really appeared."""
    iscc = find_iscc()
    steplog.info("ISCC: %s" % iscc)
    steplog.run([iscc] + list(defines) + [str(Path(iss_path).resolve())], cwd=paths.ROOT)
    artifact = Path(artifact)
    if not artifact.is_file():
        steplog.fail("the installer was compiled but %s does not exist" % artifact)
    digest = hashutil.sha256_file(artifact)
    steplog.done("artifact: %s (%.1f MB)" % (artifact, artifact.stat().st_size / (1024 * 1024)))
    steplog.info("sha256: %s" % digest)
    return {"artifact": artifact, "sha256": digest}
