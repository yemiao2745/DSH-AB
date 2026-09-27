// dsh 自己的 loader 跑一遍一个 skills 根目录：随包 node 执行本脚本，命令行是
// <app tree> <skills root>。判定与解析全部来自 dsh（FileSystemSkillProvider），
// 这里只把结果整理成 JSON 打印到 stdout：
//   {"skills": [{"path": ..., "accepted": true, "name": ..., "description": ..., "content": ...},
//               {"path": ..., "accepted": false, "reason": "<dsh 的告警原文>"}],
//    "logs": [...]}
// dsh 的 discoverRoot 只把解析得出来的文件当候选，所以收下的文件来自 list()，
// 丢掉的文件由本脚本自己列目录补齐，原因取自 dsh 自己的 logger.warn。
// 致命错误打印 {"error": "..."} 并以 3 退出。
import { readdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const [appTree, skillsRoot] = process.argv.slice(2);
const root = resolve(skillsRoot);
const logs = [];
const ctx = {
  logger: { warn: (message) => logs.push(String(message)), info() {}, debug() {}, error() {} },
  get: () => undefined,
};
const control = { invalidate: () => {}, signal: new AbortController().signal };
const config = {
  providerName: "filesystem",
  includeDefaultRoots: false,
  dshHome: root,
  agentsHome: root,
  customSkillDirs: [root],
  watch: false,
};

async function main() {
  const entry = join(appTree, "node_modules", "@deepseek-ai", "dsh-skill-filesystem", "lib", "index.js");
  const { FileSystemSkillProvider } = await import(pathToFileURL(entry).href);
  const provider = new FileSystemSkillProvider(ctx, control, config);
  const listed = await provider.list({ cwd: root });
  const skills = [];
  for (const candidate of Array.isArray(listed) ? listed : listed.candidates) {
    const loaded = await provider.get(candidate, {});
    if (loaded === undefined) continue;
    skills.push({
      path: candidate.locator.path,
      accepted: true,
      name: loaded.name,
      description: loaded.description,
      content: loaded.content,
    });
  }
  const accepted = new Set(skills.map((skill) => skill.path));
  for (const item of await readdir(root, { withFileTypes: true })) {
    if (!item.isDirectory() && !item.name.endsWith(".md")) continue;
    const path = item.isDirectory() ? join(root, item.name, "SKILL.md") : join(root, item.name);
    if (accepted.has(path)) continue;
    skills.push({ path, accepted: false, reason: logs.find((line) => line.includes(path)) ?? "" });
  }
  if (typeof provider.dispose === "function") await provider.dispose();
  return { skills, logs };
}

try {
  console.log(JSON.stringify(await main()));
} catch (error) {
  console.log(JSON.stringify({ error: String(error && error.message ? error.message : error) }));
  process.exitCode = 3;
}