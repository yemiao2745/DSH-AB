"""build.py - build one dsh version's DSH_AB.exe, payload, installer and update pack.

Steps, exactly five:
  1. icon/manifest resource (.syso, regenerated only when it is stale)
  2. go build (GUI subsystem, both versions injected), go vet, PE check, --version read-back
  3. payload      (dshab_build.mkpayload)  - skipped by --skip-payload
  4. installer    (ISCC + installer/dsh-ab.iss)  - skipped by --skip-installer
  5. update pack  (dshab_build.mkupdate)   - skipped by --skip-update-pack

The update pack is decided by --skip-update-pack alone, never by --skip-installer: it packs
the same program files the installer carries, so one run builds it once.

--offline never touches the network; a populated cache\\ is what makes that possible.
Every path is relative to this project directory.

Usage:
  tools\\python\\python.exe build/py/build.py --dsh-version 0.1.7-rc.2
"""

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from dshab_build import (build_exe, iscc, isscheck, issgen, mkpayload, mkupdate,  # noqa: E402
                         paths, pins, steplog)

TEST_PRODUCT = "DSH-ABtest"
RELEASE_PRODUCT = "DSH-AB"


def parse_args(argv):
    parser = argparse.ArgumentParser(
        prog="build.py", description="Build DSH_AB.exe and the DSH-AB artifacts.")
    parser.add_argument("--dsh-version", required=True,
                        help="the dsh this build carries (a leading dsh-v is stripped), "
                             "e.g. 0.1.7-rc.2")
    parser.add_argument("--dshab-version",
                        help="this program's own version; default: src/dsh-ab/VERSION")
    parser.add_argument("--node-version",
                        help="bundled Node.js; default: the pin in build/payload-sources.json")
    parser.add_argument("--git-version",
                        help="bundled MinGit; default: the pin in build/payload-sources.json")
    parser.add_argument("--registry",
                        help="npm registry; default: the pin in build/payload-sources.json")
    parser.add_argument("--offline", action="store_true",
                        help="never touch the network; fail if the cache lacks an input")
    parser.add_argument("--skip-payload", action="store_true",
                        help="keep the payload that is already there")
    parser.add_argument("--skip-installer", action="store_true",
                        help="do not compile the installer")
    parser.add_argument("--skip-update-pack", action="store_true",
                        help="do not assemble the in-place update pack")
    parser.add_argument("--program-only", action="store_true",
                        help="payload = program files only (no node runtime, no dsh tree); "
                             "implies --skip-installer")
    parser.add_argument("--test-product", action="store_true",
                        help="build DSH-ABtest instead of DSH-AB; implies --skip-payload")
    return parser.parse_args(argv)


def versions(args):
    dshab_version = (args.dshab_version or "").strip()
    if not dshab_version:
        if not paths.VERSION_FILE.is_file():
            steplog.fail("no version source at %s" % paths.VERSION_FILE)
        dshab_version = paths.VERSION_FILE.read_text(encoding="utf-8").strip()
    if not dshab_version:
        steplog.fail("src\\dsh-ab\\VERSION is empty")
    dsh_version = args.dsh_version.strip()
    if dsh_version.startswith("dsh-v"):
        dsh_version = dsh_version[len("dsh-v"):]
    if not dsh_version:
        steplog.fail("--dsh-version is empty")
    return dshab_version, dsh_version


def guard_rails(args):
    """The combinations that cannot produce anything useful, refused before any work."""
    if args.program_only and not args.skip_installer:
        steplog.fail("--program-only carries no slot, so there is no usable installer to "
                     "build; an installer needs slot-a - combine --program-only with "
                     "--skip-installer")
    if args.program_only and args.skip_payload:
        steplog.fail("--program-only only changes how the payload is assembled, so combining "
                     "it with --skip-payload means nothing")
    skip_payload = args.skip_payload
    if args.test_product:
        skip_payload = True
        if not (paths.PAYLOAD / "dsh-ab.toml").is_file():
            steplog.fail("the test product is built from an existing payload (the payload is "
                         "byte-identical between the two products) and there is no payload\\ "
                         "yet - run a normal build once first")
    return skip_payload


def inputs(args, dsh_version):
    node_version = args.node_version or pins.sole_version("node")
    git_version = args.git_version or pins.sole_version("mingit")
    registry = args.registry
    if not registry:
        registry = (pins.entry("dsh", dsh_version) or {}).get("registry")
    return node_version, git_version, registry or "https://registry.npmjs.org/"


def run_build(args):
    product = TEST_PRODUCT if args.test_product else RELEASE_PRODUCT
    exe_name = "DSH_ABtest.exe" if args.test_product else "DSH_AB.exe"
    app_exe = paths.BUILD_DIR / exe_name
    dshab_version, dsh_version = versions(args)
    skip_payload = guard_rails(args)
    node_version, git_version, registry = inputs(args, dsh_version)
    steplog.done("dshab %s / dsh %s / product %s (%s)"
                 % (dshab_version, dsh_version, product, exe_name))
    steplog.info("node %s / MinGit %s / registry %s"
                 % (node_version, git_version, registry))

    steplog.step("1/5 icon and manifest resource")
    build_exe.ensure_resource(paths.GO_EXE, paths.ICON, paths.APP_MANIFEST, paths.SYSO,
                              paths.SCRATCH_RSRCGEN)

    steplog.step("2/5 go build (GUI subsystem, versions injected)")
    build_exe.build(paths.GO_EXE, paths.SRC_DIR, app_exe,
                    dshab_version, dsh_version, product)
    build_exe.check_gui_subsystem(app_exe)
    build_exe.check_versions(app_exe, [dshab_version, dsh_version])

    if skip_payload:
        steplog.step("3/5 payload (skipped)")
    else:
        steplog.step("3/5 payload")
        mkpayload.run(dsh_version=dsh_version, node_version=node_version,
                      git_version=git_version, registry=registry, app_exe=app_exe,
                      out_dir=paths.PAYLOAD, cache_dir=paths.CACHE, offline=args.offline,
                      program_only=args.program_only, dshab_version=dshab_version)

    setup = paths.DIST / (issgen.artifact_stem(product, dshab_version, dsh_version) + ".exe")
    if args.skip_installer:
        steplog.step("4/5 installer (skipped)")
    else:
        steplog.step("4/5 installer")
        generated = issgen.write_include(paths.ISS_GENERATED, product, dshab_version,
                                         dsh_version, args.test_product)
        steplog.info("build values written to %s" % generated)
        isscheck.verify(paths.ISS, paths.ROOT)
        iscc.compile(paths.ISS, issgen.defines(dshab_version, dsh_version, args.test_product),
                     setup)

    if args.skip_update_pack:
        steplog.step("5/5 update pack (skipped)")
    else:
        steplog.step("5/5 update pack")
        mkupdate.make_update(app_exe=app_exe, payload_dir=paths.PAYLOAD, out_dir=paths.DIST,
                             product_name=product, dshab_version=dshab_version)

    steplog.done("")
    if paths.DIST.is_dir():
        steplog.done("dist:")
        for entry in sorted(paths.DIST.iterdir()):
            if entry.is_file():
                steplog.done("  %-60s %8.1f MB"
                             % (entry.name, entry.stat().st_size / (1024 * 1024)))


def main(argv=None):
    args = parse_args(argv)
    try:
        run_build(args)
    except steplog.BuildError as exc:
        print("\nBUILD FAILED: %s" % exc, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
