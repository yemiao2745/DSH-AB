#!/usr/bin/env node
// verify-skill.mjs - the one sanctioned check of the shipped skill (SPEC-skill-instead-of-agents-md
// section 2, criterion 2). Run by build\verify-silent.ps1; re-runnable on its own:
//
//   payload\slot-a\node\node.exe build\verify-skill.mjs [skills root]
//
// Default skills root: build\templates\skills. Pass an installed one to check the copy that was
// really written to disk, e.g. <install root>\slot-a\data\skills.
//
// Why it exists: dsh discovers a user-level skill only as <DSH_HOME>\skills\<name>\SKILL.md, with
// YAML frontmatter carrying a non-empty name and description, and a name matching the public
// kebab-case grammar (@deepseek-ai/dsh-skill isSkillName). Anything else is dropped with a log line
// the AI never sees, so a broken skill fails silently exactly where being seen is the whole point.
//
// The dsh app tree that provides `yaml` and `@deepseek-ai/dsh-skill` comes from $DSHAB_APP, else
// payload\slot-a\app, else slot-a\app. Exit code 0 = every skill is loadable.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

// description is the only trigger a skill has, so the words that have to reach the model are pinned
// here: update/upgrade dsh, plugins, config, patches, the other slot, the ledgers (SPEC section 1.3).
const REQUIRED_DESCRIPTION_WORDS = [
  '更新 dsh', '升级 dsh', '插件', '配置', '打补丁', '同步到另一个槽', '切槽', '槽位切换', 'dsh 自身',
]

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)))
const skillsRoot = process.argv[2] ?? join(repoRoot, 'build', 'templates', 'skills')
const failures = []

function fail(message) {
  failures.push(message)
  console.log(`FAIL ${message}`)
}

function findAppTree() {
  for (const candidate of [
    process.env.DSHAB_APP,
    join(repoRoot, 'payload', 'slot-a', 'app'),
    join(repoRoot, 'slot-a', 'app'),
  ]) {
    if (candidate && existsSync(join(candidate, 'node_modules', 'yaml'))) return candidate
  }
  console.log('FAIL 找不到提供 yaml 的 dsh 应用树：设置 DSHAB_APP，或先跑 build\\build.ps1 生成 payload')
  process.exit(2)
}

// The dsh loader is the authority on all three rules below, so it is asked instead of reimplemented.
const appTree = findAppTree()
const requireFromApp = createRequire(join(appTree, 'package.json'))
const { parse: parseYaml } = requireFromApp('yaml')
const { isSkillName } = await import(pathToFileURL(
  join(appTree, 'node_modules', '@deepseek-ai', 'dsh-skill', 'lib', 'index.js')).href)

function readFrontmatter(path) {
  const match = /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/.exec(readFileSync(path, 'utf8'))
  if (!match) throw new Error('没有 YAML frontmatter')
  return parseYaml(match[1]) ?? {}
}

if (!existsSync(skillsRoot)) {
  console.log(`FAIL skills 根目录不存在：${skillsRoot}`)
  process.exit(1)
}
const entries = readdirSync(skillsRoot, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))

let checked = 0
for (const entry of entries) {
  if (!entry.isDirectory()) {
    fail(`${entry.name}：skill 根目录下不该有散文件，bundle 必须是 <name>\\SKILL.md 两级`)
    continue
  }
  const dir = join(skillsRoot, entry.name)
  const file = join(dir, 'SKILL.md')
  if (!existsSync(file) || !statSync(file).isFile()) {
    fail(`${entry.name}：缺少 ${entry.name}\\SKILL.md（dsh 只认两级目录 bundle）`)
    continue
  }
  const text = readFileSync(file, 'utf8')
  let data
  try {
    data = readFrontmatter(file)
  } catch (error) {
    fail(`${entry.name}：frontmatter 读不出来（${error.message}）—— dsh 会整条忽略这个 skill`)
    continue
  }
  const name = typeof data.name === 'string' ? data.name.trim() : ''
  const description = typeof data.description === 'string' ? data.description.trim() : ''
  if (name === '' || description === '') {
    fail(`${entry.name}：frontmatter 必须有非空的 name 与 description`)
    continue
  }
  if (!isSkillName(name)) {
    fail(`${entry.name}：name "${name}" 不是合法 skill 名（^[a-z0-9]+(-[a-z0-9]+)*$）`)
    continue
  }
  const missing = REQUIRED_DESCRIPTION_WORDS.filter((word) => !description.includes(word))
  if (missing.length > 0) {
    fail(`${entry.name}：description 没覆盖触发词 ${missing.join('、')}`)
    continue
  }
  const body = text.replace(/^---[\s\S]*?\r?\n---\r?\n?/, '')
  if (!body.includes('{{DSH_AB_ROOT}}') && !/`?[A-Za-z]:\\/.test(body)) {
    fail(`${entry.name}：正文既没有 {{DSH_AB_ROOT}} 占位符，也没有已烧入的绝对安装路径`)
    continue
  }
  const state = body.includes('{{DSH_AB_ROOT}}') ? '占位符待安装期替换' : '已烧入绝对路径'
  checked += 1
  console.log(`OK   ${relative(repoRoot, file)}：name=${name}，description 覆盖 ${REQUIRED_DESCRIPTION_WORDS.length} 个触发词，正文 ${body.length} 字符（${state}）`)
}

if (failures.length > 0) {
  console.log(`${failures.length} 项不合规（skills 根目录：${skillsRoot}）`)
  process.exit(1)
}
console.log(`OK   skills 根目录 ${skillsRoot} 下 ${checked} 个 skill 全部合规`)
