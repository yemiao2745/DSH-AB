"""The source-level gates installer\\dsh-ab.iss has to pass before it is compiled.

Thirteen checks, each one a rule a silent install or the no-PowerShell requirement depends
on. Comments are ignored - a whole-line one outright, a trailing '//' one cut off its code - so a
gate is never tripped by prose, and prose can never stand in for a call either."""

import re
from pathlib import Path

from . import paths, steplog

NATIVE_DIALOG = re.compile(r"(?<![A-Za-z0-9_])MsgBox\b")
# A shell tool is invoked by naming its program, and in Inno a program name is always a quoted
# string ('...' in [Code], "..." in [Run], '{cmd}' inside either). Matching the name only inside
# quotes keeps this gate on invocations instead of on prose that merely mentions a shell.
SHELL_TOOLS = r"\{cmd\}|\[cmd\]|powershell|pwsh|cmd\.exe|reg\.exe|taskkill"
SHELL_CALLS = re.compile(r"['\"][^'\"\r\n]*?(%s)" % SHELL_TOOLS, re.I)
GIT_CALLS = re.compile(r"RunGit|InitBaselineRepo|git init")
START_MENU_ICON = re.compile(r'^Name:\s*"\{userprograms\}\\')
DESKTOP_ICON = re.compile(r'^Name:\s*"\{autodesktop\}\\')
LINK_VALUES = ("DshAbStartMenuLink", "DshAbDesktopLink")
# The write, not the name: WriteUninstallEntry records each shortcut's real path with this call, so
# a comment about the value names, or the uninstall-side read that goes through the same names,
# must not be able to stand in for it. Same call as the gate that preceded this one, with its
# spacing left free - that gate asserted the call, HKCU, Key and the value name, not where the
# spaces sit, so \s* here keeps "RegWriteStringValue(HKCU,Key,'...')" from reading as a miss.
LINK_WRITE = re.compile(r"RegWriteStringValue\s*\(\s*HKCU\s*,\s*Key\s*,\s*'(%s)'"
                        % "|".join(LINK_VALUES))


def _lines(text):
    """(line number, line) of everything that is not a comment.

    A whole-line comment is dropped; the prose after a trailing '//' is cut off, so a line is
    matched on its code only. (';' is skipped only at the start of a line: outside [Code] it opens
    a comment, but inside [Code] it ends every statement, and this file writes its explanatory
    comments with '//'.)"""
    for number, line in enumerate(text.splitlines(), 1):
        stripped = line.strip()
        if stripped.startswith(";") or stripped.startswith("//") or not stripped:
            continue
        yield number, line.split("//", 1)[0]


