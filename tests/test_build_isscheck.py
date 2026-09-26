"""The thirteen source-level gates of dshab_build/isscheck.py, one test per gate:
each gate has to fire on a script that violates it and stay silent on a compliant
one, and prose may never stand in for a call."""

import shutil
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))


class ScratchTest(unittest.TestCase):
    """Every test works in its own throwaway directory inside the workspace."""

    def setUp(self):
        scratch = ROOT / ".tmp-test"
        scratch.mkdir(exist_ok=True)
        self.dir = Path(tempfile.mkdtemp(prefix="iss-", dir=str(scratch)))
        self.addCleanup(shutil.rmtree, str(self.dir), ignore_errors=True)

from dshab_build import isscheck, paths, steplog

BS = chr(92)

# A compliant script: every gate is satisfied exactly once, and the two shells of the
# fixture (the language file, the runtime's value names) are written by make_installer().
BASE = """
; DSH-AB installer - synthetic fixture for the thirteen source-level gates.
[Setup]
AppName=DSH-AB
AppVersion={#DshabVersion}
RedirectionGuard=no
CreateUninstallRegKey=no
UsePreviousAppDir=no
OutputBaseFilename={#AppName}-{#DshabVersion}-{#DshVersion}-setup
Compression=lzma2/normal
SolidCompression=yes

[Languages]
Name: "chinesesimplified"; MessagesFile: "languages@BS@ChineseSimplified.isl"

[Messages]
WelcomeLabel2=Welcome to DSH-AB {#DshVersion}.
FinishedLabel=DSH-AB {#DshVersion} is installed.
FinishedLabelNoIcons=DSH-AB {#DshVersion} is installed without icons.

[Icons]
Name: "{userprograms}@BS@{code:GetStartMenuFolder}"; Filename: "{app}@BS@DSH_AB.exe"
Name: "{autodesktop}@BS@{code:GetDesktopName}"; Filename: "{app}@BS@DSH_AB.exe"

[Files]
Source: "{#Payload}@BS@*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs

[Run]
Filename: "{app}@BS@DSH_AB.exe"; Flags: nowait postinstall skipifsilent

[Code]
var
  Key: string;

procedure WriteShortcutRecords;
begin
  // Both value names the runtime reads back; a silent run still writes them.
  RegWriteStringValue(HKCU, Key, 'DshAbStartMenuLink', StartMenuPath);
  RegWriteStringValue(HKCU, Key, 'DshAbDesktopLink', DesktopPath);
  if not FileExists(StartMenuPath) then
    SuppressibleMsgBox('The shortcut is missing.', mbError, MB_OK, IDOK);
end;
""".replace("@BS@", BS)

START_MENU_WRITE = "  RegWriteStringValue(HKCU, Key, 'DshAbStartMenuLink', StartMenuPath);"
DESKTOP_WRITE = "  RegWriteStringValue(HKCU, Key, 'DshAbDesktopLink', DesktopPath);"
BOTH_NAMES = 'const (\n\tDshAbStartMenuLink = "DshAbStartMenuLink"\n\tDshAbDesktopLink = "DshAbDesktopLink"\n)\n'


class GateTest(ScratchTest):
    def check(self, text, language=b"[Lang]\n", naming=BOTH_NAMES, bom=False):
        installer = self.dir / "installer"
        (installer / "languages").mkdir(parents=True, exist_ok=True)
        iss = installer / "dsh-ab.iss"
        body = text.encode("utf-8")
        iss.write_bytes((b"\xef\xbb\xbf" if bom else b"") + body)
        if language is not None:
            (installer / "languages" / "ChineseSimplified.isl").write_bytes(language)
        src = self.dir / "src" / "dsh-ab"
        src.mkdir(parents=True, exist_ok=True)
        if naming is not None:
            (src / "naming.go").write_text(naming, encoding="utf-8")
        return isscheck.check(iss, self.dir)

    def assertGate(self, problems, marker):
        self.assertTrue(any(marker in problem for problem in problems),
                        "no gate fired with %r; got %r" % (marker, problems))

    def assertNoGate(self, problems, marker):
        self.assertFalse(any(marker in problem for problem in problems),
                         "gate %r fired on a script that does not violate it: %r"
                         % (marker, problems))


