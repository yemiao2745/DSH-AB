"""验收报告：逐条断言、执行的命令、日志路径，落到 .tmp-verify\\report.txt。

报告就是证据：失败时报告里能直接看到是哪一条、跑了什么命令、日志在哪。
"""

from __future__ import annotations

import time
from pathlib import Path


class Report:
    def __init__(self, path, repo_root, echo=True):
        self.path = Path(path)
        self.repo_root = Path(repo_root)
        self.echo = echo
        self.lines = []
        self.failures = []
        self.started = time.strftime("%Y-%m-%d %H:%M:%S")

    def header(self, entries):
        """报告抬头：时间、仓库根，以及这次被测的东西（安装目录、安装包、端口…）。"""
        self._add(f"DSH-AB 验收报告 {self.started}")
        self._add(f"仓库根：{self.repo_root}")
        for name, value in entries.items():
            self._add(f"{name}：{value}")
        self._add("")

    def section(self, title):
        self._add("")
        self._add(f"== {title}")

    def note(self, text):
        self._add("   " + str(text))

    def record(self, name, ok, detail="", command=None, log=None):
        """记一条断言；返回是否通过。"""
        line = f"[{'ok  ' if ok else 'FAIL'}] {name}"
        if detail:
            line += f" —— {detail}"
        self._add(line)
        if command:
            self._add(f"       cmd: {command}")
        if log:
            self._add(f"       log: {log}")
        if not ok:
            self.failures.append(name)
        return ok

    def write(self):
        conclusion = f"失败 {len(self.failures)} 条" if self.failures else "全部通过"
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.path.write_text("\n".join(self.lines + ["", "== 结论", conclusion, ""]), encoding="utf-8")
        return self.path

    def _add(self, text):
        self.lines.append(text)
        if self.echo:
            print(text)
