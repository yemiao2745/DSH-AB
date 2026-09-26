r"""Resolve the published dsh dependency tree into slot-a\app.

npm is called as the shipped node running node_modules\npm\bin\npm-cli.js - never
through npm.cmd, which would need a shell. A lockfile per dsh version pins the tree: the
first resolve writes it, every later build runs 'npm ci' against it."""

import json
from pathlib import Path

from . import dsh_times, pins, steplog

PACKAGE = "@deepseek-ai/dsh"


def app_dir(slot_dir):
    return Path(slot_dir) / "app"


def npm_cli(slot_dir):
    cli = Path(slot_dir) / "node" / "node_modules" / "npm" / "bin" / "npm-cli.js"
    if not cli.is_file():
        steplog.fail("the bundled node carries no npm at %s" % cli)
    return cli


def _write_package_json(app, dsh_version):
    app.mkdir(parents=True, exist_ok=True)
    package = {
        "name": "app",
        "version": "1.0.0",
        "private": True,
        "dependencies": {PACKAGE: dsh_version},
    }
    (app / "package.json").write_text(
        json.dumps(package, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def _resolve_before(dsh_version, registry, offline):
    """The cut-off instant for a first resolve, and the instant to record in the pin."""
    pin = pins.entry("dsh", dsh_version)
    if pin and pin.get("published"):
        steplog.info("using the pinned release instant %s for %s" % (pin["published"], dsh_version))
        return dsh_times.from_pin(pin["published"]), None
    if offline:
        steplog.fail("--offline was given and build/payload-sources.json records no release "
                     "instant for dsh %s, which a first resolve needs" % dsh_version)
    times = dsh_times.release_times(registry)
    stamp = dsh_times.published(dsh_version, times)
    return dsh_times.before_instant(dsh_version, times), stamp


def resolve(slot_dir, dsh_version, registry, cache_dir, offline):
    r"""Install the dsh tree for this version into <slot>\app and return what happened."""
    app = app_dir(slot_dir)
    cli = npm_cli(slot_dir)
    node_exe = Path(slot_dir) / "node" / "node.exe"
    _write_package_json(app, dsh_version)

    locks = Path(cache_dir) / "app-locks"
    locks.mkdir(parents=True, exist_ok=True)
    lock_file = locks / ("app-%s.json" % dsh_version)
    npm_cache = Path(cache_dir) / "npm"
    npm_args = ["--no-audit", "--no-fund", "--loglevel=error", "--cache=%s" % npm_cache]
    before = None
    recorded = None

    if lock_file.is_file():
        steplog.info("npm ci against the lockfile %s" % lock_file)
        (app / "package-lock.json").write_bytes(lock_file.read_bytes())
        if offline:
            npm_args.append("--offline")
        steplog.run([node_exe, cli, "ci"] + npm_args, cwd=app)
    else:
        if offline:
            steplog.fail("--offline was given and there is no lockfile for dsh %s (%s)"
                         % (dsh_version, lock_file))
        before, recorded = _resolve_before(dsh_version, registry, offline)
        npm_args += ["--registry=%s" % registry, "--prefer-offline"]
        if before:
            npm_args.append("--before=%s" % before)
        steplog.info("npm install: first resolve for dsh %s (--before=%s)" % (dsh_version, before))
        steplog.run([node_exe, cli, "install"] + npm_args, cwd=app)
        lock_file.write_bytes((app / "package-lock.json").read_bytes())

    installed = app / "node_modules" / "@deepseek-ai" / "dsh"
    package_json = installed / "package.json"
    if not package_json.is_file():
        steplog.fail("npm resolved no %s under %s" % (PACKAGE, app))
    got = json.loads(package_json.read_text(encoding="utf-8")).get("version")
    if got != dsh_version:
        steplog.fail("the resolved dsh is %s, expected %s" % (got, dsh_version))
    entry_point = installed / "lib" / "bin.js"
    if not entry_point.is_file():
        steplog.fail("the resolved dsh has no lib\\bin.js: %s" % entry_point)

    if recorded:
        pins.remember("dsh", dsh_version, published=recorded)
        steplog.info("recorded the release instant %s for dsh %s" % (recorded, dsh_version))
    return {"app": app, "lock_file": lock_file, "before": before, "recorded": recorded}
