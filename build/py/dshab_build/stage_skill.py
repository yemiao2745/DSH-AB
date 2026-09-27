r"""Copy the bundled skill into the fresh slot's DSH_HOME.

A new slot starts with a dsh user-level skill: dsh scans <DSH_HOME>\skills\<name>\SKILL.md
and loads it only when the task matches its description."""

import shutil
from pathlib import Path

from . import steplog


def stage(templates_dir, slot_dir):
    source_root = Path(templates_dir) / "skills"
    if not source_root.is_dir():
        steplog.fail("no bundled skills at %s" % source_root)
    staged = []
    for skill in sorted(p for p in source_root.iterdir() if p.is_dir()):
        source = skill / "SKILL.md"
        if not source.is_file():
            steplog.fail("the bundled skill %s has no SKILL.md" % skill.name)
        target_dir = Path(slot_dir) / "data" / "skills" / skill.name
        target_dir.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target_dir / "SKILL.md")
        staged.append(target_dir / "SKILL.md")
    if not staged:
        steplog.fail("no bundled skill was staged from %s" % source_root)
    return staged
