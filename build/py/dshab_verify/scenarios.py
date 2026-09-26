"""5 个安装/卸载场景与逐条断言：verify-silent.ps1 的 Python 版本。

- 每个场景都从「清空目标目录、再断言它真的是空的」开始。
- 安装与卸载一律静默参数、一律限时；超时即结束整棵进程树（疑似模态框阻塞）。
- 运行前后对用户自己那份安装的可见状态（一个 HKCU 卸载登记项 + 最多两条快捷方式）做快照与还原：
  它们不属于本次运行，必须原样奉还（快捷方式逐字节，登记项按值名与类型）。
"""

from __future__ import annotations

import hashlib
import os
import re
import subprocess
import time
from dataclasses import dataclass
from pathlib import Path

from dshab_build import install_layout, paths, steplog

from . import verify_skill
from .tcp import PortHolder, is_free
from .win_proc import (
    ProcessTimeout,
    is_alive,
    kill_tree,
    pids_by_name,
    pids_under,
    run_bounded,
    wait_for_uninstaller,
)
from .win_reg import install_entry_key, key_exists, read_value, read_values, user_entry_key
from .win_reg import restore as restore_key
from .win_reg import snapshot as snapshot_key
from .win_rmtree import clear_and_assert_empty, clear_dir
from .win_shortcut import copy as copy_file
from .win_shortcut import desktop_dir, remove as remove_file, sha256, start_menu_programs_dir

# 构建产物：安装包里的 DSH_AB.exe 必须与它逐字节相同。
BUILT_EXE = Path("build") / "DSH_AB.exe"

# 随包文件：安装期把 {{DSH_AB_ROOT}} 烧成真实安装路径，正文必须与模板逐字一致。
BAKED_FILES = (
    (r"slot-a\data\skills\dsh-self-maintenance\SKILL.md", r"skills\dsh-self-maintenance\SKILL.md"),
    (r"docs\PLUGINS.md", r"docs\PLUGINS.md"),
    (r"docs\TODO.md", r"docs\TODO.md"),
)

# 安装期不再放 AGENTS.md，也不做任何 git 操作。
FORBIDDEN_PATHS = (
    "AGENTS.md",
    ".git",
    r"slot-a\data\AGENTS.md",
    r"slot-b\data\AGENTS.md",
    r"slot-a\.git",
    r"slot-b\.git",
    r"docs\AGENTS.md",
)

DISPLAY_NAME_RE = re.compile(r"^DSH-AB( \([0-9]+\)| \([0-9a-f]{12}\))?$")
TOML_PORT_RE = re.compile(r"^\s*(production|test)\s*=\s*(\d+)\s*$")


class VerifyError(RuntimeError):
    """验收失败：任何一条断言不过即抛，退出码由入口 verify.py 变成非 0。"""


@dataclass
class Config:
    repo_root: Path
    install_dir: Path
    setup: Path
    log_dir: Path
    manifest: dict
    port: int = 3190
    timeout_sec: int = 900
    refuse_sec: int = 300

    @property
    def recorded_files(self):
        """构建期记录（payload 清单里的 install_files）：该装进安装根的文件只有这一条来源。"""
        return list(self.manifest.get("install_files") or [])


def run(cfg, rep):
    """跑完 5 个场景；用户可见状态无论如何都要还原。"""
    local_appdata = os.environ.get("LOCALAPPDATA")
    if not local_appdata:
        raise VerifyError(
            "环境变量 LOCALAPPDATA 没有设置：工作区外痕迹的两条检查（%LOCALAPPDATA%\\DSH-AB 与 "
            "%LOCALAPPDATA%\\Programs\\DSH-AB）在这一轮无法判定，场景 5 不能跑。"
            "请在正常登录的 Windows 会话里重跑，或先设置 LOCALAPPDATA。"
        )
    PREFLIGHT(rep)
    outside = [
        Path(local_appdata) / "DSH-AB",
        Path(local_appdata) / "Programs" / "DSH-AB",
    ]
    preexisting = {}
    for path in outside:
        preexisting[path] = _outside_snapshot(path)
        if preexisting[path] is not None:
            rep.note(f"运行前已存在（不是本次运行的痕迹）：{path}（{preexisting[path]}）")

    if not is_free(cfg.port):
        rep.note(f"注意：127.0.0.1:{cfg.port} 现在被占用，安装类的场景会因此被拒（用 --port 换一个空闲端口）")

    # 随包策略（用户约束）：载荷/安装根里不提供任何工具，只靠随包 skill 软性引导。
    # 这一条只看构建产物，不依赖安装；装完后的同一检查见 1.x。
    tools_dir = paths.ROOT / "payload" / "tools"
    rep.record("1.0", not tools_dir.exists(),
               "载荷里没有 tools 目录（随包不提供任何工具）" if not tools_dir.exists()
               else f"载荷里不该有 tools 目录：{tools_dir}")
    stray = sorted(
        q.name for q in (paths.ROOT / "payload").glob("*")
        if q.is_file() and q.suffix.lower() in {".py", ".ps1", ".bat", ".cmd", ".mjs", ".js", ".sh"}
    )
    rep.record("1.0b", not stray,
               "载荷根没有脚本文件" if not stray else f"载荷根不该有脚本：{stray}")

    dshab_before = pids_by_name("DSH_AB")
    if dshab_before:
        rep.note(f"运行前已有 DSH_AB.exe 在跑（不是本次运行的痕迹）：PID {_pids(dshab_before)}")

    state = UserState(cfg, rep)
    state.save()

    body_error = None
    try:
        _scenario_1(cfg, rep, dshab_before)
        _scenario_2(cfg, rep)
        _scenario_3(cfg, rep)
        _scenario_4(cfg, rep)
        _scenario_5(cfg, rep, outside, preexisting)
    except BaseException as exc:  # 失败也要还原用户状态，然后再把失败抛出去
        body_error = exc

    restore_error = None
    try:
        state.restore()
    except BaseException as exc:
        restore_error = exc

    if restore_error is not None:
        message = f"用户可见状态没有还原干净：{restore_error}"
        if body_error is not None:
            message = f"{body_error}\n{message}"
        rep.record("用户可见状态已还原", False, message)
        raise VerifyError(message) from restore_error
    rep.record("用户可见状态已还原", True, "快捷方式逐字节，卸载登记项按快照")
    if body_error is not None:
        raise body_error


