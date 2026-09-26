"""进程枚举与结束：Toolhelp32 + QueryFullProcessImageNameW + 带超时运行。

旧工具链靠 Get-Process/Stop-Process/taskkill.exe；这里全部用 ctypes 直接调 kernel32，
所以验收工具不再依赖 powershell.exe 或 taskkill.exe。
"""

from __future__ import annotations

import ctypes
import os
import re
import subprocess
import time
from ctypes import wintypes
from pathlib import Path

_MAX_PATH = 260
_TH32CS_SNAPPROCESS = 0x00000002
_PROCESS_TERMINATE = 0x0001
_PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
_SYNCHRONIZE = 0x00100000
_WAIT_TIMEOUT = 0x00000102

_K32 = ctypes.WinDLL("kernel32", use_last_error=True)


class _PROCESSENTRY32W(ctypes.Structure):
    _fields_ = [
        ("dwSize", wintypes.DWORD),
        ("cntUsage", wintypes.DWORD),
        ("th32ProcessID", wintypes.DWORD),
        ("th32DefaultHeapID", ctypes.POINTER(ctypes.c_ulong)),
        ("th32ModuleID", wintypes.DWORD),
        ("cntThreads", wintypes.DWORD),
        ("th32ParentProcessID", wintypes.DWORD),
        ("pcPriClassBase", ctypes.c_long),
        ("dwFlags", wintypes.DWORD),
        ("szExeFile", wintypes.WCHAR * _MAX_PATH),
    ]


_K32.CreateToolhelp32Snapshot.restype = wintypes.HANDLE
_K32.CreateToolhelp32Snapshot.argtypes = [wintypes.DWORD, wintypes.DWORD]
_K32.Process32FirstW.argtypes = [wintypes.HANDLE, ctypes.POINTER(_PROCESSENTRY32W)]
_K32.Process32NextW.argtypes = [wintypes.HANDLE, ctypes.POINTER(_PROCESSENTRY32W)]
_K32.OpenProcess.restype = wintypes.HANDLE
_K32.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
_K32.QueryFullProcessImageNameW.argtypes = [
    wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR, ctypes.POINTER(wintypes.DWORD)
]
_K32.TerminateProcess.argtypes = [wintypes.HANDLE, wintypes.UINT]
_K32.WaitForSingleObject.argtypes = [wintypes.HANDLE, wintypes.DWORD]
_K32.CloseHandle.argtypes = [wintypes.HANDLE]


class ProcessTimeout(RuntimeError):
    """进程在限时内没有退出：静默运行里出现模态框时就是这样。"""


def iter_processes():
    """产出 (pid, 父 pid, 可执行文件名)，来自一份 Toolhelp32 快照。"""
    snapshot = _K32.CreateToolhelp32Snapshot(_TH32CS_SNAPPROCESS, 0)
    if snapshot == wintypes.HANDLE(-1).value or not snapshot:
        raise OSError("CreateToolhelp32Snapshot 失败，无法枚举进程")
    try:
        entry = _PROCESSENTRY32W()
        entry.dwSize = ctypes.sizeof(_PROCESSENTRY32W)
        if not _K32.Process32FirstW(snapshot, ctypes.byref(entry)):
            return
        while True:
            yield entry.th32ProcessID, entry.th32ParentProcessID, entry.szExeFile
            if not _K32.Process32NextW(snapshot, ctypes.byref(entry)):
                return
    finally:
        _K32.CloseHandle(snapshot)


def image_path(pid):
    """进程可执行文件的完整路径；拿不到（受保护或已退出）返回 None。"""
    handle = _K32.OpenProcess(_PROCESS_QUERY_LIMITED_INFORMATION, False, pid)
    if not handle:
        return None
    try:
        size = wintypes.DWORD(_MAX_PATH * 4)
        buffer = ctypes.create_unicode_buffer(size.value)
        if not _K32.QueryFullProcessImageNameW(handle, 0, buffer, ctypes.byref(size)):
            return None
        return buffer.value
    finally:
        _K32.CloseHandle(handle)


