"""Assemble the in-place update pack: the installation root's program files, minus the
slots, minus the user's configuration and minus the ledgers. Overwriting the installation
root with it is the whole update."""

import shutil
from pathlib import Path

from . import build_exe, filetime, hashutil, install_layout, paths, steplog, ziputil

EXE_NAME = "DSH_AB.exe"
# The authoritative list of what an update may overwrite; see install_layout.py.
IGNORE = ("slot-*", "dsh-ab.toml", "docs\\*")


def make_update(*, app_exe, payload_dir, out_dir, product_name, dshab_version):
    app_exe = Path(app_exe)
    payload = Path(payload_dir)
    out_dir = Path(out_dir)
    if not app_exe.is_file():
        steplog.fail("DSH_AB.exe not found: %s" % app_exe)
    if not (payload / "README.md").is_file():
        steplog.fail("payload not found: %s" % payload)

    scratch = paths.SCRATCH_UPDATE
    stage = scratch / "stage"
    zip_path = out_dir / ("%s-%s-update.zip" % (product_name, dshab_version))
    try:
        if scratch.exists():
            shutil.rmtree(scratch)
        stage.mkdir(parents=True)
        # The exe is installed as DSH_AB.exe whatever the build product is called.
        shutil.copyfile(app_exe, stage / EXE_NAME)
        for name in ("README.md", "LICENSES.txt", ".gitignore"):
            source = payload / name
            if not source.is_file():
                steplog.fail("the payload has no %s (expected %s)" % (name, source))
            shutil.copyfile(source, stage / name)
        runtime = payload / "runtime"
        if not runtime.is_dir():
            steplog.fail("the payload has no runtime\\ (expected %s)" % runtime)
        shutil.copytree(runtime, stage / "runtime")

        # Same instant the payload uses: the exe here comes straight from go build, and
        # without normalization two builds of one commit produce different packs.
        filetime.normalize(stage)

        if not (stage / EXE_NAME).is_file():
            steplog.fail("the staged pack has no %s" % EXE_NAME)
        expected = install_layout.install_root_files(payload, ignore=IGNORE)
        actual = install_layout.install_root_files(stage)
        diff = install_layout.compare_file_sets(expected, actual)
        if diff["missing"]:
            steplog.fail("the update pack is missing %d installation file(s): %s"
                         % (len(diff["missing"]), install_layout.format_diff(diff["missing"])))
        if diff["unexpected"]:
            steplog.fail("the update pack carries %d file(s) that must not be overwritten "
                         "(slots, dsh-ab.toml, the docs\\ ledgers): %s"
                         % (len(diff["unexpected"]), install_layout.format_diff(diff["unexpected"])))
        slots = [p.name for p in stage.iterdir() if p.is_dir() and p.name.startswith("slot-")]
        if slots:
            steplog.fail("the update pack must carry no slot: %s" % ", ".join(slots))
        build_exe.check_versions(stage / EXE_NAME, [dshab_version], what="packed exe")

        out_dir.mkdir(parents=True, exist_ok=True)
        ziputil.pack_tree(stage, zip_path)
        digest = hashutil.sha256_file(zip_path)
        size_mb = zip_path.stat().st_size / (1024 * 1024)
        top = sorted(p.name for p in stage.iterdir())
    finally:
        if scratch.exists():
            shutil.rmtree(scratch, ignore_errors=True)

    steplog.done("update pack: %s" % zip_path)
    steplog.info("%d files, %.1f MB, top level: %s" % (len(actual), size_mb, "  ".join(top)))
    steplog.info("carries no slot, no dsh-ab.toml and no docs\\ ledgers")
    steplog.info("sha256: %s" % digest)
    return {"zip": zip_path, "sha256": digest, "files": len(actual)}