class CompliantScriptTest(GateTest):
    def test_the_compliant_script_passes_every_gate(self):
        self.assertEqual(self.check(BASE), [])

    def test_a_script_that_starts_with_a_utf8_bom_still_passes(self):
        self.assertEqual(self.check(BASE, bom=True), [])

    def test_every_failing_gate_is_reported_not_only_the_first(self):
        broken = BASE.replace("RedirectionGuard=no\n", "")
        broken = broken.replace("CreateUninstallRegKey=no\n", "")
        broken = broken.replace("UsePreviousAppDir=no\n", "")
        problems = self.check(broken)
        self.assertEqual(len(problems), 3)
        self.assertGate(problems, "RedirectionGuard=no is missing")
        self.assertGate(problems, "CreateUninstallRegKey=no is missing")
        self.assertGate(problems, "UsePreviousAppDir=no is missing")

    def test_verify_raises_on_a_script_that_violates_a_gate(self):
        installer = self.dir / "installer"
        (installer / "languages").mkdir(parents=True)
        iss = installer / "dsh-ab.iss"
        iss.write_text(BASE.replace("SuppressibleMsgBox", "MsgBox"), encoding="utf-8")
        (installer / "languages" / "ChineseSimplified.isl").write_bytes(b"[Lang]\n")
        (self.dir / "src" / "dsh-ab").mkdir(parents=True)
        (self.dir / "src" / "dsh-ab" / "naming.go").write_text(BOTH_NAMES, encoding="utf-8")
        with self.assertRaises(steplog.BuildError) as caught:
            isscheck.verify(iss, self.dir)
        self.assertIn("installer script gates failed", str(caught.exception))


class NativeDialogGateTest(GateTest):
    def test_a_plain_msgbox_fails_the_gate(self):
        problems = self.check(BASE.replace("SuppressibleMsgBox", "MsgBox"))
        self.assertGate(problems, "still calls a plain MsgBox")

    def test_a_suppressible_msgbox_is_the_form_that_passes(self):
        self.assertNoGate(self.check(BASE), "still calls a plain MsgBox")

    def test_prose_about_msgbox_is_not_a_call(self):
        text = BASE.replace(
            "  // Both value names the runtime reads back; a silent run still writes them.",
            "  // MsgBox would block a silent run, so every dialog is suppressible.\n"
            "  Sleep(0); // MsgBox is never called here")
        self.assertEqual(self.check(text), [])


class RedirectionGuardGateTest(GateTest):
    def test_a_missing_redirection_guard_setting_fails_the_gate(self):
        problems = self.check(BASE.replace("RedirectionGuard=no\n", ""))
        self.assertGate(problems, "RedirectionGuard=no is missing")

    def test_a_value_other_than_no_fails_the_gate(self):
        problems = self.check(BASE.replace("RedirectionGuard=no", "RedirectionGuard=yes"))
        self.assertGate(problems, "RedirectionGuard=no is missing")

    def test_a_comment_cannot_stand_in_for_the_setting(self):
        problems = self.check(BASE.replace("RedirectionGuard=no\n", "; RedirectionGuard=no\n"))
        self.assertGate(problems, "RedirectionGuard=no is missing")


class LanguageFileGateTest(GateTest):
    def test_the_languages_entry_has_to_point_at_the_vendored_file(self):
        problems = self.check(
            BASE.replace('; MessagesFile: "languages@BS@ChineseSimplified.isl"'.replace("@BS@", BS),
                         "; MessagesFile: \"builtin\""))
        self.assertGate(problems, "no [Languages] entry with MessagesFile")

    def test_the_vendored_language_file_has_to_exist(self):
        problems = self.check(BASE, language=None)
        self.assertGate(problems, "the language file is missing")

    def test_a_language_file_with_a_bom_fails_the_encoding_gate(self):
        problems = self.check(BASE, language=b"\xef\xbb\xbf[Lang]\n")
        self.assertGate(problems, "must be UTF-8 without BOM")

    def test_a_language_file_without_a_bom_passes_the_encoding_gate(self):
        self.assertNoGate(self.check(BASE), "must be UTF-8 without BOM")


class OutputNameGateTest(GateTest):
    def test_the_artifact_name_has_to_be_the_one_the_build_derives(self):
        problems = self.check(BASE.replace("{#AppName}-{#DshabVersion}-{#DshVersion}-setup",
                                           "DSH-AB-setup"))
        self.assertGate(problems, "OutputBaseFilename is not")


