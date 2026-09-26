"""HKCU 卸载登记项：读取、快照、还原、存在性比对。

旧工具链这三件事全部靠 reg.exe（query/export/import/delete）；这里是它的替代实现，只用 winreg，
所以验收工具不再依赖 reg.exe。登记项本身由安装器 [Code] 按安装根派生，键名规则见 install_entry_key。
"""

from __future__ import annotations

import hashlib
import winreg

# Inno 的卸载登记项都落在这一层。
UNINSTALL_SUBKEY = r"Software\Microsoft\Windows\CurrentVersion\Uninstall"

# 用户自己那份安装的登记项（多份安装身份化之前的固定 AppId）：只被快照与还原，绝不被断言改动。
LEGACY_USER_ENTRY = "{8F3A1C42-6D7E-4B9A-9E15-2C4F7A0B6D31}_is1"

# 现在每一份安装都带这个 AppId，叶子名再加安装根派生的 tag。
INSTALL_APP_ID = "{6F1D2A74-3B58-4C9E-8A17-5E0C4D3B9A62}"

_HIVES = {
    "HKCU": winreg.HKEY_CURRENT_USER,
    "HKLM": winreg.HKEY_LOCAL_MACHINE,
}


def split_path(key_path):
    """'HKCU\\Software\\...' -> ('HKCU', 'Software\\...')。"""
    root, _, sub = key_path.partition("\\")
    if root.upper() not in _HIVES or not sub:
        raise ValueError(f"只支持 HKCU/HKLM 下的键路径：{key_path}")
    return root.upper(), sub


def key_exists(key_path):
    hive, sub = split_path(key_path)
    try:
        with winreg.OpenKey(_HIVES[hive], sub):
            return True
    except FileNotFoundError:
        return False


def read_values(key_path):
    """返回 {值名: (类型, 数据)}；键不存在返回 None。默认值（名 ""）同样在结果里。"""
    hive, sub = split_path(key_path)
    try:
        with winreg.OpenKey(_HIVES[hive], sub) as key:
            values = {}
            index = 0
            while True:
                try:
                    name, data, kind = winreg.EnumValue(key, index)
                except OSError:
                    break
                values[name] = (kind, data)
                index += 1
            return values
    except FileNotFoundError:
        return None


def read_value(key_path, name):
    """单个值的数据；键或值不存在返回 None。"""
    values = read_values(key_path)
    if not values or name not in values:
        return None
    return values[name][1]


def snapshot(key_path):
    """快照一份登记项：None 表示运行前这个键不存在，还原时也必须让它不存在。"""
    return read_values(key_path)


def delete_tree(key_path):
    hive, sub = split_path(key_path)
    _delete_tree(_HIVES[hive], sub)


def _delete_tree(hive, sub):
    try:
        with winreg.OpenKey(hive, sub, 0, winreg.KEY_READ | winreg.KEY_WRITE) as key:
            children = []
            index = 0
            while True:
                try:
                    children.append(winreg.EnumKey(key, index))
                except OSError:
                    break
                index += 1
    except FileNotFoundError:
        return
    for child in children:
        _delete_tree(hive, sub + "\\" + child)
    winreg.DeleteKey(hive, sub)


def restore(key_path, saved):
    """把快照放回去，并核对存在性；对不上就抛错。

    先删再建：被测安装可能写进了快照里没有的值名，而还原是「替换」不是「合并」。
    """
    hive, sub = split_path(key_path)
    delete_tree(key_path)
    if saved is not None:
        with winreg.CreateKeyEx(_HIVES[hive], sub, 0, winreg.KEY_WRITE) as key:
            for name, (kind, data) in saved.items():
                winreg.SetValueEx(key, name, 0, kind, data)
    if key_exists(key_path) != (saved is not None):
        raise OSError(f"卸载登记项状态与运行前不一致（运行前存在={saved is not None}）：{key_path}")


def remove_trailing_sep(path):
    r"""Inno 的 RemoveBackslashUnlessRoot（src\dsh-ab\naming.go 的 removeTrailingSep 同规则）。

    去掉全部尾分隔符，但盘符根（D:\）要保留一个反斜杠：安装器、运行时和这里必须哈希同一个字符串，
    否则同一台机器上三方会算出不同的 tag（见 naming_test.go 里钉死的那条）。
    """
    trimmed = str(path).rstrip("\\/")
    if not trimmed:
        return str(path)
    if len(trimmed) == 2 and trimmed[1] == ":":
        return trimmed + "\\"
    return trimmed


def install_tag(root):
    """[Code] 由安装根派生 tag：小写、去尾反斜杠（根目录除外）、UTF-16LE、SHA-256 前 12 位十六进制。

    旧验收脚本用的是 TrimEnd('\\')，盘符根会把反斜杠也去掉，与安装器算出的不一样；这里按安装器
    与运行时的口径来（有意与已删除的脚本不同）。
    """
    digest = hashlib.sha256(remove_trailing_sep(root).lower().encode("utf-16-le")).hexdigest()
    return digest[:12]


def entry_key_path(leaf, subkey=UNINSTALL_SUBKEY):
    """'HKCU\\Software\\...\\Uninstall\\<leaf>'。"""
    return "HKCU\\" + subkey + "\\" + leaf


def install_entry_key(root, subkey=UNINSTALL_SUBKEY):
    """这份安装自己的登记项键路径（与 dsh-ab.iss 的 WriteUninstallEntry 同一条规则）。"""
    return entry_key_path(f"{INSTALL_APP_ID}_{install_tag(root)}_is1", subkey)


def user_entry_key(subkey=UNINSTALL_SUBKEY):
    """用户自己那份安装的登记项键路径。"""
    return entry_key_path(LEGACY_USER_ENTRY, subkey)
