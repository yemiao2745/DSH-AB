"""Step output, warnings, running an external program, and failing by raising."""

import os
import subprocess

from . import paths


class BuildError(RuntimeError):
    """One build step failed; the driver turns this into exit code 1."""


def step(text):
    print("\n=== " + text, flush=True)


def info(text):
    print("      " + text, flush=True)


def warn(text):
    print("      warning: " + text, flush=True)


def fail(text):
    raise BuildError(text)


def done(text):
    print(text, flush=True)


def tool_env(base=None):
    r"""The environment an external tool is started with: its scratch space is in the project.

    go creates its work directory under %TEMP% before it compiles anything, and a %TEMP% that
    is outside the project is refused outright when the build itself runs restricted - the
    build then dies at "go: creating work dir: ... Access is denied" and never reaches the
    compiler. TEMP/TMP, go's work directory and go's build cache therefore all point into
    .tmp-build\ under the project root. Every one of these is read by its own tool only, so
    setting the go ones for every tool is harmless. The directories are created on first use.
    A caller's env is kept and only extended."""
    env = dict(os.environ if base is None else base)
    for name, directory in (("TEMP", paths.SCRATCH_TMP), ("TMP", paths.SCRATCH_TMP),
                            ("GOTMPDIR", paths.SCRATCH_GO),
                            ("GOCACHE", paths.SCRATCH_GOCACHE)):
        directory.mkdir(parents=True, exist_ok=True)
        env[name] = str(directory)
    return env


def run(cmd, cwd=None, env=None, timeout=None):
    """Run an external program with an explicit argv (never a shell) and raise on a
    non-zero exit. Returns the completed process, so callers can read stdout."""
    argv = [str(part) for part in cmd]
    info("$ " + subprocess.list2cmdline(argv))
    proc = subprocess.run(
        argv,
        cwd=str(cwd) if cwd else None,
        env=tool_env(env),
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=timeout,
    )
    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        fail("command failed with exit code %d: %s%s"
             % (proc.returncode, argv[0], ("\n" + detail) if detail else ""))
    return proc
