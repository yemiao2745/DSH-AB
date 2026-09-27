r"""Unit tests for the shipped-skill check (build/py/dshab_verify/verify_skill.py).

The check asks dsh's own loader (the bundled node plus the app tree's
@deepseek-ai/dsh-skill-filesystem) what it does with every SKILL.md, so the frontmatter rules are
not tested here at all: a stub stands in for that answer. What is covered is what remains ours -
the two-level bundle layout, the nine trigger words checked against the description dsh parsed,
the body rule, the collected failures and the exit codes. One integration test runs the real
helper end to end and is skipped when the payload app tree has not been built.
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = REPO_ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))

from dshab_verify import verify_skill, win_rmtree  # noqa: E402

WORK = REPO_ROOT / ".tmp-tests" / "skill"
TEMPLATE_SKILLS = REPO_ROOT / "build" / "templates" / "skills"

PLACEHOLDER = "{{DSH_AB_ROOT}}"
BAKED_PATH = r"C:\Program Files\DSH-AB"

# The nine trigger words, pinned as literals: the description is the only trigger a skill has, so a
# change to this list has to be a deliberate change made in two places at once.
TRIGGER_WORDS = [
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
FULL_DESCRIPTION = "，".join(TRIGGER_WORDS)


def skill_text(name="dsh-self-maintenance", description=FULL_DESCRIPTION, body=None):
    if body is None:
        body = "安装根 " + PLACEHOLDER + r"\slot-a 下有说明"
    return f"---\nname: {name}\ndescription: {description}\n---\n{body}\n"


class SkillCheckTests(unittest.TestCase):
    def setUp(self):
        win_rmtree.rmtree(WORK)
        self.root = WORK / "skills"
        self.root.mkdir(parents=True)
        self.dropped = {}
        self.saved = (verify_skill.resolve_dsh_app_tree, verify_skill._loader_verdict)
        verify_skill.resolve_dsh_app_tree = lambda repo_root: (WORK / "app", "node")
        verify_skill._loader_verdict = self.dsh_stub

    def tearDown(self):
        verify_skill.resolve_dsh_app_tree, verify_skill._loader_verdict = self.saved
        win_rmtree.rmtree(WORK)

    def dsh_stub(self, skills_root, app_tree, node_exe):
        """替 dsh 的 loader 回答：文件收不收、收进来是什么。只服务测试，不是实现。"""
        skills = []
        for skill_file in sorted(Path(skills_root).rglob("SKILL.md")):
            path = str(skill_file)
            if path in self.dropped:
                skills.append({"path": path, "accepted": False, "reason": self.dropped[path]})
                continue
            text = skill_file.read_text(encoding="utf-8")
            description = next((line.split(": ", 1)[1] for line in text.splitlines()
                                if line.startswith("description: ")), "")
            skills.append({
                "path": path,
                "accepted": True,
                "name": "dsh-self-maintenance",
                "description": description,
                "content": text.split("---\n", 2)[-1].strip(),
            })
        return {"skills": skills, "logs": []}

    def write_skill(self, text, directory_name="dsh-self-maintenance"):
        directory = self.root / directory_name
        directory.mkdir(exist_ok=True)
        (directory / "SKILL.md").write_text(text, encoding="utf-8")
        return directory

    def check(self, root=None):
        return verify_skill.check_root(self.root if root is None else root, REPO_ROOT)

    # ---- what dsh's loader decides ----------------------------------------------------------------

    def test_code_0_for_a_clean_skill(self):
        self.write_skill(skill_text())
        code, lines = self.check()
        self.assertEqual(code, 0, lines)
        self.assertTrue(any(line.startswith("OK") and "全部合规" in line for line in lines), lines)

    def test_a_file_dsh_drops_is_reported_with_dshs_own_reason(self):
        directory = self.write_skill(skill_text())
        reason = f"skill file {directory / 'SKILL.md'} ignored: invalid YAML frontmatter: boom"
        self.dropped[str(directory / "SKILL.md")] = reason
        code, lines = self.check()
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("dsh 会丢掉这个文件" in line and reason in line for line in lines), lines)

    def test_code_2_when_the_app_tree_cannot_be_resolved(self):
        verify_skill.resolve_dsh_app_tree = lambda repo_root: None
        self.write_skill(skill_text())
        code, lines = self.check()
        self.assertEqual(code, 2, lines)
        self.assertTrue(any("找不到 dsh 应用树" in line and "DSHAB_APP" in line for line in lines), lines)

    def test_code_2_when_the_loader_cannot_run(self):
        def boom(skills_root, app_tree, node_exe):
            raise ValueError("起不了 node：boom")

        verify_skill._loader_verdict = boom
        self.write_skill(skill_text())
        code, lines = self.check()
        self.assertEqual(code, 2, lines)
        self.assertTrue(any("跑不动 dsh 的 loader" in line for line in lines), lines)

    # ---- the rules that are ours ------------------------------------------------------------------

    def test_loose_file_at_the_skills_root_is_a_failure(self):
        self.write_skill(skill_text())
        (self.root / "loose.md").write_text("x", encoding="utf-8")
        code, lines = self.check()
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("loose.md" in line and "散文件" in line for line in lines), lines)

    def test_directory_without_skill_md_is_a_failure(self):
        self.write_skill(skill_text())
        (self.root / "empty-bundle").mkdir()
        code, lines = self.check()
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("empty-bundle" in line and "SKILL.md" in line for line in lines), lines)

    def test_every_missing_trigger_word_is_reported(self):
        for word in TRIGGER_WORDS:
            with self.subTest(missing=word):
                win_rmtree.rmtree(self.root)
                self.root.mkdir()
                rest = [item for item in TRIGGER_WORDS if item != word]
                self.write_skill(skill_text(description="，".join(rest)))
                code, lines = self.check()
                self.assertEqual(code, 1, lines)
                self.assertTrue(
                    any("没覆盖触发词" in line and word in line for line in lines),
                    f"{word} 缺失时没有报出来：{lines}",
                )

    def test_the_description_rule_uses_what_dsh_parsed(self):
        # 文件里写着一堆字，但 dsh 只收到一半（比如 "#" 之后被当注释吃掉）：按 dsh 收到的判。
        self.write_skill(skill_text())
        passthrough = self.dsh_stub

        def half(skills_root, app_tree, node_exe):
            verdict = passthrough(skills_root, app_tree, node_exe)
            for item in verdict["skills"]:
                item["description"] = "更新 dsh，升级 dsh"
            return verdict

        verify_skill._loader_verdict = half
        code, lines = self.check()
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("没覆盖触发词" in line for line in lines), lines)

    def test_body_needs_the_placeholder_or_a_baked_path(self):
        self.write_skill(skill_text(body="正文里没有任何安装路径"))
        code, lines = self.check()
        self.assertEqual(code, 1, lines)
        self.assertTrue(any(PLACEHOLDER in line or "占位符" in line for line in lines), lines)

    def test_body_with_a_baked_absolute_path_passes(self):
        self.write_skill(skill_text(body="见 " + BAKED_PATH + r"\slot-a\data"))
        code, lines = self.check()
        self.assertEqual(code, 0, lines)
        self.assertTrue(any("已烧入绝对路径" in line for line in lines), lines)

    def test_every_failure_is_collected_not_just_the_first(self):
        self.write_skill(skill_text())
        (self.root / "loose.md").write_text("x", encoding="utf-8")
        (self.root / "empty-bundle").mkdir()
        directory = self.write_skill(skill_text(description="没有触发词"), "broken-bundle")
        self.dropped[str(directory / "SKILL.md")] = "skill file ignored: invalid YAML frontmatter: x"
        code, lines = self.check()
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("loose.md" in line for line in lines), lines)
        self.assertTrue(any("empty-bundle" in line for line in lines), lines)
        self.assertTrue(any("broken-bundle" in line for line in lines), lines)
        self.assertTrue(any("3 项不合规" in line for line in lines), lines)

    def test_code_1_when_the_skills_root_is_missing(self):
        code, lines = self.check(self.root / "not-there")
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("skills 根目录不存在" in line for line in lines), lines)

    def test_empty_skills_root_is_accepted(self):
        code, lines = self.check()
        self.assertEqual(code, 0, lines)
        self.assertTrue(any("0 个 skill" in line for line in lines), lines)

    # ---- the real loader, when the payload has been built ------------------------------------------

    @unittest.skipUnless(verify_skill.resolve_dsh_app_tree(REPO_ROOT) is not None,
                         "payload 里的 dsh 应用树还没生成：先跑 build/py/build.py")
    def test_the_shipped_template_skill_passes_every_rule(self):
        self.assertTrue(TEMPLATE_SKILLS.is_dir(), TEMPLATE_SKILLS)
        verify_skill.resolve_dsh_app_tree, verify_skill._loader_verdict = self.saved
        code, lines = verify_skill.check_root(TEMPLATE_SKILLS, REPO_ROOT)
        self.assertEqual(code, 0, lines)
        self.assertTrue(any("dsh-self-maintenance" in line for line in lines), lines)


if __name__ == "__main__":
    unittest.main()
