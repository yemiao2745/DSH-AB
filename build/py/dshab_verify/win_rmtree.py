"""递归删除：只读属性和长路径都要删得掉。

旧工具链删不干净时会退回 cmd.exe /c rd /s /q；这里用 shutil + 扩展长度路径前缀替代，
验收工具因此不再调用 cmd。
"""

from __future__ import annotations

import os
import shutil
import stat
import time
from pathlib import Path


def long_path(path):
    r"""绝对路径加上 \\?\ 前缀，绕过 260 字符限制。"""
    text = os.path.abspath(str(path))
    if text.startswith("\\\\?\\"):
        return text
    return "\\\\?\\" + text


def _force_remove(func, path, _error):
    os.chmod(path, stat.S_IWRITE)
    func(path)


def rmtree(path, retries=3):
    """递归删除；删不干净就抛错（绝不静默返回）。"""
    target = long_path(path)
    last = None
    for _attempt in range(retries):
        if not os.path.exists(target):
            return
        try:
            shutil.rmtree(target, onexc=_force_remove)
        except OSError as exc:
            last = exc
            time.sleep(0.5)
    if os.path.exists(target):
        raise OSError(f"目录没清干净：{path}（{last}）")


def clear_dir(path):
    rmtree(path)


def clear_and_assert_empty(path):
    """清空目录、建出来，再断言它确实是空的——每个安装场景都从这里开始。"""
    rmtree(path)
    Path(path).mkdir(parents=True, exist_ok=True)
    left = [entry.name for entry in Path(path).iterdir()]
    if left:
        raise OSError(f"目标目录没清干净：{path}（还有 {len(left)} 项）")
