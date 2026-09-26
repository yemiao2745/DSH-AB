"""Assemble payload\\: fetch and verify the inputs, unpack them, resolve the dsh tree,
copy the seed files, gate the path lengths, record the manifest, normalize the timestamps.

Directory structure of the payload equals the installation root (installer\\dsh-ab.iss
[Files] mirrors it 1:1), except for payload-manifest.json, which is never installed."""

import shutil
from pathlib import Path

from . import (dsh_tree, filetime, hashutil, install_layout, manifest, mingit,
               node_runtime, paths, payload_rootfiles, stage_skill, steplog)


def run(*, dsh_version, node_version, git_version, registry, app_exe, out_dir,
        cache_dir, offline, program_only, dshab_version, templates_dir=None):
    app_exe = Path(app_exe)
    out_dir = Path(out_dir)
    cache_dir = Path(cache_dir)
    templates_dir = Path(templates_dir) if templates_dir else paths.TEMPLATES
    dsh_version = (dsh_version or "").strip()

    if not app_exe.is_file():
        steplog.fail("DSH_AB.exe not found: %s" % app_exe)
    if not (templates_dir / "dsh-ab.toml").is_file():
        steplog.fail("no payload seeds in %s" % templates_dir)
    if not dsh_version and not program_only:
        steplog.fail("a dsh version is required; only --program-only can build a payload "
                     "without dsh in it")

    if program_only:
        steplog.info("[1/5] bundled node runtime - skipped, --program-only carries program files")
        node_zip = None
    else:
        steplog.info("[1/5] bundled node runtime")
        node_zip = node_runtime.fetch(node_version, cache_dir, offline)

    steplog.info("[2/5] portable git (MinGit)")
    git_zip = mingit.fetch(git_version, cache_dir, offline)

    if out_dir.exists():
        shutil.rmtree(out_dir)
    slot = out_dir / "slot-a"

    if program_only:
        steplog.info("[3/5] slot-a\\node - skipped, --program-only carries program files")
    else:
        steplog.info("[3/5] slot-a\\node")
        slot.mkdir(parents=True)
        node_runtime.install(node_zip, slot, node_version, cache_dir)

    steplog.info("[4/5] runtime\\git")
    (out_dir / "runtime").mkdir(parents=True)
    mingit.install(git_zip, out_dir / "runtime", git_version, cache_dir)

    if program_only:
        steplog.info("[5/5] slot-a\\app - skipped, --program-only carries program files")
    else:
        steplog.info("[5/5] slot-a\\app (the published dsh tree)")
        dsh_tree.resolve(slot, dsh_version, registry, cache_dir, offline)
        stage_skill.stage(templates_dir, slot)

    payload_rootfiles.stage(templates_dir, app_exe, out_dir)
    payload_rootfiles.assert_only_root_exe(out_dir)

    length, rel = filetime.assert_relative_paths(out_dir)
    steplog.info("longest payload path is %d characters (limit %d): %s"
                 % (length, filetime.MAX_RELATIVE_PATH, rel))

    fields = {
        "dshab_version": dshab_version,
        # A --program-only payload carries no dsh at all, so the field is empty rather than wrong.
        "dsh_version": "" if program_only else dsh_version,
        "exe_sha256": hashutil.sha256_file(out_dir / payload_rootfiles.EXE_NAME),
        "install_files": install_layout.install_root_files(out_dir),
    }
    record = manifest.write(out_dir, fields)

    filetime.normalize(out_dir)
    steplog.info("every payload timestamp set to %s, so the installer and the update pack "
                 "are reproducible" % filetime.FIXED_TIME.strftime("%Y-%m-%dT%H:%M:%SZ"))

    size_mb = sum(f.stat().st_size for f in out_dir.rglob("*") if f.is_file()) / (1024 * 1024)
    steplog.done("payload ready: %s (%.1f MB) dsh %s, %d files"
                 % (out_dir, size_mb, fields["dsh_version"] or "(none)", len(fields["install_files"])))
    return {"out_dir": out_dir, "manifest": record, "manifest_fields": fields,
            "install_files": fields["install_files"]}
