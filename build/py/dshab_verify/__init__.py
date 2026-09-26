"""验收工具（build/py/dshab_verify）。

13 条源码级门禁在入口 build/py/verify.py；本包提供安装/卸载场景、skill 检查与 Windows 原语。
不调用 powershell/cmd/reg/taskkill：注册表走 winreg，进程走 Toolhelp32，端口走 socket。
"""
