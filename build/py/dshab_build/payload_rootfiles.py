"""The payload root: the entry point, the user's configuration seed, the plugin ledger and
the files the installer bakes the installation path into."""

import shutil
from pathlib import Path

from . import steplog

# Read from build/templates; the last one is installed under its real name.
_ROOT_FILES = {"dsh-ab.toml": "dsh-ab.toml",
               "README.md": "README.md",
               "LICENSES.txt": "LICENSES.txt",
               "gitignore.template": ".gitignore"}
_DOC_FILES = ("PLUGINS.md",)
EXE_NAME = "DSH_AB.exe"


def stage(templates_dir, app_exe, out_dir):
    templates_dir = Path(templates_dir)
    out_dir = Path(out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    for source_name, installed_name in _ROOT_FILES.items():
        source = templates_dir / source_name
        if not source.is_file():
            steplog.fail("no seed file at %s" % source)
        shutil.copyfile(source, out_dir / installed_name)
    docs = out_dir / "docs"
    docs.mkdir(exist_ok=True)
    for name in _DOC_FILES:
        source = templates_dir / "docs" / name
        if not source.is_file():
            steplog.fail("no ledger seed at %s" % source)
        shutil.copyfile(source, docs / name)
    shutil.copyfile(app_exe, out_dir / EXE_NAME)
    return out_dir / EXE_NAME


def assert_only_root_exe(root):
    """BUILD_CONTRACT 4b.1: the payload root carries no executable but the app exe this build
    staged. The bundled Node runtime and MinGit live in their own directories, and the .iss
    [Files] section mirrors this root 1:1 - so a stray .exe would be installed silently, and
    the file-set rules could not catch it: they compare the installation against this same
    build's own record."""
    found = sorted(entry.name for entry in Path(root).iterdir()
                   if entry.is_file() and entry.suffix.lower() == ".exe")
    if found != [EXE_NAME]:
        steplog.fail("the payload root must carry exactly one executable, %s, but carries: %s"
                     % (EXE_NAME, ", ".join(found) or "(none)"))
