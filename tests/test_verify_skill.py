r"""Unit tests for the shipped-skill check (build/py/dshab_verify/verify_skill.py).

Contract: BUILD_CONTRACT.md section 4, scenario 1 assertions 1.8 / 1.9 ("verify-skill passes for both
build/templates/skills and <Dir>/slot-a/data/skills") - the rule list itself is the one the previous
implementation shipped: two-level <name>\SKILL.md bundles, non-empty YAML name and description, a
kebab-case name, all nine trigger words in the description, and a body that carries either the
install-time placeholder or an already baked absolute path.

Fixtures live under .tmp-tests\skill. The "app tree" the check resolves is faked with a directory
holding node_modules\yaml, so a skills root can be checked without a payload build.
"""

from __future__ import annotations

import os
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
        self.app_tree = WORK / "app"
        (self.app_tree / "node_modules" / "yaml").mkdir(parents=True)
        self.root = WORK / "skills"
        self.root.mkdir()
        self._saved_app = os.environ.get("DSHAB_APP")
        os.environ["DSHAB_APP"] = str(self.app_tree)

    def tearDown(self):
        if self._saved_app is None:
            os.environ.pop("DSHAB_APP", None)
        else:
            os.environ["DSHAB_APP"] = self._saved_app
        win_rmtree.rmtree(WORK)

    def write_skill(self, text, directory_name="dsh-self-maintenance"):
        directory = self.root / directory_name
        directory.mkdir(exist_ok=True)
        (directory / "SKILL.md").write_text(text, encoding="utf-8")
        return directory

    def check(self, root=None):
        return verify_skill.check_root(self.root if root is None else root, REPO_ROOT)

    # ---- the exit-code contract: 2 = no app tree, 1 = violations, 0 = clean ---------------------

    def test_code_2_when_the_app_tree_cannot_be_resolved(self):
        os.environ.pop("DSHAB_APP", None)
        # A repository root with no payload of its own, so the answer does not depend on whether this
        # checkout happens to have been built.
        code, lines = verify_skill.check_root(self.root, WORK / "repo-without-payload")
        self.assertEqual(code, 2, lines)
        self.assertTrue(any("应用树" in line and "DSHAB_APP" in line for line in lines), lines)

    def test_code_1_when_the_skills_root_is_missing(self):
        code, lines = self.check(self.root / "not-there")
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("skills 根目录不存在" in line for line in lines), lines)

    def test_code_0_for_a_clean_skill(self):
        self.write_skill(skill_text())
        code, lines = self.check()
        self.assertEqual(code, 0, lines)
        self.assertTrue(any(line.startswith("OK") and "全部合规" in line for line in lines), lines)

    # ---- one rule per test ----------------------------------------------------------------------

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

    def test_missing_frontmatter_is_a_failure(self):
        self.write_skill("name: x\ndescription: y\n")
        code, lines = self.check()
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("没有 YAML frontmatter" in line for line in lines), lines)

    def test_unterminated_frontmatter_is_a_failure(self):
        self.write_skill("---\nname: x\ndescription: y\n")
        code, lines = self.check()
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("没有 YAML frontmatter" in line for line in lines), lines)

    def test_empty_name_or_description_is_a_failure(self):
        cases = {
            "empty name": skill_text(name=""),
            "empty description": skill_text(description=""),
        }
        for label, text in cases.items():
            with self.subTest(label=label):
                win_rmtree.rmtree(self.root)
                self.root.mkdir()
                self.write_skill(text)
                code, lines = self.check()
                self.assertEqual(code, 1, lines)
                self.assertTrue(any("非空的 name 与 description" in line for line in lines), lines)

    def test_non_kebab_name_is_a_failure(self):
        for bad in ("Bad_Name", "Upper", "-lead", "trail-", "two--dashes", "有中文"):
            with self.subTest(name=bad):
                win_rmtree.rmtree(self.root)
                self.root.mkdir()
                self.write_skill(skill_text(name=bad))
                code, lines = self.check()
                self.assertEqual(code, 1, lines)
                self.assertTrue(any("不是合法 skill 名" in line for line in lines), lines)

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

    # ---- shape of the output and cross-cutting behaviour ----------------------------------------

    def test_every_failure_is_collected_not_just_the_first(self):
        self.write_skill(skill_text())
        (self.root / "loose.md").write_text("x", encoding="utf-8")
        (self.root / "empty-bundle").mkdir()
        self.write_skill("name: x\n", directory_name="broken-bundle")
        code, lines = self.check()
        self.assertEqual(code, 1, lines)
        self.assertTrue(any("loose.md" in line for line in lines), lines)
        self.assertTrue(any("empty-bundle" in line for line in lines), lines)
        self.assertTrue(any("broken-bundle" in line for line in lines), lines)
        self.assertTrue(any("3 项不合规" in line for line in lines), lines)

    def test_frontmatter_values_may_be_quoted_and_crlf(self):
        text = skill_text().replace("\n", "\r\n").replace(
            "name: dsh-self-maintenance", 'name: "dsh-self-maintenance"'
        )
        self.write_skill(text)
        code, lines = self.check()
        self.assertEqual(code, 0, lines)

    def test_parse_frontmatter_keeps_block_scalar_text_on_its_key(self):
        data = verify_skill.parse_frontmatter(
            "---\nname: dsh-x\ndescription: one\n  two\n---\nbody\n"
        )
        self.assertEqual(data["name"], "dsh-x")
        self.assertEqual(data["description"], "one\ntwo")

    def test_parse_frontmatter_rejects_text_without_frontmatter(self):
        with self.assertRaises(ValueError):
            verify_skill.parse_frontmatter("no frontmatter here\n")

    def test_empty_skills_root_is_accepted_like_the_previous_check(self):
        code, lines = self.check()
        self.assertEqual(code, 0, lines)
        self.assertTrue(any("0 个 skill" in line for line in lines), lines)

    # ---- the shipped template itself ------------------------------------------------------------

    def test_the_shipped_template_skill_passes_every_rule(self):
        self.assertTrue(TEMPLATE_SKILLS.is_dir(), TEMPLATE_SKILLS)
        code, lines = verify_skill.check_root(TEMPLATE_SKILLS, REPO_ROOT)
        self.assertEqual(code, 0, lines)
        self.assertTrue(any("dsh-self-maintenance" in line for line in lines), lines)


if __name__ == "__main__":
    unittest.main()
