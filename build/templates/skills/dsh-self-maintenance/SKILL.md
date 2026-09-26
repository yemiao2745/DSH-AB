---
name: dsh-self-maintenance
description: 维护这套 DSH 安装本身时读：更新 dsh、升级 dsh、安装/更新/移除插件、修改 dsh 或其插件配置、打补丁、把改动同步到另一个槽、登记切槽（槽位切换）、维护安装根的插件与待办台账。只在你要动 dsh 自身时读它。
---

# Working on this DSH installation

Install root = `{{DSH_AB_ROOT}}` - if the directory was moved, take the one `DSH_AB.exe` sits in. Every change happens
inside it. The bundle carries its own Git under `runtime\git\` (find the exe inside it): DSH-AB runs no git command,
so the snapshots are yours.

**No tools ship with this installation.** There is no helper script anywhere under the install root, and nothing may
be added there either: everything below is guidance you carry out with your own means (the browser, the bundled Git,
dsh's own commands).

**"Patch" here always means dsh's own patches** - the changes this installation has made to dsh itself and to its
plugins (they live in the slots and are listed in `docs\PLUGINS.md`). DSH-AB never patches dsh, and DSH-AB's own
program files are not part of that ledger.

## Invariants

1. **The running slot is left alone - not one byte.** The active slot serves the production port and holds the data
   in use. Never write, patch, install into or delete anything under `slot-<active>\`: not `app\`, not `node\`,
   and not `data\profiles\` either - `dsh plugin add` and `pnpm install` rewrite a plugin tree, and doing that
   under a live instance is exactly what this forbids. **Nothing enforces this for you**: DSH-AB does not lock the
   slot, so you are the guard. Every change goes into the inactive slot (invariant 2). The active slot's `data\` is
   written by the running dsh itself - that is the instance working, not permission for you to.
2. **A working copy is never built anywhere else.** The inactive slot `<install root>\slot-<x>` is the only place a
   change is made, and `<install root>\slot-archive\<slot>-<dsh version>-<yyyyMMdd-HHmmss>\` (created on demand,
   ignored by the install root's git) is the only place an old tree is set aside. No copies under `C:\Backups`, no
   scratch trees in a workspace temp directory: a copy DSH-AB does not know about is a copy the next round has to
   rediscover, and the user has said so.
3. **Snapshot the installation root before you start and after you finish** (the bundled Git: add -A, commit; what
   and when is your judgement). That snapshot is what a rollback returns to.
4. **Prove the change in the inactive slot before switching.** Start dsh there on a free port you pick yourself and
   tell the user which port that was. **A listening port, an HTTP 200, and a 200 from the login gate prove
   nothing** - client plugins load in the browser and fail only there, and a session-format change fails only
   inside a real turn. Drive it for real:

   Nothing is provided to help you: **this installation ships no tools** - no script under the install root, and
   nothing may be added there either. Drive the browser yourself and look at what it shows:

   - open the authenticated address (the one the instance printed; `logs\dsh-ab.log` carries it on the line
     "已取得 dsh 认证地址" - turn the log level to `full` while working if you want the instance's own output);
   - **you run real turns there** ("请只回复两个字：收到"), and only a turn that completes with no error shown and
     no failed-plugin notice counts;
   - a listening port, an HTTP 200 and a 200 from the login gate are still worth nothing: client plugins fail only
     inside the loaded page, and a session-format change fails only inside a real turn.

   If you cannot get such a turn through, you have not proved the change - nothing switches.
5. **The switch is the user's, and you may not register one until invariant 4 passed.** Write the registration into
   `state\pending.json` the way this installation's format does (`{"target_slot":"slot-b"}`) and tell the user to
   click 重启; a running tray picks the file up within about two seconds and shows it in the menu and the status
   popup. A registration can be withdrawn until then. **Nothing checks that you ran invariant 4** - that honesty is
   yours, and it is the whole reason invariant 4 is written down. (The tray's own 槽位切换 item belongs to the
   user; it is not your path.)
6. **Carrying data across slots is a union, never a replacement.** Read the old slot, write the new one; never
   delete or overwrite a file that is already there; when one file differs on the two sides
   (`storages\workspace.json`, `settings.yaml`) stop and ask the user - there is no safe automatic answer.
   `data\storages\` has to travel WITH `data\sessions\`, or the sidebar silently loses the new conversations:
   `dsh-workspace` rebuilds its registry only while `global.initialized` is false, and never backfills.
   `sessions.cleared-*` is not a move-in-place tool. Say plainly that the last turns of this conversation may not
   make it across.

The 2-second re-read of `state\` in invariant 5 needs DSH-AB 0.1.3 or newer. On an older tray, write
`state\pending.json` and then **restart `DSH_AB.exe`**: a running older tray never notices the file, and its 重启
would silently not switch. (0.1.3 also closes dsh together with the tray, so a force-killed tray no longer leaves a
dsh holding the port.)

## Upgrading dsh is destructive - this order, no shortcuts

Upgrade -> re-apply patches -> re-run each patch's own verify step -> real browser turn in the inactive slot -> only then register.

- Read `docs\PLUGINS.md` first, and the patch tree that travels with this installation (`patches\` - your own maintenance state, not something the installer ships; create it if it is not there yet). A `pnpm install` or `dsh plugin add` restores pristine
  packages, so every patch in the ledger is gone: re-apply and re-verify each one. These are dsh's and its plugins'
  patches (this installation's own changes to them) - never DSH-AB's program files.
- A span like 0.1.5 -> 0.1.7 fails whole turns with `format v4 message requires a producer-owned source kind` (the
  V4 write gate accepts only producer-owned source kinds, and migration runs only when reading old sessions). The
  interface just says 本轮运行失败. Fix it in the inactive slot and keep the fix as a patch of your own (e.g. `patches\dsh-0.1.7-v4-message-source\`, a directory in this installation that you create and that travels with it).
- The old `settings.yaml` is imported exactly once; check that `agent-presets.default` reached the profile's
  `cordis.patch.yml` (0.1.7 defaults to `standard`, this installation wants `ptc`).

## Where things are

- `state\` holds the active-slot pointer and the pending switch; `dsh-ab.toml` is the user's (ports, log level).
- `logs\dsh-ab.log` is where an error shows up first - turn the level to full while working, and put it back
  before switching (to persist a level, edit `logs.level` and restart DSH-AB). `auto` records info/warn/error,
  `full` adds debug and the child's own output.
- The `docs\` ledgers are yours: `PLUGINS.md` (a row per plugin, one column asking whether upstream has
  fixed it - check that before any upgrade) and `TODO.md` (cleared each round, ticked as items finish).
- This skill belongs to `slot-<active>\data\skills\` while DSH_HOME follows the active slot, so it is gone after a
  switch: carry it with the rest of the data (invariant 6), or keep a copy the next round can restore.

## Updating DSH-AB itself

Do it in place here: a second installation directory is a different installation with its own data.

1. Take `DSH-AB-<DSH-AB version>-update.zip` from the Releases of `https://github.com/yemiao2745/DSH-AB`: one pack per
   DSH-AB version, this installation's program files, no slot in it.
2. Snapshot the installation root first - "before updating to <new version>": the rollback is that commit.
3. Expand the pack, then overwrite the program files. What the pack does not carry and is not the user's is the old
   version's leftovers and goes away (deleting directories alone leaves old top-level files behind), while the two
   slots, `dsh-ab.toml`, `state\`, the uninstaller's own files and the `docs\` ledgers stay - the pack never
   carries the ledgers. `DSH_AB.exe` may be running: copy over it directly; only a sharing violation means renaming
   it first, which leaves a `DSH_AB.exe.old` the running process holds open - expected. No process is ended and
   nothing restarts: the new tray takes effect on its next start.
4. Rollback: `git reset --hard <that snapshot>` - it tracks the program files and `docs\`, and also `dsh-ab.toml` and
   `state\` (the .gitignore leaves out only the two slots, `runtime\` and `logs\`).
