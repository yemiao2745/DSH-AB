# Maintaining DSH-AB (for AI maintainers)

What ships to a running dsh is not a document but a skill (build\templates\skills\dsh-self-maintenance\SKILL.md,
installed as <DSH_HOME>\skills\dsh-self-maintenance\SKILL.md with the installation root baked into it); this file is
for changing DSH-AB itself. The reasoning behind every rule below is in the local development notes, which are
deliberately not uploaded.

## Build

    pwsh -NoProfile -File build\build.ps1 -DshVersion <dsh version> [-Offline]
    pwsh -NoProfile -File build\verify-silent.ps1 [-Setup <installer>] [-Iss <repo>\installer\dsh-ab.iss]
    payload\slot-a\node\node.exe build\verify-skill.mjs [skills root]

build.ps1 runs five steps - resource, Go build (both versions injected by -ldflags), payload, Inno Setup
compile, update pack - each with its own switch, documented in that script's header. Both verify-silent.ps1
defaults (the temporary installation directory and the .iss it gates) come from the script's own location, so a
clone anywhere runs unchanged; -Iss is only needed for a .iss outside this repository.

verify-silent.ps1 runs verify-skill.mjs, then asserts the installation as a whole: the root's file set against the
build-time record in the payload manifest, the three baked files (the skill, docs\PLUGINS.md, docs\TODO.md)
verbatim once the placeholder is replaced, the exe hash, the port, the uninstall entry with its EstimatedSize, and
nothing left to restore. It installs into a temporary directory on a port of its own (the /PRODUCTION= value in the
script: a silent install shows no ports page); it may run while the user's own installation runs, but its port has
to be free - the installer binds 127.0.0.1 and refuses a taken port.

## Verification reports

One file per round in the local development-notes directory: that is how the next round reads what was actually
run, with what evidence and what was left open. A report living only in a temporary directory (.tmp-*) is lost with
it. Clean up the test installations and file the report: two separate things, both required.

## Test build (a second, independent product)

    pwsh -NoProfile -File build\build.ps1 -DshVersion <dsh version> -TestProduct

-TestProduct builds the very same source as the product **DSH-ABtest**: one switch changes the installer's AppName,
its default installation directory, the Start Menu entry name, the Add/Remove DisplayName prefix, the Go appName
constant (injected with -ldflags -X; the value in the source is the default) and the artifact name. Every
name-related place carries the product suffix, so a test installation stays apart from a DSH-AB that is already
installed and running - which is what makes the *single-install* naming rule testable on a machine that already has
a release build.

Both sides scan "the other installations on this machine" by one rule - the first word of a DisplayName is the
product name - so an entry naming another product of the family (DSH-AB vs DSH-ABtest) does not belong to this one,
the DSH_AB.exe fallback included, because both products ship that same exe name. naming.go's isDshAbEntry and
dsh-ab.iss's IsOwnProductEntry are that one rule on two sides - one Go, one Pascal, so the rule matches and the text
cannot - and naming_test.go plus verify-silent.ps1 pin it. A test build implies -SkipPayload (it takes the exe from
build\DSH_ABtest.exe and leaves the release artifacts alone), so run a normal build once first.

## Tests, and a new dsh version

The Go module root is src\dsh-ab, not the repository root:

    cd src\dsh-ab
    tools\go\bin\gofmt.exe -l .          # must print nothing
    tools\go\bin\go.exe vet ./...
    tools\go\bin\go.exe test ./... -count=1

CI runs the same go test ./... -count=1 with the toolchain build.ps1 uses, and a failing test blocks the offline
build as well as the release. No version list is written down in this repository: the workflow's discover job finds
the upstream dsh-v* releases itself, and a dispatch may pass dsh_versions=0.1.5-rc.2,0.1.2-rc.1 to build a subset.
Locally: build.ps1 -DshVersion <version> then verify-silent.ps1 - the artifact name, the payload's file record and
the two versions compiled into the exe have to agree (build.ps1 reads both back out of the built exe, dsh-ab.iss
names the artifact from them, verify-silent.ps1 gates that name).

## In-place update (the update pack)

Updating DSH-AB on a machine that already has one does not go through the installer: the installer refuses a
non-empty directory, and there is no overwrite upgrade. The update path is a separate artefact:

    build\build.ps1 -DshVersion <dsh version>      # step 5/5 also writes dist\<product>-<dshab>-update.zip

