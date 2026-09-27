r"""随包 skill 的检查：frontmatter 由 dsh 自己的 loader 判，Python 这边只管我们自己的规则。

判定与解析都来自 dsh：随包 node（<slot>\node\node.exe）跑 skill_loader.mjs，它 import 应用树里的
@deepseek-ai/dsh-skill-filesystem，用 dsh 的 FileSystemSkillProvider 读出每个 skill 文件"收不收、
收进来是什么"，dsh 丢掉的文件连同它自己的告警原文一起带回来 —— YAML 一行都不在这里重新实现。
所以这个检查需要 dsh 应用树：先跑 tools\python\python.exe build\py\build.py 生成 payload。

留下的规则是我们自己的：两级目录 bundle（散文件、缺 SKILL.md）、description 覆盖九个触发词、正文
带 {{DSH_AB_ROOT}} 占位符或已烧入的绝对安装路径；"dsh 收不收这个文件"由 loader 决定。

单独运行：
    tools\python\python.exe build\py\dshab_verify\verify_skill.py [skills 根目录]
退出码：2 = 找不到 dsh 应用树或跑不动它的 loader；1 = 有不合规项；0 = 全部合规。
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path

# description 是 skill 唯一的触发途径，必须覆盖的词在这里钉死。
REQUIRED_DESCRIPTION_WORDS = [
    "更新 dsh",
    "升级 dsh",
    "插件",
    "配置",
    "打补丁",
    "同步到另一个槽",
    "切槽",
    "槽位切换",
    "dsh 自身",
]

PLACEHOLDER = "{{DSH_AB_ROOT}}"
BAKED_PATH_RE = re.compile(r"`?[A-Za-z]:\\")

# dsh loader 的入口：应用树里有它，才跑得动 dsh 自己的解析。
LOADER_ENTRY = Path("node_modules") / "@deepseek-ai" / "dsh-skill-filesystem" / "lib" / "index.js"
LOADER_TIMEOUT_SEC = 300
APP_TREE_HINT = "设置 DSHAB_APP，或先跑 tools\\python\\python.exe build\\py\\build.py 生成 payload"


def resolve_dsh_app_tree(repo_root):
    """dsh 应用树与随包 node：dsh 的 loader 要这两个。解析不到返回 None。"""
    for candidate in (os.environ.get("DSHAB_APP"),
                      Path(repo_root) / "payload" / "slot-a" / "app",
                      Path(repo_root) / "slot-a" / "app"):
        if not candidate:
            continue
        app_tree = Path(candidate)
        if not (app_tree / LOADER_ENTRY).is_file():
            continue
        node_exe = app_tree.parent / "node" / "node.exe"
        return app_tree, str(node_exe) if node_exe.is_file() else "node"
    return None


def _loader_verdict(skills_root, app_tree, node_exe):
    """跑 dsh 自己的 loader（skill_loader.mjs），返回它打印的 JSON。跑不动就抛 ValueError。"""
    helper = Path(__file__).resolve().with_name("skill_loader.mjs")
    command = [str(node_exe), str(helper), str(app_tree), str(skills_root)]
    try:
        run = subprocess.run(command, capture_output=True, text=True, encoding="utf-8",
                             errors="replace", timeout=LOADER_TIMEOUT_SEC)
    except OSError as exc:
        raise ValueError(f"起不了 node（{node_exe}）：{exc}") from exc
    except subprocess.TimeoutExpired as exc:
        raise ValueError(f"loader 超过 {LOADER_TIMEOUT_SEC} 秒没返回") from exc
    if run.returncode != 0:
        raise ValueError(f"loader 退出码 {run.returncode}：{(run.stderr or run.stdout).strip()[:300]}")
    try:
        verdict = json.loads(run.stdout)
    except ValueError as exc:
        raise ValueError(f"loader 没有打印 JSON（{exc}）：{run.stdout.strip()[:200]}") from exc
    if "error" in verdict:
        raise ValueError(str(verdict["error"])[:300])
    return verdict


def _path_key(path):
    """dsh 那边给的是绝对路径，这边手上可能是相对路径（/ 与 \\ 也可能混用）：比较前统一。"""
    return os.path.normcase(os.path.normpath(os.path.abspath(str(path))))


def check_root(skills_root, repo_root):
    """检查一个 skills 根目录，返回 (退出码, 输出行)。每个失败都收集，不提前退出。"""
    skills_root = Path(skills_root)
    lines = []
    failures = []

    def fail(message):
        failures.append(message)
        lines.append(f"FAIL {message}")

    app_tree = resolve_dsh_app_tree(repo_root)
    if app_tree is None:
        lines.append("FAIL 找不到 dsh 应用树（要跑 dsh 自己的 loader）：" + APP_TREE_HINT)
        return 2, lines
    if not skills_root.is_dir():
        lines.append(f"FAIL skills 根目录不存在：{skills_root}")
        return 1, lines
    try:
        verdict = _loader_verdict(skills_root, app_tree[0], app_tree[1])
    except ValueError as exc:
        lines.append(f"FAIL 跑不动 dsh 的 loader：{exc}")
        return 2, lines
    from_dsh = {_path_key(item.get("path")): item for item in verdict.get("skills", [])}

    checked = 0
    for entry in sorted(skills_root.iterdir(), key=lambda item: item.name):
        if not entry.is_dir():
            fail(f"{entry.name}：skill 根目录下不该有散文件，bundle 必须是 <name>\\SKILL.md 两级")
            continue
        skill_file = entry / "SKILL.md"
        if not skill_file.is_file():
            fail(f"{entry.name}：缺少 {entry.name}\\SKILL.md（dsh 只认两级目录 bundle）")
            continue
        loaded = from_dsh.get(_path_key(skill_file))
        if loaded is None or not loaded.get("accepted"):
            reason = (loaded or {}).get("reason") or "dsh 没有说原因"
            fail(f"{entry.name}：dsh 会丢掉这个文件（{reason}）")
            continue
        description = loaded.get("description") or ""
        missing = [word for word in REQUIRED_DESCRIPTION_WORDS if word not in description]
        if missing:
            fail(f"{entry.name}：description 没覆盖触发词 {'、'.join(missing)}")
            continue
        body = loaded.get("content") or ""
        if PLACEHOLDER not in body and not BAKED_PATH_RE.search(body):
            fail(f"{entry.name}：正文既没有 {PLACEHOLDER} 占位符，也没有已烧入的绝对安装路径")
            continue
        state = "占位符待安装期替换" if PLACEHOLDER in body else "已烧入绝对路径"
        checked += 1
        lines.append(
            f"OK   {_relative(skill_file, repo_root)}：name={loaded.get('name')}，"
            f"description 覆盖 {len(REQUIRED_DESCRIPTION_WORDS)} 个触发词，"
            f"正文 {len(body)} 字符（{state}）"
        )

    if failures:
        lines.append(f"{len(failures)} 项不合规（skills 根目录：{skills_root}）")
        return 1, lines
    lines.append(f"OK   skills 根目录 {skills_root} 下 {checked} 个 skill 全部合规")
    return 0, lines


def _relative(path, repo_root):
    try:
        return str(Path(path).relative_to(repo_root))
    except ValueError:
        return str(path)


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    repo_root = Path(__file__).resolve().parents[3]
    skills_root = Path(argv[0]) if argv else repo_root / "build" / "templates" / "skills"
    code, lines = check_root(skills_root, repo_root)
    for line in lines:
        print(line)
    return code


if __name__ == "__main__":
    raise SystemExit(main())