# ---- 环境预检：这一轮能不能跑 ---------------------------------------------------------------

def _preflight_environment(rep):
    """跑场景之前先证明这台机器允许跑；不成立就一条场景都不跑。

    两件事必须先成立：Inno 的加载器要能在自己的临时目录里建目录，安装器要能往开始菜单和桌面写
    快捷方式。不成立时安装器在能写日志之前就退出，报告里只剩「退出码 1」和一个从未出现的日志
    路径——环境问题会被读成产品问题。所以先问清楚，再决定跑不跑。

    临时目录问的是安装器实际会看到的那个（工具链把它指到项目内的 scratch 目录，见
    steplog.tool_env），不是系统 %TEMP%：跑起来的是前者。
    """
    scratch_temp = Path(steplog.tool_env()["TEMP"])
    rep.note(f"安装器/卸载器的 TEMP 指向 {scratch_temp}（系统 %TEMP% 不是它们看到的那个）")
    problems = []
    for label, why, directory in (
        ("%TEMP%", "安装器的加载器要在里面建自己的临时目录", scratch_temp),
        (r"开始菜单\程序", "两条快捷方式要写这里", start_menu_programs_dir()),
        ("桌面", "两条快捷方式要写这里", desktop_dir()),
    ):
        error = None
        try:
            _probe_writable(directory)
        except OSError as exc:
            error = exc
            problems.append(f"{label} {directory}：{exc}")
        rep.record(f"环境预检：{label} 可写（{why}）", error is None,
                   str(directory) if error is None else f"{directory} 不可写：{error}")
    if problems:
        raise VerifyError(
            "环境挡住了这一轮，不是产品的问题（environment blocks this run, not the product）："
            + "；".join(problems)
            + "。请在有可写临时目录和可写开始菜单/桌面的会话里重跑（例如 CI runner）。"
        )


# run() 只通过这个名字调用预检：真实预检是默认值，测试可把它换成 lambda rep: None
# 来单独练编排（保存/还原/抛出），不必碰文件系统。
PREFLIGHT = _preflight_environment


def _probe_writable(directory):
    """在那个目录里真建一个文件和一个子目录（Inno 的加载器要建自己的临时目录），再删掉。

    建不出来就把 OSError 抛给调用方，由它记成「环境不满足」。只查权限位不算数：真写一次才知道。
    """
    stamp = f".dshab-write-probe-{os.getpid()}"
    probes = (Path(directory) / stamp, Path(directory) / (stamp + ".tmp"))
    try:
        probes[0].write_bytes(b"probe")
        probes[1].mkdir()
    finally:
        for probe in probes:
            try:
                if probe.is_dir():
                    probe.rmdir()
                elif probe.exists():
                    probe.unlink()
            except OSError:
                pass


# ---- 场景 1：静默安装 + 静默卸载（默认保留用户数据） -----------------------------------------

