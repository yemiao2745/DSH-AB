"""The installer-source assertions the thirteen isscheck gates do not make, about the change that
took every Exec() call and both helper-tool invocations out of installer\\dsh-ab.iss: the port
now binds 127.0.0.1 in-process through ws2_32, and the "processes under this install root" scan
walks the process list in-process through kernel32. All of it is read-only source analysis -
nothing here compiles or runs the installer.

Not a replacement for isscheck.check() (build\\py\\dshab_build\\isscheck.py), which already owns
gate on shell names inside quoted strings, the silent-dialog gate, and the shortcut/registry
contract. Two of the checks below are deliberately wider than that gate: a shell name is refused
anywhere in the code, not only inside a quoted string, and Exec() itself is refused outright -
after this change the installer starts no helper program at all."""

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ISS = ROOT / "installer" / "dsh-ab.iss"

# The shell tools the toolchain and the installer must not use, in any spelling Inno can call one
# with: the program name, or the {cmd} / [cmd] placeholder.
SHELL_NAMES = re.compile(r"powershell|pwsh|cmd\.exe|reg\.exe|taskkill|\{cmd\}|\[cmd\]", re.I)

# Every executable the new in-process code needs, by the DLL it comes from. Declared and called are
# both checked, because a declaration nobody calls would satisfy a declaration-only assertion.
WS2_32_FUNCTIONS = ("WSAStartup", "socket", "bind", "closesocket", "WSAGetLastError")
WS2_32_CALLED = ("WSAStartup", "socket", "bind", "closesocket", "WSAGetLastError")
KERNEL32_FUNCTIONS = ("CreateToolhelp32Snapshot", "Process32FirstW", "Process32NextW", "OpenProcess",
                      "QueryFullProcessImageNameW", "TerminateProcess", "CloseHandle", "GetLastError")
KERNEL32_CALLED = ("CreateToolhelp32Snapshot", "Process32FirstW", "Process32NextW", "OpenProcess",
                   "QueryFullProcessImageNameW", "TerminateProcess", "CloseHandle")

# Build scripts this repository deleted when the toolchain became Python. A comment that still names
# one of them points the next reader at a file that is not there.
DELETED_BUILD_SCRIPTS = ("build.ps1", "mkpayload.ps1", "mkupdate.ps1", "verify-silent.ps1",
                         "install-layout.ps1", "provision-go.ps1", "verify-skill.mjs")


def code_lines(text):
    """(line number, code) of everything that is not a comment.

    Same rule as isscheck._lines: a whole-line ';' or '//' comment is dropped, and the prose after a
    trailing '//' is cut off, so no assertion here can be satisfied - or broken - by prose."""
    for number, line in enumerate(text.lstrip("\ufeff").splitlines(), 1):
        stripped = line.strip()
        if not stripped or stripped.startswith(";") or stripped.startswith("//"):
            continue
        yield number, line.split("//", 1)[0]