build\mkupdate.ps1 assembles it out of the very same payload\ the installer is compiled from, minus the slots,
minus dsh-ab.toml (the user's ports and log settings) and minus docs\: both ledgers are the installation's own
accumulated record, so no pack overwrites them (baking the installation root into them is the installer's job, and
only for a fresh installation). dsh itself lives inside the slots, so the pack does not depend on the dsh version
it was built next to: one pack per DSH-AB version. CI builds it in its standalone update-pack job (-SkipInstaller
still runs step 5/5; -SkipUpdatePack is the only switch that skips it) and publish puts it into the release. **No
script ships with it**: the process is written down as rules in the skill the installer already ships, section
"Updating DSH-AB itself", and the AI maintaining the installation carries it out. Rolling back is the snapshot
taken before the update (git reset --hard <that snapshot>): the snapshot tracks the program files and docs\, and
also dsh-ab.toml and state\ (.gitignore leaves out only the two slots, runtime\ and logs\).

## Production port

Exactly one port, so a release has nothing to switch. It comes from one source in three places -
installer\dsh-ab.iss, build\templates\dsh-ab.toml and src\dsh-ab\config.go - and config_test.go pins the number, so
no document carries it. The installer takes it from /PRODUCTION= in InitializeSetup, which also runs for a silent
install, and seeds the ports page from that variable: a silent install keeps the port it was given and aborts when
it is taken, while the wizard pre-fills the first free port (behind not WizardSilent). The page carries a seed only.

## Rules

- **The bundled skill covers dsh's own side of the installation** - the installation root, dsh-ab.toml, state\,
  the slots. What the installer does (its checks, refusals, shortcuts, registry entries) is described here.
- **AI instructions are a user-level skill** (<DSH_HOME>\skills\), and a slot's data\ holds dsh's own data: dsh
  injects <DSH_HOME>\AGENTS.md into every session no matter the working directory, so one in there is permanent
  prompt pollution. The installer runs no git command at all.
- **One identity per installation, all of it derived from the installation directory**: the HKCU Add/Remove entry
  (CreateUninstallRegKey stays no, [Code] writes it per installation, EstimatedSize included), the Start Menu entry
  and the desktop shortcut (both directly in 开始菜单\程序, told apart by their names alone - no subfolder), and the
  name InstallName computes for this machine (DSH-AB / DSH-AB (port) / DSH-AB (tag)). The brackets belong to the
  name, so every face shows that string verbatim and none adds brackets of its own; a version number appears in
  DisplayVersion only. naming.go and the [Code] side of dsh-ab.iss answer the same thing for the same machine, and
  naming_test.go pins the three shapes as literals.
- **The empty-directory rule belongs to the installer**: it accepts an empty target only - an overwrite/merge or an
  in-place upgrade there would contradict it - while a *second* installation on the same machine is allowed (no
  AppMutex, no "already installed" gate) and a taken port is refused by binding 127.0.0.1. It does not restrict the
  AI's in-place upgrade of dsh inside the inactive slot of this same installation, which is how dsh is upgraded.
- **The uninstaller reports what it could not delete** (RemoveAll collects every leftover, CurUninstallStepChanged
  prints 「以下没删掉，请手动删除」), and a silent uninstall writes that report into its log.
- **The payload's relative layout is what has to stay short**: ISCC and Windows cannot create a path longer than
  MAX_PATH, so mkpayload.ps1 gates the longest relative path at 180 characters (it prints the worst path and stops
  the build past it), and the --before pin on the fresh npm resolve keeps the dependency family at one generation so
  the tree stays flat. Measure the built payload before touching either - the history is in those code comments.
- **build\verify-skill.mjs resolves @deepseek-ai/dsh-skill instead of assuming a layout** (Node's own resolution
  first, two concrete fallbacks, a genuine miss reported); its assertions - non-empty frontmatter, the kebab-case
  name, the nine trigger words - stay as they are.
- **Three separate subagents**: the one that writes a change does not verify it, an independent one does, and
  another audits the whole change for bugs and for what could be simplified or deleted - each in its own run, all
  of them before anything is published.
- **The tray's single-instance mutex is derived from the installation root** (src\dsh-ab\main.go): two
  installations run side by side, a second start of the same one is refused. The popup mechanism has one owner and
  is non-blocking, so background work keeps running. Credentials come from .secrets\github.toml: read locally
  only, absent from output, logs and everything shipped.
