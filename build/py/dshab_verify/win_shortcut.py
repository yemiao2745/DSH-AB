"""快捷方式：存在性、sha256、复制、删除，以及两条快捷方式所在的已知文件夹。

旧工具链用 Copy-Item/Get-FileHash/Remove-Item 和 [Environment]::GetFolderPath；
这里用 shutil 与 shell32 的 SHGetKnownFolderPath（桌面可能被重定向到 OneDrive，
所以不能拿 %USERPROFILE%\\Desktop 当桌面）。
"""

from __future__ import annotations

import ctypes
import os
import shutil
import stat
import uuid
from ctypes import wintypes
from pathlib import Path

# 文件级 sha256 只留一份实现（构建包的 hashutil，scenarios.py 本来就依赖 dshab_build）；这里保留
# 原来的名字，调用点和测试都用它。
from dshab_build.hashutil import sha256_file as sha256

# 已知文件夹的 GUID：桌面与「开始菜单\程序」。
DESKTOP_FOLDER = "{B4BFCC3A-DB2C-424C-B029-7FE99A87C641}"
START_MENU_PROGRAMS_FOLDER = "{A77F5D77-2E2B-44C3-A6A2-ABA601054A51}"


class _GUID(ctypes.Structure):
    _fields_ = [
        ("Data1", wintypes.DWORD),
        ("Data2", wintypes.WORD),
        ("Data3", wintypes.WORD),
        ("Data4", ctypes.c_ubyte * 8),
    ]


def _guid(text):
    raw = uuid.UUID(text).bytes
    return _GUID(
        int.from_bytes(raw[0:4], "big"),
        int.from_bytes(raw[4:6], "big"),
        int.from_bytes(raw[6:8], "big"),
        (ctypes.c_ubyte * 8)(*raw[8:16]),
    )


def known_folder(folder_id):
    pointer = ctypes.c_wchar_p()
    result = ctypes.windll.shell32.SHGetKnownFolderPath(
        ctypes.byref(_guid(folder_id)), 0, None, ctypes.byref(pointer)
    )
    if result != 0:
        raise OSError(f"SHGetKnownFolderPath 失败（0x{result & 0xFFFFFFFF:08X}）：{folder_id}")
    try:
        return Path(pointer.value)
    finally:
        ctypes.windll.ole32.CoTaskMemFree(ctypes.cast(pointer, ctypes.c_void_p))


def desktop_dir():
    return known_folder(DESKTOP_FOLDER)


def start_menu_programs_dir():
    return known_folder(START_MENU_PROGRAMS_FOLDER)


def exists(path):
    return Path(path).is_file()


def copy(source, destination):
    """逐字节复制，父目录不存在就建。"""
    destination = Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)


def remove(path):
    path = Path(path)
    if not path.exists():
        return
    os.chmod(path, stat.S_IWRITE)
    path.unlink()