class ShippedInstallerTest(unittest.TestCase):
    """Assertions about the script that actually ships."""

    @classmethod
    def setUpClass(cls):
        cls.text = ISS.read_text(encoding="utf-8")
        cls.code = list(code_lines(cls.text))
        cls.code_text = "\n".join(line for _number, line in cls.code)
        # What is neither a comment, nor a declaration, nor a string literal: the calls themselves.
        # Pascal needs no parentheses around a call (Err := WSAGetLastError), so "used" has to mean
        # the name is there as code - a name that only survives inside a log message is not a call.
        cls.used = "\n".join(re.sub(r"'[^']*'", "''", line) for _number, line in cls.code
                             if not line.strip().lower().startswith("function"))

    def test_the_installer_calls_no_exec_at_all(self):
        """No helper program is started: both Exec() sites are gone.

        The isscheck shell gate looks for shell names inside quoted strings; this one is about the
        mechanism, so a call to any program at all fails it."""
        for name in ("Exec", "ShellExec"):
            called = [number for number, line in self.code
                      if re.search(r"(?<![A-Za-z0-9_])%s\s*\(" % name, line)]
            self.assertEqual(called, [],
                             "%s still calls %s() on line(s) %r" % (ISS.name, name, called))

    def test_the_port_probe_is_declared_from_ws2_32_and_really_called(self):
        """The bind probe needs the socket functions, and the script has to use them: a set of
        declared-but-never-called externals is the shape a half-done rewrite leaves behind."""
        for name in WS2_32_FUNCTIONS:
            self.assertIn("external '%s@ws2_32.dll stdcall'" % name, self.text,
                          "%s does not declare %s from ws2_32" % (ISS.name, name))
        for name in WS2_32_CALLED:
            self.assertRegex(self.used, r"(?<![A-Za-z0-9_])%s(?![A-Za-z0-9_])" % name,
                             "%s declares %s but never uses it" % (ISS.name, name))

    def test_the_process_scan_is_declared_from_kernel32_and_really_called(self):
        """The Toolhelp walk needs the snapshot functions; both halves are asserted for the same
        reason as above."""
        for name in KERNEL32_FUNCTIONS:
            self.assertIn("external '%s@kernel32.dll stdcall'" % name, self.text,
                          "%s does not declare %s from kernel32" % (ISS.name, name))
        for name in KERNEL32_CALLED:
            self.assertRegex(self.used, r"(?<![A-Za-z0-9_])%s(?![A-Za-z0-9_])" % name,
                             "%s declares %s but never uses it" % (ISS.name, name))

    def test_no_declaration_or_call_names_a_shell_tool(self):
        """Stricter than the isscheck shell gate, which matches only inside quotes: here a shell name
        anywhere in the code fails, so a declaration or a call can never slip through unquoted."""
        hits = [(number, line.strip()) for number, line in self.code if SHELL_NAMES.search(line)]
        self.assertEqual(hits, [], "%s still names a shell tool: %r" % (ISS.name, hits))

    def own_running_processes_body(self):
        """The body of OwnRunningProcesses, from its "function" line to its closing "end;"."""
        lines = self.text.splitlines()
        start = next(index for index, line in enumerate(lines)
                     if line.startswith("function OwnRunningProcesses("))
        # The function's own end is the one at column 0; the ends of its nested blocks are indented.
        end = next(index for index in range(start, len(lines)) if lines[index] == "end;")
        return "\n".join(lines[start:end + 1])

    def test_own_running_processes_reports_a_failed_scan_as_minus_one(self):
        """The count is the answer and -1 is the "could not look" answer: a 0 written on a failure
        path would read as "nothing of ours is running", which is the one wrong answer - the
        uninstaller then skips the close-and-continue question and reports success over files that
        were still open. Asserted on the code path, not by running the installer."""
        body = self.own_running_processes_body()
        self.assertIn("Result := -1;", body,
                      "OwnRunningProcesses no longer starts from -1")
        self.assertNotIn("Result := 0", body,
                         "OwnRunningProcesses writes 0, so a failed scan reads as "
                         '"nothing is running"')
        # -1 is the state before anything is looked at, so both failure exits leave it behind.
        self.assertLess(body.index("Result := -1;"), body.index("CreateToolhelp32Snapshot"),
                        "the snapshot runs before -1 is set, so a failure could leak a stale Result")
        self.assertEqual(body.count("Result := Count;"), 1,
                         "the finished walk is not the one and only place a count is written")
        failures = [match.start() for match in re.finditer(r"\bExit;", body)]
        self.assertGreaterEqual(len(failures), 2,
                                "the two ways the walk can fail to start no longer exit")
        self.assertTrue(all(position < body.index("Result := Count;") for position in failures),
                        "a failure path writes a count")

    def test_the_caller_reads_minus_one_as_could_not_look(self):
        """-1 is only worth returning if the uninstaller tells it apart from 0."""
        self.assertIn("Running < 0", self.code_text,
                      "the uninstall step no longer handles the -1 answer")
        self.assertIn("跳过了运行中进程的检查", self.text,
                      "the -1 answer is no longer reported in the log")

    def test_the_old_powershell_helpers_are_gone(self):
        """PortProbe and KillScript were the two Pascal Script helpers that existed only to run a
        PowerShell child; with the calls gone, their names must not survive anywhere in the file."""
        for name in ("PortProbe", "KillScript"):
            self.assertNotIn(name, self.text,
                             "%s still mentions the deleted helper %s" % (ISS.name, name))
        self.assertNotIn(".ps1", self.text, "%s still points at a .ps1 script" % ISS.name)

    def test_no_comment_names_a_deleted_build_script(self):
        """Comments in this file name the tool that builds each thing it depends on; a name the
        repository deleted sends the reader to a file that is not there."""
        for name in DELETED_BUILD_SCRIPTS:
            self.assertNotIn(name, self.text,
                             "%s still names the deleted %s" % (ISS.name, name))
        # The pointer has to still exist in its live form, or the fix for the line above could have
        # been "delete the comment" instead of "name the tool that is really there".
        self.assertIn("mkpayload.py", self.text,
                      "%s no longer names the payload assembler it depends on" % ISS.name)


if __name__ == "__main__":
    unittest.main()