class VersionLabelGateTest(GateTest):
    def test_every_page_that_names_the_dsh_version_names_it(self):
        for label in ("WelcomeLabel2", "FinishedLabel", "FinishedLabelNoIcons"):
            with self.subTest(label=label):
                line = [text for text in BASE.splitlines()
                        if text.startswith(label + "=")][0]
                problems = self.check(BASE.replace(line, line.replace(" {#DshVersion}", "")))
                self.assertGate(problems, "%s does not name {#DshVersion}" % label)

    def test_the_label_lines_that_name_the_version_pass(self):
        for label in ("WelcomeLabel2", "FinishedLabel", "FinishedLabelNoIcons"):
            with self.subTest(label=label):
                self.assertNoGate(self.check(BASE), "%s does not name {#DshVersion}" % label)


class AppMutexGateTest(GateTest):
    def test_an_app_mutex_fails_the_gate(self):
        problems = self.check(BASE.replace("AppVersion={#DshabVersion}\n",
                                           "AppVersion={#DshabVersion}\nAppMutex=DSH-AB\n"))
        self.assertGate(problems, "AppMutex is set")

    def test_a_comment_about_the_app_mutex_is_harmless(self):
        text = BASE.replace("AppVersion={#DshabVersion}\n",
                            "AppVersion={#DshabVersion}\n; AppMutex=DSH-AB, deliberately not set\n")
        self.assertNoGate(self.check(text), "AppMutex is set")


class UninstallKeyGateTest(GateTest):
    def test_innos_own_uninstall_registry_key_fails_the_gate(self):
        problems = self.check(BASE.replace("CreateUninstallRegKey=no\n", ""))
        self.assertGate(problems, "CreateUninstallRegKey=no is missing")


class PreviousDirGateTest(GateTest):
    def test_a_remembered_application_directory_fails_the_gate(self):
        problems = self.check(BASE.replace("UsePreviousAppDir=no\n", ""))
        self.assertGate(problems, "UsePreviousAppDir=no is missing")


class IconsGateTest(GateTest):
    def test_a_missing_desktop_icon_fails_the_gate(self):
        line = [text for text in BASE.splitlines() if text.startswith('Name: "{autodesktop}')][0]
        problems = self.check(BASE.replace(line + "\n", ""))
        self.assertGate(problems, "expected exactly one [Icons] line under {autodesktop}, found 0")

    def test_two_icons_in_one_location_fail_the_gate(self):
        line = [text for text in BASE.splitlines() if text.startswith('Name: "{userprograms}')][0]
        problems = self.check(BASE.replace(line + "\n", line + "\n" + line + "\n"))
        self.assertGate(problems, "expected exactly one [Icons] line under {userprograms}, found 2")

    def test_an_icon_without_a_code_supplied_name_fails_the_gate(self):
        problems = self.check(BASE.replace("{code:GetStartMenuFolder}", ""))
        self.assertGate(problems, "the {userprograms} icon has no {code:...} name")

    def test_only_the_icons_section_counts(self):
        line = [text for text in BASE.splitlines() if text.startswith('Name: "{userprograms}')][0]
        text = BASE.replace("[Icons]\n" + line + "\n", "[Tasks]\n" + line + "\n[Icons]\n")
        problems = self.check(text)
        self.assertGate(problems, "expected exactly one [Icons] line under {userprograms}, found 0")

    def test_the_two_declared_icons_pass_the_gate(self):
        problems = self.check(BASE)
        self.assertNoGate(problems, "expected exactly one [Icons] line under {userprograms}, found")
        self.assertNoGate(problems, "icon has no {code:...} name")


