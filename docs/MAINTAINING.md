# Maintaining DSH-AB (for AI maintainers)

What the installer ships to a running dsh is not a document but a skill
(build\templates\skills\dsh-self-maintenance\SKILL.md, installed as <DSH_HOME>\skills\dsh-self-maintenance\SKILL.md with
the installation root baked in) for working on dsh's own installation. This file tells you how to change DSH-AB itself;
the long-form reasoning behind every rule below is in the local development notes, which are
deliberately not uploaded.

## Build

    pwsh -NoProfile -File build\build.ps1 -DshVersion <dsh version> [-Offline]
    pwsh -NoProfile -File build\verify-silent.ps1 [-Setup <installer>]
    payload\slot-a\node\node.exe build\verify-skill.mjs [skills root]

verify-silent.ps1 runs verify-skill.mjs; on its own it checks build\templates\skills (no argument) or an installed
<install root>\slot-a\data\skills. It asserts what dsh requires before it loads a skill at all - a two-level
<name>\SKILL.md bundle, frontmatter with a non-empty name and description, a name accepted by @deepseek-ai/dsh-skill isSkillName, and the trigger words the description must carry - because dsh drops a non-conforming skill silently.

The script installs into a temporary directory with a fixed test port of its own (the `/PRODUCTION=` value in the
script) and asserts the installed dsh-ab.toml carries that port (a silent install shows no ports page), the uninstall
entry and its `EstimatedSize` are there, and it can find nothing to restore afterwards. It may run while the user's own installation runs (no mutex refuses an install), but its
port must be free: the installer binds 127.0.0.1 and refuses a taken port.

## Verification reports

Every verification and real-machine test report is filed in one place: one file per round in the local
development-notes directory (that directory is not shipped and not uploaded, but it stays on disk, so the next person - or AI - can
read what was actually run, with what evidence, and what was left open). A report that only lives in a temporary
directory (`.tmp-*`) is lost with it, and a machine test that is not written down has to be repeated from scratch.
Cleaning up the test installations and keeping the report are two separate things: do both.

## Test build (a second, independent product)

    pwsh -NoProfile -File build\build.ps1 -DshVersion <dsh version> -TestProduct

`-TestProduct` builds the very same source as the product **DSH-ABtest**: one switch changes the installer's
`AppName`, its default installation directory (`%LOCALAPPDATA%\DSH-ABtest`), the Start Menu entry name,
the Add/Remove entry's DisplayName prefix, the Go `appName` constant (injected with `-ldflags -X`; the source
constant is never edited per build) and the artifact name (`DSH-ABtest-<dshab version>-<dsh version>-setup.exe`).
Installing it therefore stays completely apart from a DSH-AB that is already installed and running - which is what
makes the *single-install* naming rule testable on a machine that already has a release build.

The two products stay apart in the other direction too: every scan of "the other installations on this machine"
(`naming.go`'s `isDshAbEntry` and `dsh-ab.iss`'s `IsOwnProductEntry`, which must stay verbatim-identical) reads the
first word of a DisplayName - the product name - and an entry naming *another* product of the family (`DSH-AB` vs
`DSH-ABtest`) is never claimed, not even by the `DSH_AB.exe` fallback, because both products ship that same exe name.

