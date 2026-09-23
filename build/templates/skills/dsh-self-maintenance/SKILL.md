---
name: dsh-self-maintenance
description: 维护这套 DSH 安装本身时读：更新 dsh、升级 dsh、安装/更新/移除插件、修改 dsh 或其插件配置、打补丁、把改动同步到另一个槽、登记切槽（槽位切换）、维护安装根的插件与待办台账。只在你要动 dsh 自身时读它。
---

# Working on this DSH installation

Install root = `{{DSH_AB_ROOT}}` - if the directory was moved, take the one `DSH_AB.exe` sits in. Every change happens
inside it. The bundle carries its own Git under `runtime\git\` (find the exe inside it): DSH-AB runs no git command,
so the snapshots are yours.

## Invariants

1. **The active slot is left alone** - it serves the production port and holds the data in use. Changes go into the
   inactive slot, and nothing switches until the switch takes effect.
2. **Snapshot the installation root before you start and after you finish** (the bundled Git: add -A, commit; what
   and when is your judgement). That snapshot is what a rollback returns to.
3. **Prove the change in the inactive slot before switching**: start dsh there on a free port you pick yourself and
   tell the user which port that was. If it does not come up, nothing switches.
4. **The switch is the user's**: register it in the tray, or write it into `state\` the way this installation's
   current format does (that format is the authority), and the user clicks 重启; a registration can be withdrawn
   until then. Sync the data this conversation needs from the old slot first - where both sides have a file the
   inactive slot wins, and the last turns may not make it across: say so.

What to copy, in what order and which data to carry is your judgement.

## Where things are

- `state\` holds the active-slot pointer and the pending switch; `dsh-ab.toml` is the user's (ports, log level).
- `logs\dsh-ab.log` is where an error shows up first - turn the level to full while working, and put it back
  before switching (to persist a level, edit `logs.level` and restart DSH-AB).
- The `docs\` ledgers are yours: `PLUGINS.md` (a row per plugin, one column asking whether upstream has
  fixed it - check that before any upgrade) and `TODO.md` (cleared each round, ticked as items finish).
- This skill belongs to `slot-a\data\skills\` while DSH_HOME follows the active slot, so it is gone after a switch:
  carry it with the rest of the data (invariant 4), or keep a copy the next round can restore.

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