def check(iss_path, root=None):
    """Return the list of failed gates as readable strings (empty means all pass)."""
    iss_path = Path(iss_path)
    root = Path(root) if root else paths.ROOT
    text = iss_path.read_text(encoding="utf-8").lstrip("\ufeff")
    code = list(_lines(text))
    problems = []

    def setting(name):
        return any(line.strip().startswith(name) for _number, line in code)

    def setting_is(name, value):
        return any(line.strip().lower() == ("%s=%s" % (name, value)).lower()
                   for _number, line in code)

    # 1. Every dialog the installer shows has to be suppressible, or a silent run blocks forever.
    for number, line in code:
        if NATIVE_DIALOG.search(line):
            problems.append("%s:%d still calls a plain MsgBox; a silent install would block "
                            "on it (use SuppressibleMsgBox)" % (iss_path.name, number))
            break

    # 2. RedirectionGuard=no: dsh's profile module fallback uses NTFS junctions.
    if not setting_is("RedirectionGuard", "no"):
        problems.append("%s: RedirectionGuard=no is missing" % iss_path.name)

    # 3. The vendored Chinese language file, and it has to exist next to the script.
    language = iss_path.parent / "languages" / "ChineseSimplified.isl"
    if 'MessagesFile: "languages\\ChineseSimplified.isl"' not in text:
        problems.append('%s: no [Languages] entry with MessagesFile: '
                        '"languages\\ChineseSimplified.isl"' % iss_path.name)
    elif not language.is_file():
        problems.append("%s: the language file is missing" % language.name)

    # 4. The artifact name the build and the verification both derive.
    if not any(line.strip().startswith("OutputBaseFilename=")
               and "{#AppName}-{#DshabVersion}-{#DshVersion}-setup" in line
               for _number, line in code):
        problems.append("%s: OutputBaseFilename is not "
                        "{#AppName}-{#DshabVersion}-{#DshVersion}-setup" % iss_path.name)

    # 5. The pages that name the bundled dsh version.
    for label in ("WelcomeLabel2", "FinishedLabel", "FinishedLabelNoIcons"):
        if not any(line.strip().startswith(label + "=") and "{#DshVersion}" in line
                   for _number, line in code):
            problems.append("%s: %s does not name {#DshVersion}" % (iss_path.name, label))

    # 6. No AppMutex: it banned a second DSH-AB on the same machine.
    if setting("AppMutex="):
        problems.append("%s: AppMutex is set; two installations are allowed to coexist"
                        % iss_path.name)

    # 7. Inno must not own the Add/Remove entry; [Code] writes one per installation.
    if not setting_is("CreateUninstallRegKey", "no"):
        problems.append("%s: CreateUninstallRegKey=no is missing" % iss_path.name)

    # 8. A remembered directory is always a non-empty one, and those are refused anyway.
    if not setting_is("UsePreviousAppDir", "no"):
        problems.append("%s: UsePreviousAppDir=no is missing" % iss_path.name)

    # 9. Exactly one shortcut in each location, each named by [Code] at install time.
    icons = _section(code, "[Icons]")
    for pattern, what in ((START_MENU_ICON, "{userprograms}"), (DESKTOP_ICON, "{autodesktop}")):
        hits = [line for _number, line in icons if pattern.match(line.strip())]
        if len(hits) != 1:
            problems.append("%s: expected exactly one [Icons] line under %s, found %d"
                            % (iss_path.name, what, len(hits)))
        elif "{code:" not in hits[0]:
            problems.append("%s: the %s icon has no {code:...} name" % (iss_path.name, what))

    # 10. The uninstall entry and the shortcuts have to be recognised by both sides: the
    #     installer really writes each value name, and naming.go knows it.
    naming = root / "src" / "dsh-ab" / "naming.go"
    naming_text = naming.read_text(encoding="utf-8") if naming.is_file() else ""
    written = set()
    for _number, line in code:
        written.update(LINK_WRITE.findall(line))
    for value in LINK_VALUES:
        if value not in written:
            problems.append("%s: %s is never written" % (iss_path.name, value))
        if value not in naming_text:
            problems.append("%s: %s is unknown to the runtime" % (naming.name, value))

    # 11. The installer performs no git operation at all.
    for number, line in code:
        if GIT_CALLS.search(line):
            problems.append("%s:%d runs git; the AI that maintains the installation does that"
                            % (iss_path.name, number))
            break

    # 12. No shell: no powershell.exe, no cmd.exe, no reg.exe, no taskkill.exe.
    for number, line in code:
        called = SHELL_CALLS.search(line)
        if called:
            problems.append("%s:%d calls a shell or a shell tool (%s); the toolchain and the "
                            "installer must not use powershell/cmd/reg/taskkill"
                            % (iss_path.name, number, called.group(1)))
            break

    # 13. The vendored language file has to stay UTF-8 without BOM.
    if language.is_file() and language.read_bytes()[:3] == b"\xef\xbb\xbf":
        problems.append("%s: the language file must be UTF-8 without BOM" % language.name)

    return problems


def _section(code, name):
    """The (line number, line) pairs inside one [Section]."""
    inside = False
    out = []
    for number, line in code:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            inside = stripped.lower() == name.lower()
            continue
        if inside:
            out.append((number, line))
    return out


def verify(iss_path, root=None):
    """Compile-time gate: raise on the first build that would ship a broken installer."""
    problems = check(iss_path, root)
    if problems:
        steplog.fail("installer script gates failed (%d):\n  - %s"
                     % (len(problems), "\n  - ".join(problems)))
    steplog.info("%s passed all 13 source-level gates" % Path(iss_path).name)
    return []
