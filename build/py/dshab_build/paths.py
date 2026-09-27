"""Project paths. Every path is derived from this file's own location, so the
project directory can be moved anywhere without editing the toolchain."""

from pathlib import Path

PACKAGE_DIR = Path(__file__).resolve().parent
PY_DIR = PACKAGE_DIR.parent                 # build/py
ROOT = PY_DIR.parent.parent                 # project root

BUILD_DIR = ROOT / "build"
SRC_DIR = ROOT / "src" / "dsh-ab"
INSTALLER_DIR = ROOT / "installer"
TOOLS = ROOT / "tools"
TEMPLATES = BUILD_DIR / "templates"
PAYLOAD = ROOT / "payload"
DIST = ROOT / "dist"
CACHE = ROOT / "cache"

GO_EXE = TOOLS / "go" / "bin" / "go.exe"
PIN_FILE = BUILD_DIR / "payload-sources.json"
APP_MANIFEST = BUILD_DIR / "dsh-ab.exe.manifest"
ICON = SRC_DIR / "assets" / "dsh.ico"
SYSO = SRC_DIR / "rsrc_windows_amd64.syso"
VERSION_FILE = SRC_DIR / "VERSION"
ISS = INSTALLER_DIR / "dsh-ab.iss"
ISS_GENERATED = INSTALLER_DIR / "dsh-ab.generated.iss"

# Build record written into the payload root; it is the one file of the payload that is
# never installed (see install_layout.py).
MANIFEST_NAME = "payload-manifest.json"

CACHE_NODE = CACHE / "node"
CACHE_GIT = CACHE / "git"
CACHE_APP_LOCKS = CACHE / "app-locks"
CACHE_NPM = CACHE / "npm"
SCRATCH_RSRCGEN = ROOT / ".tmp-src" / "rsrcgen"
SCRATCH_UPDATE = ROOT / ".tmp-update"

# Scratch space for the external tools we start. go, npm and ISCC all default their temporary
# files to %TEMP% (and go its build cache to %LOCALAPPDATA%\go-build) - outside the project,
# which a build may neither depend on nor write to. All of it lives under .tmp-build\, which
# the existing .tmp-*/ rule in .gitignore already covers.
SCRATCH_BUILD = ROOT / ".tmp-build"
SCRATCH_TMP = SCRATCH_BUILD / "general"
SCRATCH_GO = SCRATCH_BUILD / "go"
SCRATCH_GOCACHE = SCRATCH_BUILD / "gocache"
