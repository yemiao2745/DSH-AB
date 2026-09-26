r"""随包 skill 的检查：verify-skill.mjs 的 Python 版本。

替换 build/verify-skill.mjs：不再需要 Node，也不再需要 dsh 应用树里的 yaml 与
@deepseek-ai/dsh-skill —— frontmatter 与名字规则在这里直接用 re 实现。
应用树仍然按老规矩解析（DSHAB_APP → payload\slot-a\app → slot-a\app），
解析不到就是退出码 2，与旧脚本一致。

单独运行：
    tools\python\python.exe build\py\dshab_verify\verify_skill.py [skills 根目录]
退出码：2 = 解析不到应用树；1 = 有不合规项；0 = 全部合规。
"""

from __future__ import annotations

import os
import re
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

# dsh 的 isSkillName：^[a-z0-9]+(-[a-z0-9]+)*$
SKILL_NAME_RE = re.compile(r"^[a-z0-9]+(-[a-z0-9]+)*$")
FRONTMATTER_RE = re.compile(r"^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)")
FRONTMATTER_STRIP_RE = re.compile(r"^---[\s\S]*?\r?\n---\r?\n?")
BAKED_PATH_RE = re.compile(r"`?[A-Za-z]:\\")
PLACEHOLDER = "{{DSH_AB_ROOT}}"

APP_TREE_HINT = "设置 DSHAB_APP，或先跑 tools\\python\\python.exe build\\py\\build.py 生成 payload"


def find_app_tree(repo_root):
    """提供 yaml 的 dsh 应用树；解析不到返回 None。"""
    candidates = [
        os.environ.get("DSHAB_APP"),
        Path(repo_root) / "payload" / "slot-a" / "app",
        Path(repo_root) / "slot-a" / "app",
    ]
    for candidate in candidates:
        if candidate and (Path(candidate) / "node_modules" / "yaml").exists():
            return Path(candidate)
    return None


def parse_frontmatter(text):
    """--- 之间的 YAML frontmatter -> dict。解析不了就抛 ValueError。"""
    match = FRONTMATTER_RE.match(text)
    if not match:
        raise ValueError("没有 YAML frontmatter")
    data = {}
    key = None
    for line in match.group(1).splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if line[:1].isspace():
            # 续行 / 块标量的正文：并到上一个键上，不要当成新键。
            if key is None:
                raise ValueError(f"frontmatter 里有解析不了的行：{stripped}")
            data[key] = (data[key] + "\n" + stripped).strip()
            continue
        head, separator, rest = line.partition(":")
        if not separator:
            raise ValueError(f"frontmatter 里有解析不了的行：{stripped}")
        key = head.strip()
        value = rest.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
            value = value[1:-1]
        data[key] = value
    return data


def check_root(skills_root, repo_root):
    """检查一个 skills 根目录，返回 (退出码, 输出行)。每个失败都收集，不提前退出。"""
    skills_root = Path(skills_root)
    lines = []
    failures = []

    def fail(message):
        failures.append(message)
        lines.append(f"FAIL {message}")

    if find_app_tree(repo_root) is None:
        lines.append("FAIL 找不到提供 yaml 的 dsh 应用树：" + APP_TREE_HINT)
        return 2, lines

    if not skills_root.is_dir():
        lines.append(f"FAIL skills 根目录不存在：{skills_root}")
        return 1, lines

    checked = 0
    for entry in sorted(skills_root.iterdir(), key=lambda item: item.name):
        if not entry.is_dir():
            fail(f"{entry.name}：skill 根目录下不该有散文件，bundle 必须是 <name>\\SKILL.md 两级")
            continue
        skill_file = entry / "SKILL.md"
        if not skill_file.is_file():
            fail(f"{entry.name}：缺少 {entry.name}\\SKILL.md（dsh 只认两级目录 bundle）")
            continue
        text = skill_file.read_text(encoding="utf-8")
        try:
            data = parse_frontmatter(text)
        except ValueError as exc:
            fail(f"{entry.name}：frontmatter 读不出来（{exc}）—— dsh 会整条忽略这个 skill")
            continue
        name = data.get("name", "").strip() if isinstance(data.get("name"), str) else ""
        description = data.get("description", "").strip() if isinstance(data.get("description"), str) else ""
        if not name or not description:
            fail(f"{entry.name}：frontmatter 必须有非空的 name 与 description")
            continue
        if not SKILL_NAME_RE.match(name):
            fail(f'{entry.name}：name "{name}" 不是合法 skill 名（^[a-z0-9]+(-[a-z0-9]+)*$）')
            continue
        missing = [word for word in REQUIRED_DESCRIPTION_WORDS if word not in description]
        if missing:
            fail(f"{entry.name}：description 没覆盖触发词 {'、'.join(missing)}")
            continue
        body = FRONTMATTER_STRIP_RE.sub("", text, count=1)
        if PLACEHOLDER not in body and not BAKED_PATH_RE.search(body):
            fail(f"{entry.name}：正文既没有 {PLACEHOLDER} 占位符，也没有已烧入的绝对安装路径")
            continue
        state = "占位符待安装期替换" if PLACEHOLDER in body else "已烧入绝对路径"
        checked += 1
        lines.append(
            f"OK   {_relative(skill_file, repo_root)}：name={name}，"
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