A test build implies `-SkipPayload`: the payload is identical between the two products, so the test build reads the
exe from `build\DSH_ABtest.exe` instead of the payload's `DSH_AB.exe` and never overwrites the release artifacts
(`build\DSH_AB.exe`, `dist\DSH-AB-*-setup.exe`). Run a normal build once first so `payload\` exists.

## Tests

The Go module root is `src\dsh-ab`, not the repository root:

    cd src\dsh-ab
    tools\go\bin\gofmt.exe -l .          # must print nothing
    tools\go\bin\go.exe vet ./...
    tools\go\bin\go.exe test ./... -count=1

CI runs the same `go test ./... -count=1` with the Go toolchain build.ps1 uses, and `build` depends on it: a failing test blocks the offline build and the release.

## Adding a new dsh version

Add the version to the release matrix in the GitHub workflow, build with -DshVersion <version> (the artefact is
<product>-<project version>-<dsh version>-setup.exe, DSH-AB for a normal build), then run verify-silent.ps1: both
versions are injected into the exe and checked by a static gate.

## In-place update (the update pack)

Updating DSH-AB on a machine that already has one does **not** go through the installer: the installer still refuses a
non-empty directory and there is still no overwrite upgrade. The update path is a separate artefact.

    build\build.ps1 -DshVersion <dsh version>      # step 5/5 also writes dist\<product>-<dshab>-update.zip

The pack is assembled by `build\mkupdate.ps1` out of the very same `payload\` the installer is compiled from, with the
slots left out: `DSH_AB.exe`, `README.md`, `LICENSES.txt`, `.gitignore`, `docs\` (still carrying the
`{{DSH_AB_ROOT}}` placeholder) and the whole `runtime\`. Not in it: either slot, `dsh-ab.toml` (the user's ports and
log settings) and `payload-manifest.json`. dsh itself lives inside the slots, so the pack does not depend on the dsh
version it was built next to - **one pack per DSH-AB version**. Every matrix cell of the workflow builds one, but only
the cell `discover` names in `update_cell` uploads it (`DSH-AB-update`), and `publish` uploads it into the release
as `DSH-AB-<dshab>-update.zip`.

**No script ships with it.** What to do with the pack is written down as rules in the skill the installer already ships
(`build\templates\skills\dsh-self-maintenance\SKILL.md`, section 「升级 DSH-AB 本身」), and the AI maintaining the
installation carries it out: commit a snapshot first, expand the pack, delete every directory of the installation root
except `slot-a`, `slot-b`, `.git` and `state\`, copy the new files over, replace `DSH_AB.exe` by renaming the
running one out of the way first, and bake the installation root into the two ledgers (UTF-8 without BOM). Rolling back
is `git reset --hard <that snapshot>`; the snapshot tracks the program files and `docs\`, **not** `runtime\` and not
the slots (`.gitignore` excludes both).

## Production port

There is exactly one port, so a release has nothing to switch: the default is **3090**. It lives in
installer\dsh-ab.iss (InitializeSetup), build\templates\dsh-ab.toml ([ports]) and src\dsh-ab\config.go
(defaultProductionPort, which Validate's repair message derives from). config_test.go pins the number.

## Rules

- **The bundled skill never describes the installer**: it covers dsh's own work
  only. Naming the *installation's* own configuration (its root, `ports.production` in it) is fine;
  installer facts - checks, refusals, shortcuts, the entry - are not.
- Never put an AGENTS.md into a slot's data directory: dsh injects <DSH_HOME>\AGENTS.md into every session no matter
  the working directory. The AI instructions are a user-level skill, and the installer runs no git command at all.
- The port comes from the command line (/PRODUCTION=) in InitializeSetup, which also runs for a silent install; the
  ports page is seeded from that variable. Never put a literal port back into the page, and keep the wizard's "first
  free port" pre-fill behind `not WizardSilent`: a silent install never switches ports.
- The installer refuses a non-empty target directory: never add an overwrite/merge or in-place upgrade path, and never
  refuse a *second* installation (no AppMutex, no "already installed" gate). It does refuse a port that is already taken,
  by binding 127.0.0.1. Never read the empty-directory rule as forbidding the AI's in-place upgrade of dsh inside the
  inactive slot of this same installation.
- Every installation keeps its own identity, all derived from the installation directory: its HKCU Add/Remove entry
  (CreateUninstallRegKey stays no; [Code] writes it per installation, `EstimatedSize` included), its Start Menu
  entry and its desktop shortcut (never a fixed name; both sit directly in `开始菜单\程序` — there is no
  subfolder, several installations are told apart by their names alone), and the name
  `InstallName` computes for this machine
  (DSH-AB / DSH-AB (port) / DSH-AB (tag), never with a version number in the name): the brackets belong
  to the name itself, so every face (the Add/Remove DisplayName, the Start Menu entry, the desktop shortcut, the
  status popup's title) shows that string verbatim and none of them adds brackets of its own - the version number
  appears in DisplayVersion only. `naming.go` and the [Code] side of dsh-ab.iss must answer the same thing for the
  same machine, and naming_test.go pins the three shapes as literals.
- The uninstaller reports what it could not delete (`RemoveAll` collects every leftover, `CurUninstallStepChanged`
  prints 「以下没删掉，请手动删除」). Never make that report silent again.
- ISCC cannot read a **source** path longer than MAX_PATH, and npm's nested `node_modules` produce them (dsh
  0.1.5-alpha.2's payload had a 261-character one): the compile then dies with 「系统找不到指定的路径」. build.ps1 therefore
  maps `payload\` onto a free drive letter (`subst`) and compiles with `/DPayload=X:\`, which keeps the source prefix
  three characters long no matter where the checkout lives - CI's `D:\a\<repo>\<repo>` is about twenty characters longer
  than a development checkout, so the payload must never sit behind the repository path again. The payload's own
  relative layout is untouched by this, so a pack or an installer built from it is byte-identical to before.
- `build\verify-skill.mjs` must keep looking for `@deepseek-ai/dsh-skill` instead of assuming a layout: Node's own
  resolution from the app tree and from `dsh` (which finds `dsh\node_modules\@deepseek-ai\dsh-skill`, the layout
  0.1.5-alpha.x ships) comes first, the two concrete paths are the fallback, and only a genuine miss is reported. The
  real assertions - frontmatter, the kebab-case name, the nine trigger words - are never relaxed.
- The tray's single-instance mutex is derived from the installation root (src\dsh-ab\main.go): two installations run
  side by side, a second start of the same one is refused. Never let a modal dialog block other work - the popup
  mechanism is single and owned. Credentials come from .secrets\github.toml and must never be printed or shipped.