def pids_by_name(name):
    """按进程名（可带或不带 .exe，不分大小写）匹配的 pid 列表。"""
    wanted = name.lower()
    if wanted.endswith(".exe"):
        wanted = wanted[: -len(".exe")]
    return [pid for pid, _ppid, exe in iter_processes() if exe.lower().removesuffix(".exe") == wanted]


def pids_under(root):
    """可执行文件位于 root 之下的进程：(pid, 路径) 列表。"""
    prefix = os.path.normcase(str(root).rstrip("\\") + "\\")
    found = []
    for pid, _ppid, _exe in iter_processes():
        path = image_path(pid)
        if path and os.path.normcase(path).startswith(prefix):
            found.append((pid, path))
    return found


def is_alive(pid):
    handle = _K32.OpenProcess(_SYNCHRONIZE, False, pid)
    if not handle:
        return False
    try:
        return _K32.WaitForSingleObject(handle, 0) == _WAIT_TIMEOUT
    finally:
        _K32.CloseHandle(handle)


def _terminate(pid):
    handle = _K32.OpenProcess(_PROCESS_TERMINATE | _SYNCHRONIZE, False, pid)
    if not handle:
        return
    try:
        _K32.TerminateProcess(handle, 1)
    finally:
        _K32.CloseHandle(handle)


def kill_tree(pid):
    """结束 pid 及其全部后代（先叶子后根），不调用 taskkill。"""
    children = {}
    for child_pid, parent_pid, _exe in iter_processes():
        children.setdefault(parent_pid, []).append(child_pid)
    order = []
    _collect(pid, children, order)
    for victim in order:
        _terminate(victim)
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline and any(is_alive(victim) for victim in order):
        time.sleep(0.2)


def _collect(pid, children, order):
    for child in children.get(pid, ()):
        _collect(child, children, order)
    order.append(pid)


def run_bounded(argv, timeout_sec, capture=False, env=None):
    """运行一个进程，超时即结束整棵进程树并抛 ProcessTimeout。

    capture=True 时把 stdout+stderr 合并成文本返回，用于读 --version 这类回读。
    env=None 时继承本进程的环境；安装器/卸载器要一份能写 %TEMP% 的环境（见 scenarios._run_or_fail）。
    """
    argv = [str(arg) for arg in argv]
    kwargs = {
        "stdout": subprocess.PIPE if capture else subprocess.DEVNULL,
        "stderr": subprocess.STDOUT if capture else subprocess.DEVNULL,
        "env": env,
    }
    if capture:
        kwargs.update(text=True, encoding="utf-8", errors="replace")
    process = subprocess.Popen(argv, **kwargs)
    try:
        output, _ = process.communicate(timeout=timeout_sec)
    except subprocess.TimeoutExpired:
        kill_tree(process.pid)
        try:
            process.communicate(timeout=30)
        except Exception:
            pass
        raise ProcessTimeout(
            f"超时：{argv[0]} 在 {timeout_sec} 秒内没有退出（疑似模态框阻塞），其进程树已被结束"
        ) from None
    return subprocess.CompletedProcess(argv, process.returncode, output if capture else None)


def wait_for_uninstaller(directory, timeout_sec):
    """等 Inno 的卸载器真的干完活。

    unins000.exe 会把活儿交给 %TEMP% 里的一份拷贝后立刻返回，退出码说明不了文件删没删完；
    不等的话下一个场景就会和还在进行的删除赛跑。
    """
    uninstaller = Path(directory) / "unins000.exe"
    deadline = time.monotonic() + timeout_sec
    while time.monotonic() < deadline:
        busy = any(re.match(r"^(unins|_unins)", exe, re.IGNORECASE) for _pid, _ppid, exe in iter_processes())
        if not busy and not uninstaller.exists():
            time.sleep(0.8)
            return
        time.sleep(0.4)
    raise ProcessTimeout(f"卸载程序在 {timeout_sec} 秒内没有真正结束（{directory}）")