def _scenario_1(cfg, rep, dshab_before):
    rep.section("[1/5] 静默安装 + 静默卸载（默认保留用户数据）")
    clear_and_assert_empty(cfg.install_dir)

    install_log = cfg.log_dir / "s1-install.log"
    argv = _install_argv(cfg, install_log, cfg.install_dir)
    result, command = _run_or_fail(rep, "1.1 静默安装退出码 0", argv, cfg.timeout_sec, install_log)
    _check(rep, "1.1 静默安装退出码 0", result.returncode == 0,
           f"退出码 {result.returncode}", command, install_log)

    payload_files = install_layout.install_root_files(cfg.repo_root / "payload")
    installed_files = install_layout.install_root_files(cfg.install_dir, ignore=["unins*"])
    installed_diff = install_layout.compare_file_sets(cfg.recorded_files, installed_files)
    if installed_diff["missing"]:
        _fail(rep, "1.2 安装根与构建期记录逐个对上",
              f"构建期记录里有 {len(installed_diff['missing'])} 个文件没装进安装根："
              f"{install_layout.format_diff(installed_diff['missing'])}")
    if installed_diff["unexpected"]:
        _fail(rep, "1.2 安装根与构建期记录逐个对上",
              f"安装根里有 {len(installed_diff['unexpected'])} 个构建期记录里没有的文件："
              f"{install_layout.format_diff(installed_diff['unexpected'])}")
    _check(rep, "1.2 安装根与构建期记录逐个对上", True,
           f"安装根 {len(installed_files)} 个文件（载荷 {len(payload_files)} 个，记录 {len(cfg.recorded_files)} 个）")

    payload_diff = install_layout.compare_file_sets(cfg.recorded_files, payload_files)
    if payload_diff["missing"]:
        _fail(rep, "1.3 payload 与构建期记录一致",
              f"payload 比构建期记录少 {len(payload_diff['missing'])} 个文件（构建后删过？）："
              f"{install_layout.format_diff(payload_diff['missing'])}")
    if payload_diff["unexpected"]:
        _fail(rep, "1.3 payload 与构建期记录一致",
              f"payload 里有 {len(payload_diff['unexpected'])} 个构建期记录里没有的文件（构建后加的？没重编安装包就不会进安装根）："
              f"{install_layout.format_diff(payload_diff['unexpected'])}")
    _check(rep, "1.3 payload 与构建期记录一致", True, f"{len(payload_files)} 个文件")

    for name in ("state", "logs"):
        _check(rep, f"1.4 安装根有 {name} 目录", (cfg.install_dir / name).is_dir(), str(cfg.install_dir / name))

    present = [name for name in FORBIDDEN_PATHS if (cfg.install_dir / name).exists()]
    _check(rep, "1.5 安装根里没有 AGENTS.md / git 残留", not present,
           f"不该存在的东西出现了：{'; '.join(present)}")

    slot_b = cfg.install_dir / "slot-b"
    if not slot_b.is_dir():
        _fail(rep, "1.6 slot-b 存在且为空", "缺少空的 slot-b 目录")
    slot_b_entries = list(slot_b.iterdir())
    _check(rep, "1.6 slot-b 存在且为空", not slot_b_entries, f"实际有 {len(slot_b_entries)} 项")

    templates = cfg.repo_root / "build" / "templates"
    for relative, template_relative in BAKED_FILES:
        installed = cfg.install_dir / relative
        if not installed.is_file():
            _fail(rep, f"1.7 {relative} 与模板逐字一致", f"缺少文件：{installed}")
        head = installed.read_bytes()[:3]
        if head == b"\xef\xbb\xbf":
            _fail(rep, f"1.7 {relative} 与模板逐字一致", "被写回了 UTF-8 BOM（dsh 要求文件以 --- 开头）")
        expected = (templates / template_relative).read_bytes().decode("utf-8-sig").replace(
            "{{DSH_AB_ROOT}}", str(cfg.install_dir))
        got = installed.read_bytes().decode("utf-8-sig")
        if got != expected:
            if "{{" in got:
                _fail(rep, f"1.7 {relative} 与模板逐字一致", "里还有未替换的 {{...}} 占位符（安装期没有烧入绝对路径）")
            _fail(rep, f"1.7 {relative} 与模板逐字一致", "与「模板替换占位符后」的内容不一致：安装期读写它时改动了正文")
    _check(rep, "1.7 三个随包文件与模板逐字一致且都已烧入安装路径", True, str(cfg.install_dir))

    for label, skills_root in (
        ("1.8 skill 合规（模板）", templates / "skills"),
        ("1.9 skill 合规（安装根）", cfg.install_dir / "slot-a" / "data" / "skills"),
    ):
        code, lines = verify_skill.check_root(skills_root, cfg.repo_root)
        for line in lines:
            rep.note(line)
        _check(rep, label, code == 0, f"退出码 {code}（{skills_root}）")

    built_exe = cfg.repo_root / BUILT_EXE
    if not built_exe.is_file():
        _fail(rep, "1.10 安装包里的 DSH_AB.exe 与构建产物一致", f"找不到构建产物：{built_exe}")
    installed_exe = cfg.install_dir / "DSH_AB.exe"
    if not installed_exe.is_file():
        _fail(rep, "1.10 安装包里的 DSH_AB.exe 与构建产物一致", f"安装根里没有 DSH_AB.exe：{installed_exe}")
    hash_built = sha256(built_exe)
    hash_installed = sha256(installed_exe)
    if hash_built != hash_installed:
        _fail(rep, "1.10 安装包里的 DSH_AB.exe 与构建产物一致",
              f"不一致（build {hash_built} / 安装根 {hash_installed}）：payload 过期")
    _check(rep, "1.10 安装包里的 DSH_AB.exe 与构建产物一致", True, hash_installed)

    manifest_hash = cfg.manifest.get("exe_sha256")
    if not manifest_hash:
        _fail(rep, "1.11 payload 清单记的 exe 哈希一致",
              "payload 清单里没有记 exe 哈希（exe_sha256）：这一条判不了，payload 要重跑一次构建")
    if manifest_hash != hash_built.lower():
        _fail(rep, "1.11 payload 清单记的 exe 哈希一致",
              f"清单 {manifest_hash} 与 {built_exe} 的 {hash_built.lower()} 不一致（payload 过期）")
    _check(rep, "1.11 payload 清单记的 exe 哈希一致", True,
           f"清单与 {built_exe} 都是 {hash_built.lower()}")

    versions = [str(value) for value in (cfg.manifest.get("dshab_version"), cfg.manifest.get("dsh_version")) if value]
    if len(versions) < 2:
        _fail(rep, "1.12 装出来的 exe 报的版本含两个版本号",
              f"payload 清单里的版本号不全：{cfg.manifest.get('dshab_version')} / {cfg.manifest.get('dsh_version')}")
    version_run, command = _run_or_fail(
        rep, "1.12 装出来的 exe 报的版本含两个版本号",
        [cfg.install_dir / "DSH_AB.exe", "--version"], 60, capture=True)
    reported = (version_run.stdout or "").strip()
    absent = [value for value in versions if value not in reported]
    _check(rep, "1.12 装出来的 exe 报的版本含两个版本号", not absent,
           f"--version 输出：{reported}（缺 {absent}）", command)

    new_pids = [pid for pid in pids_by_name("DSH_AB") if pid not in dshab_before]
    _check(rep, "1.13 静默安装没有启动新的 DSH_AB.exe", not new_pids,
           f"新 PID {_pids(new_pids)}（桌面会多出托盘图标）")

    git = cfg.install_dir / "runtime" / "git" / "cmd" / "git.exe"
    if not git.is_file():
        _fail(rep, "1.14 随包 git 可用", f"安装根里没有随包 git：{git}")
    git_run, command = _run_or_fail(rep, "1.14 随包 git 可用", [git, "--version"], 60, capture=True)
    _check(rep, "1.14 随包 git 可用", git_run.returncode == 0,
           (git_run.stdout or "").strip(), command)

    toml = cfg.install_dir / "dsh-ab.toml"
    if not toml.is_file():
        _fail(rep, "1.15 dsh-ab.toml 的端口来自命令行", f"缺少配置文件：{toml}")
    ports = {}
    for line in toml.read_text(encoding="utf-8").splitlines():
        match = TOML_PORT_RE.match(line)
        if match:
            ports[match.group(1)] = int(match.group(2))
    if "test" in ports:
        _fail(rep, "1.15 dsh-ab.toml 的端口来自命令行", f"还有测试端口那一行：test = {ports['test']}")
    _check(rep, "1.15 dsh-ab.toml 的端口来自命令行", ports.get("production") == cfg.port,
           f"dsh-ab.toml 里是 production={ports.get('production')}，期望 {cfg.port}")

    entry_key = install_entry_key(cfg.install_dir)
    if not key_exists(entry_key):
        _fail(rep, "1.16 这次安装有自己的卸载登记项", f"没找到：{entry_key}")
    _check(rep, "1.16 这次安装有自己的卸载登记项", True, entry_key)

    estimated_kb = read_value(entry_key, "EstimatedSize")
    actual_bytes = _dir_size(cfg.install_dir)
    ok = (isinstance(estimated_kb, int) and estimated_kb > 0
          and actual_bytes * 0.9 <= estimated_kb * 1024 <= actual_bytes * 1.1)
    _check(rep, "1.17 EstimatedSize 与安装目录实际体积相符", ok,
           f"登记项 {estimated_kb} KB，实测 {actual_bytes / 1024 / 1024:.1f} MB")

    display_name = read_value(entry_key, "DisplayName")
    ok = isinstance(display_name, str) and DISPLAY_NAME_RE.match(display_name) is not None
    _check(rep, "1.18 DisplayName 形状正确", ok,
           f"DisplayName = {display_name}（必须是 DSH-AB / DSH-AB (端口) / DSH-AB (tag) 之一）")
    dshab_version = str(cfg.manifest.get("dshab_version") or "")
    _check(rep, "1.19 DisplayName 里没有版本号", dshab_version not in display_name,
           f"DisplayName = {display_name}，版本只该在 DisplayVersion 里")
    display_version = read_value(entry_key, "DisplayVersion")
    _check(rep, "1.20 DisplayVersion 等于清单版本", display_version == dshab_version,
           f"DisplayVersion = {display_version}，期望 {dshab_version}")

    links = []
    for value in ("DshAbStartMenuLink", "DshAbDesktopLink"):
        recorded = read_value(entry_key, value)
        if not recorded:
            _fail(rep, f"1.21 登记项记录了 {value}", f"没写：DSH-AB 就找不到自己那条快捷方式（{entry_key}）")
        links.append(str(recorded))
    if not Path(links[0]).is_file():
        _fail(rep, "1.21 登记项记录的开始菜单快捷方式存在", f"不存在：{links[0]}")
    _check(rep, "1.21 登记项记录的两条快捷方式可用", True, " | ".join(links))

    marker = cfg.install_dir / "slot-a" / "data" / "user-session.txt"
    marker.write_text("keep me", encoding="ascii")
    data_root = cfg.install_dir / "slot-a" / "data"
    installed_data = sorted(path.relative_to(cfg.install_dir) for path in data_root.rglob("*") if path.is_file())
    rep.note(f"slot-a\\data 里的文件（卸载后必须一个不少）：{len(installed_data)} 个")

    node_exe = cfg.install_dir / "slot-a" / "node" / "node.exe"
    if not node_exe.is_file():
        _fail(rep, "1.22 替身进程在本安装根下运行", f"找不到随包 node：{node_exe}")
    stand_in = subprocess.Popen(
        [str(node_exe), "-e", "setInterval(function(){},1000)"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        creationflags=subprocess.CREATE_NO_WINDOW)
    try:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline and not is_alive(stand_in.pid):
            time.sleep(0.2)
        if not is_alive(stand_in.pid):
            _fail(rep, "1.22 替身进程在本安装根下运行",
                  f"替身进程没有起来（PID {stand_in.pid}），这一轮证明不了卸载会结束本安装的进程")
        _check(rep, "1.22 替身进程在本安装根下运行", True, f"PID {stand_in.pid}：{node_exe}")

        uninstaller = cfg.install_dir / "unins000.exe"
        _check(rep, "1.23 卸载程序存在", uninstaller.is_file(), str(uninstaller))
        uninstall_log = cfg.log_dir / "s1-uninstall.log"
        argv = [uninstaller, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", f"/LOG={uninstall_log}"]
        result, command = _run_or_fail(rep, "1.24 静默卸载退出码 0", argv, cfg.timeout_sec, uninstall_log)
        _check(rep, "1.24 静默卸载退出码 0", result.returncode == 0,
               f"退出码 {result.returncode}", command, uninstall_log)

        try:
            wait_for_uninstaller(cfg.install_dir, cfg.timeout_sec)
        except ProcessTimeout as exc:
            _fail(rep, "1.25 卸载程序真的结束", str(exc))
        _check(rep, "1.25 卸载程序真的结束", True, "unins* 进程已退出且 unins000.exe 已消失")

        _check(rep, "1.26 卸载结束了本安装根下的进程", not is_alive(stand_in.pid),
               f"PID {stand_in.pid} 还在：结束进程那段没有跑成")
        leftovers = pids_under(cfg.install_dir)
        _check(rep, "1.27 本安装根下没有残留进程", not leftovers,
               "; ".join(f"{pid} {path}" for pid, path in leftovers))

        for index, link in enumerate(links, start=1):
            _check(rep, f"1.28 卸载删掉了自己的快捷方式 #{index}", not Path(link).exists(),
                   f"{link} 还在（要按登记项里记的真实路径删）")
        _check(rep, "1.29 卸载后 DSH_AB.exe 已删", not (cfg.install_dir / "DSH_AB.exe").exists(),
               str(cfg.install_dir / "DSH_AB.exe"))
        _check(rep, "1.30 卸载保留了用户数据 marker", marker.is_file(), str(marker))
        gone = [str(relative) for relative in installed_data if not (cfg.install_dir / relative).exists()]
        _check(rep, "1.31 卸载没删掉用户数据里的任何文件", not gone, "; ".join(gone))
    finally:
        if is_alive(stand_in.pid):
            kill_tree(stand_in.pid)


# ---- 场景 2：/DELETEUSERDATA=1 那一路 --------------------------------------------------------

def _scenario_2(cfg, rep):
    rep.section("[2/5] 静默卸载 /DELETEUSERDATA=1：槽内用户数据被删、安装根不留东西")
    clear_and_assert_empty(cfg.install_dir)
    install_log = cfg.log_dir / "s2-wipe-install.log"
    argv = _install_argv(cfg, install_log, cfg.install_dir)
    result, command = _run_or_fail(rep, "2.1 静默安装退出码 0", argv, cfg.timeout_sec, install_log)
    _check(rep, "2.1 静默安装退出码 0", result.returncode == 0,
           f"退出码 {result.returncode}", command, install_log)

    wipe_marker = cfg.install_dir / "slot-a" / "data" / "wipe-me.txt"
    wipe_marker.write_text("delete me", encoding="ascii")

    uninstaller = cfg.install_dir / "unins000.exe"
    _check(rep, "2.2 卸载程序存在", uninstaller.is_file(), str(uninstaller))
    uninstall_log = cfg.log_dir / "s2-wipe-uninstall.log"
    argv = [uninstaller, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/DELETEUSERDATA=1",
            f"/LOG={uninstall_log}"]
    result, command = _run_or_fail(rep, "2.3 卸载退出码 0", argv, cfg.timeout_sec, uninstall_log)
    _check(rep, "2.3 卸载退出码 0", result.returncode == 0,
           f"退出码 {result.returncode}", command, uninstall_log)

    try:
        wait_for_uninstaller(cfg.install_dir, cfg.timeout_sec)
    except ProcessTimeout as exc:
        _fail(rep, "2.4 卸载程序真的结束", str(exc))
    _check(rep, "2.4 卸载程序真的结束", True, "unins* 进程已退出且 unins000.exe 已消失")

    _check(rep, "2.5 卸载器删掉了用户数据", not wipe_marker.exists(),
           f"{wipe_marker} 还在（/DELETEUSERDATA=1 没有被 [Code] 读到？）")
    left = _entries(cfg.install_dir)
    _check(rep, "2.6 卸载后安装根里不留东西", not left,
           f"还剩 {len(left)} 项：{left[0] if left else ''}")
    entry_key = install_entry_key(cfg.install_dir)
    _check(rep, "2.7 卸载后卸载登记项已删", not key_exists(entry_key), entry_key)


# ---- 场景 3：非空目标目录必须被静默拒绝 -------------------------------------------------------

def _scenario_3(cfg, rep):
    rep.section("[3/5] 非空目录 + 静默参数：必须立即以非 0 退出码结束，不弹框")
    # 目标目录故意带空格：/DIR=<带空格的路径> 的加引号规则在这里被真跑一遍——路径被拆坏时安装器
    # 看到的不是这个目录，就不会因为「非空」被拒，3.1 立刻变红。
    target = _spaced_install_dir(cfg)
    rep.note(f"目标目录（含空格）：{target}")
    clear_dir(target)
    target.mkdir(parents=True, exist_ok=True)
    pre_existing = target / "pre-existing.txt"
    pre_existing.write_text("not mine", encoding="ascii")

    refuse_log = cfg.log_dir / "s3-nonempty.log"
    if refuse_log.exists():
        refuse_log.unlink()
    argv = _install_argv(cfg, refuse_log, target)
    result, command = _run_or_fail(rep, "3.1 非空目录被拒（非 0 退出码）", argv, cfg.refuse_sec, refuse_log)
    _check(rep, "3.1 非空目录被拒（非 0 退出码）", result.returncode != 0,
           f"退出码 {result.returncode}（非空目录竟然安装成功了）", command, refuse_log)

    if not refuse_log.is_file():
        _fail(rep, "3.2 拒绝时写了日志", f"没有写日志：{refuse_log}")
    _check(rep, "3.2 拒绝时写了日志", True, str(refuse_log), log=refuse_log)
    text = refuse_log.read_text(encoding="utf-8", errors="replace")
    _check(rep, "3.3 日志里有拒绝记录", "target directory is not empty" in text,
           f"日志里没有拒绝记录：{refuse_log}", log=refuse_log)
    _check(rep, "3.4 已存在的文件原样保留", pre_existing.is_file(), str(pre_existing))
    _check(rep, "3.5 非空目录里没有被装进文件", not (target / "DSH_AB.exe").exists(),
           str(target / "DSH_AB.exe"))


# ---- 场景 4：端口被占用必须被静默拒绝 ---------------------------------------------------------

def _scenario_4(cfg, rep):
    rep.section("[4/5] 生产端口被占用 + 静默参数：必须被查，且立即以非 0 退出码结束、目标目录不留文件")
    clear_and_assert_empty(cfg.install_dir)
    try:
        holder = PortHolder(cfg.port).start()
    except OSError as exc:
        _fail(rep, "4.0 生产端口能被占住", f"{cfg.port} 现在绑定不上（{exc}）：换一个空闲端口再来（--port）")
    try:
        rep.note(f"占用生产端口 {holder.host}:{holder.port}")
        port_log = cfg.log_dir / "s4-port-taken.log"
        if port_log.exists():
            port_log.unlink()
        argv = _install_argv(cfg, port_log, cfg.install_dir)
        result, command = _run_or_fail(rep, "4.1 端口被占用时被拒（非 0 退出码）", argv, cfg.refuse_sec, port_log)
        _check(rep, "4.1 端口被占用时被拒（非 0 退出码）", result.returncode != 0,
               f"端口 {cfg.port} 已被占用，安装却成功了（退出码 {result.returncode}）", command, port_log)

        if not port_log.is_file():
            _fail(rep, "4.2 拒绝时写了日志", f"没有写日志：{port_log}")
        _check(rep, "4.2 拒绝时写了日志", True, str(port_log), log=port_log)
        text = port_log.read_text(encoding="utf-8", errors="replace")
        _check(rep, "4.3 日志里有中文的端口占用说明", "已被占用" in text,
               f"静默安装时那是唯一的说明：{port_log}", log=port_log)
        left = _entries(cfg.install_dir)
        _check(rep, "4.4 被拒绝的安装没有留下文件", not left,
               f"还剩 {len(left)} 项：{left[0] if left else ''}")
    finally:
        holder.stop()


# ---- 场景 5：本次运行不得在工作区外留下痕迹 ---------------------------------------------------

def _outside_snapshot(path):
    r"""工作区外一条路径「现在的样子」：不存在返回 None，否则是它顶层的名字与类型（文件再加大小）。

    只记「在不在」在本来就有 DSH-AB 的机器上永远为真（5.1 就成了句废话）；记下顶层有什么，这一轮
    造了东西没有才说得出来。安装器留下的痕迹就在这一层——目录本身，或者一个新条目——所以只比顶层
    的名字、类型和文件大小。目录的修改时间与深处的内容都不看：%LOCALAPPDATA%\DSH-AB 里是用户正在
    跑的那份 DSH-AB 自己的数据，它一直在深处写，连顶层子目录的修改时间都会被它带动；拿这些算指纹，
    这一条在装了 DSH-AB 的机器上会变成必然的假失败——那是把环境噪声又报成产品问题，正是这一轮要修
    掉的东西。
    """
    path = Path(path)
    if path.is_dir():
        items = []
        for entry in path.iterdir():
            if entry.is_dir():
                items.append(f"{entry.name} 目录")
                continue
            try:
                size = entry.stat().st_size
            except OSError:
                items.append(f"{entry.name} 文件 读不到")
                continue
            items.append(f"{entry.name} 文件 {size}")
        digest = hashlib.sha256("\n".join(sorted(items)).encode("utf-8")).hexdigest()[:16]
        return f"目录：顶层 {len(items)} 项，指纹 {digest}"
    if path.is_file():
        stat = path.stat()
        return f"文件：{stat.st_size} 字节，改动时间 {stat.st_mtime_ns}"
    return None


def _outside_residue(before, after, path):
    """这一条该不该红。before 是本轮开始时的快照，after 是运行后的同一份；不一致就返回原因。

    True / False 是「只记了运行前在不在」的粗快照，也接受：那样判不了改动，就只判存在性。
    """
    if before is None or before is False:
        return None if after is None else f"工作区外留下了安装痕迹：{path}（{after}）"
    if before is True:
        return None if after is not None else f"运行前存在的东西现在不见了：{path}"
    return None if after == before else f"运行前就存在的东西被这一轮改动了：{path}（{before} → {after}）"


def _scenario_5(cfg, rep, outside, preexisting):
    rep.section("[5/5] 清理：本次运行不得在工作区外留下安装痕迹")
    clear_dir(cfg.install_dir)
    clear_dir(_spaced_install_dir(cfg))
    for index, path in enumerate(outside, start=1):
        name = f"5.{index} 工作区外没有安装痕迹：{path}"
        before = preexisting[path]
        after = _outside_snapshot(path)
        residue = _outside_residue(before, after, path)
        _check(rep, name, residue is None, residue or f"与运行前一致（{after or '不存在'}）")


# ---- 用户自己那份安装的可见状态 ---------------------------------------------------------------

class UserState:
    """用户已有的卸载登记项与快捷方式：先快照，跑完再逐字节/逐值还回去。"""

    def __init__(self, cfg, rep):
        self.rep = rep
        self.dir = cfg.log_dir / "user-state"
        self.entries = []  # [(快捷方式路径, 备份文件或 None)]
        self.saved = None
        self.key = user_entry_key()

    def save(self):
        self.dir.mkdir(parents=True, exist_ok=True)
        recorded = read_values(self.key) or {}
        paths = []
        for value, base in (
            ("DshAbStartMenuLink", start_menu_programs_dir()),
            ("DshAbDesktopLink", desktop_dir()),
        ):
            link = recorded.get(value, (None, None))[1]
            if link:
                paths.append(Path(link))
            elif recorded.get("DisplayName", (None, None))[1]:
                paths.append(base / (recorded["DisplayName"][1] + ".lnk"))
        for index, path in enumerate(paths):
            backup = None
            if path.is_file():
                backup = self.dir / f"shortcut{index}.lnk"
                copy_file(path, backup)
                self.rep.note(f"已快照快捷方式：{path}")
            self.entries.append((path, backup))
        if self.entries:
            self.rep.note(f"快捷方式保护清单 {len(self.entries)} 条：" + " | ".join(str(p) for p, _ in self.entries))
        else:
            self.rep.note("这台机器没有既有的卸载登记项，没有快捷方式需要保护")
        self.saved = snapshot_key(self.key)
        if self.saved is None:
            self.rep.note(f"运行前没有卸载登记项，运行后要把它删掉：{self.key}")
        else:
            self.rep.note(f"已快照卸载登记项：{self.key}")

    def restore(self):
        problems = []
        try:
            restore_key(self.key, self.saved)
        except OSError as exc:
            problems.append(str(exc))
        for path, backup in self.entries:
            if backup is None:
                if path.exists():
                    remove_file(path)
            # 先比哈希再决定写不写：用户自己的快捷方式通常原样没动过，无条件回写只会在没权限时多
            # 报一个假失败——还原是安全网，不该自己制造失败。
            elif not path.is_file() or sha256(path) != sha256(backup):
                copy_file(backup, path)
                if sha256(path) != sha256(backup):
                    problems.append(f"快捷方式还原后与快照不一致：{path}")
        if problems:
            raise VerifyError("；".join(problems))


# ---- 小工具 ----------------------------------------------------------------------------------

def _install_argv(cfg, log, target):
    r"""静默安装的命令行；路径原样给出，加引号交给 Python（subprocess.list2cmdline）。

    自己摘引号在带空格的路径上会出错（/DIR=C:\a b 会被拆成两个参数），而先加引号再交给 Popen 也
    不行：它会把元素里的引号再转义一遍，安装器收到的是带引号的字面值。
    """
    return [
        cfg.setup,
        "/VERYSILENT",
        "/SUPPRESSMSGBOXES",
        "/NORESTART",
        f"/LOG={log}",
        f"/DIR={target}",
        f"/PRODUCTION={cfg.port}",
    ]


def _spaced_install_dir(cfg):
    """名字里带空格的安装目录：命令行加引号那条规则在这里被真跑一遍（见 _scenario_3）。"""
    return cfg.install_dir.parent / (cfg.install_dir.name + " with space")


def _command(argv):
    return " ".join(str(arg) for arg in argv)


def _run_or_fail(rep, name, argv, seconds, log=None, capture=False):
    """跑一个安装/卸载进程；超时（疑似模态框）即记失败并抛出。

    子进程带上构建工具链那一份环境（steplog.tool_env）：它的 %TEMP% 指向项目内的 scratch 目录。
    %TEMP% 不能被写的会话里，Inno 的加载器正是在那里建不出自己的临时目录，于是连日志都没写就退出了。
    """
    try:
        return run_bounded(argv, seconds, capture=capture, env=steplog.tool_env()), _command(argv)
    except ProcessTimeout as exc:
        _fail(rep, name, str(exc), _command(argv), log)


def _entries(root):
    """目录下所有条目（含隐藏），目录不存在按空算。"""
    root = Path(root)
    return list(root.rglob("*")) if root.is_dir() else []


def _dir_size(root):
    root = Path(root)
    if not root.is_dir():
        return 0
    return sum(path.stat().st_size for path in root.rglob("*") if path.is_file())


def _pids(pids):
    return ", ".join(str(pid) for pid in pids)


def _log_evidence(detail, log):
    """日志不存在就别把它当证据，改在 detail 里说明。

    安装器的加载器在能打开日志之前就可能退出（见 _preflight_environment）；报告里引一个从未出现的
    路径，读起来像安装器坏了，而不像环境不让写。
    """
    if log is None or Path(log).is_file():
        return detail, log
    note = f"没有日志可看（{log} 不存在：这一步在能写出日志之前就失败了）"
    return (f"{detail}；{note}" if detail else note), None


def _check(rep, name, ok, detail="", command=None, log=None):
    detail, log = _log_evidence(detail, log)
    if not rep.record(name, ok, detail, command=command, log=log):
        raise VerifyError(f"{name}：{detail}")


def _fail(rep, name, detail, command=None, log=None):
    detail, log = _log_evidence(detail, log)
    rep.record(name, False, detail, command=command, log=log)
    raise VerifyError(f"{name}：{detail}")