class ShortcutValueGateTest(GateTest):
    def test_a_real_write_of_each_shortcut_value_satisfies_the_gate(self):
        self.assertNoGate(self.check(BASE), "is never written")

    def test_a_read_of_a_value_name_does_not_count_as_writing_it(self):
        text = BASE.replace(
            START_MENU_WRITE + "\n" + DESKTOP_WRITE,
            "  if RegQueryStringValue(HKCU, Key, 'DshAbStartMenuLink', Value) then\n"
            "    RegWriteStringValue(HKCU, Key, 'OtherValue', Value);\n"
            "  Value := 'DshAbDesktopLink'; // read it back, never write it")
        problems = self.check(text)
        self.assertGate(problems, "DshAbStartMenuLink is never written")
        self.assertGate(problems, "DshAbDesktopLink is never written")

    def test_a_comment_naming_the_values_does_not_count_as_writing_them(self):
        text = BASE.replace(START_MENU_WRITE, "  // " + START_MENU_WRITE.strip())
        text = text.replace(DESKTOP_WRITE, "  // " + DESKTOP_WRITE.strip())
        problems = self.check(text)
        self.assertGate(problems, "DshAbStartMenuLink is never written")
        self.assertGate(problems, "DshAbDesktopLink is never written")

    def test_a_near_miss_write_call_does_not_count(self):
        text = BASE.replace(
            START_MENU_WRITE,
            "  RegWriteExpandStringValue(HKCU, Key, 'DshAbStartMenuLink', StartMenuPath);")
        problems = self.check(text)
        self.assertGate(problems, "DshAbStartMenuLink is never written")
        self.assertNoGate(problems, "DshAbDesktopLink is never written")

    def test_a_value_name_the_runtime_does_not_know_fails_the_gate(self):
        problems = self.check(BASE, naming='const DshAbStartMenuLink = "DshAbStartMenuLink"\n')
        self.assertGate(problems, "naming.go: DshAbDesktopLink is unknown to the runtime")
        self.assertNoGate(problems, "DshAbStartMenuLink is unknown to the runtime")

    def test_a_missing_runtime_name_file_leaves_both_names_unknown(self):
        problems = self.check(BASE, naming=None)
        self.assertGate(problems, "naming.go: DshAbStartMenuLink is unknown to the runtime")
        self.assertGate(problems, "naming.go: DshAbDesktopLink is unknown to the runtime")


class GitGateTest(GateTest):
    def test_an_installer_that_runs_git_fails_the_gate(self):
        text = BASE.replace("  if not FileExists(StartMenuPath) then",
                            "  if not DirExists(Baseline) then\n"
                            "    RunGitInit(Baseline);\n"
                            "  if not FileExists(StartMenuPath) then")
        self.assertGate(self.check(text), "runs git")

    def test_prose_about_git_is_harmless(self):
        text = BASE.replace("  // Both value names the runtime reads back; a silent run still writes them.",
                            "  // The installer never runs git init; the runtime does that.")
        self.assertNoGate(self.check(text), "runs git")


class ShellGateTest(GateTest):
    def with_code(self, line):
        return BASE.replace("  if not FileExists(StartMenuPath) then",
                            "  " + line + "\n  if not FileExists(StartMenuPath) then")

    def test_a_quoted_powershell_program_name_fails_the_gate(self):
        problems = self.check(self.with_code(
            "Exec('powershell.exe', '-NoProfile -Command whoami', '', SW_HIDE, "
            "ewWaitUntilTerminated, ResultCode);"))
        self.assertGate(problems, "calls a shell or a shell tool (powershell)")

    def test_a_quoted_cmd_placeholder_fails_the_gate(self):
        problems = self.check(BASE.replace(
            'Filename: "{app}@BS@DSH_AB.exe"; Flags: nowait postinstall skipifsilent'.replace("@BS@", BS),
            'Filename: "{cmd}"; Parameters: "/c whoami"'))
        self.assertGate(problems, "calls a shell or a shell tool ({cmd})")

    def test_a_quoted_reg_exe_fails_the_gate(self):
        problems = self.check(self.with_code(
            "Exec('reg.exe', 'query HKCU', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);"))
        self.assertGate(problems, "calls a shell or a shell tool (reg.exe)")

    def test_a_quoted_taskkill_fails_the_gate(self):
        problems = self.check(self.with_code(
            "Exec('taskkill.exe', '/F /IM DSH_AB.exe', '', SW_HIDE, "
            "ewWaitUntilTerminated, ResultCode);"))
        self.assertGate(problems, "calls a shell or a shell tool (taskkill)")

    def test_a_trailing_comment_that_mentions_powershell_is_not_a_call(self):
        problems = self.check(self.with_code("Sleep(0);  // never call powershell.exe here"))
        self.assertNoGate(problems, "calls a shell or a shell tool")

    def test_a_whole_line_comment_that_mentions_cmd_and_taskkill_is_not_a_call(self):
        problems = self.check(self.with_code("// cmd.exe and taskkill.exe are never used"))
        self.assertNoGate(problems, "calls a shell or a shell tool")

    def test_prose_that_mentions_taskkill_without_a_program_name_is_not_a_call(self):
        problems = self.check(self.with_code("KillAllProcesses(); // no taskkill.exe anywhere"))
        self.assertNoGate(problems, "calls a shell or a shell tool")


class ShippedInstallerTest(unittest.TestCase):
    def test_the_shipped_installer_script_passes_all_thirteen_gates(self):
        self.assertEqual(isscheck.check(paths.ISS), [])
