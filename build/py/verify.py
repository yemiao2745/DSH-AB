#!/usr/bin/env python3
r"""verify.py - 验收：13 条源码级门禁 + 5 个安装/卸载场景 + 随包 skill 检查。

替代 build\verify-silent.ps1（以及 build\verify-skill.mjs 的 Node 依赖）。用项目自带的解释器跑：

    tools\python\python.exe build\py\verify.py [--dir ...] [--setup ...] [--iss ...] [--port 3190]

13 条源码级门禁的唯一实现在 build\py\dshab_build\isscheck.py（构建期编译前的门禁与验收期是同一份），
安装根文件集规则的唯一实现在 build\py\dshab_build\install_layout.py，本脚本只负责调用与报告。

三个默认值都由本脚本自己的位置推出来（全部相对路径，项目目录搬到哪都能跑）：
    --dir    仓库根\.tmp-verify\install
    --setup  dist\DSH-AB-<dshab版本>-<dsh版本>-setup.exe（名字与版本来自 payload 清单）
    --iss    仓库根\installer\dsh-ab.iss

报告（逐条断言、执行的命令、日志路径）写在 仓库根\.tmp-verify\report.txt。
退出码 0 = 全部通过，最后一行是：OK install+uninstall exit=0 dir=<Dir>
"""

from __future__ import annotations

import argparse
import json
import sys
import traceback
from pathlib import Path

# 入口脚本的目录要显式放进 sys.path：tools\python 是 embeddable 版解释器，它的 python312._pth
# 一旦存在，Python 就不再自动把脚本目录（和 cwd）加进 sys.path，于是从项目根跑
# tools\python\python.exe build\py\verify.py 时 dshab_build / dshab_verify 都导入不到。
sys.path.insert(0, str(Path(__file__).resolve().parent))

from dshab_build import isscheck

from dshab_verify import scenarios
from dshab_verify.report import Report
from dshab_verify.scenarios import Config, VerifyError


class Paths:
    """这次被测的东西：安装目录、安装包、安装器脚本、payload 清单。"""

    def __init__(self, install_dir, setup, iss, manifest, manifest_path, expected_setup_name):
        self.install_dir = install_dir
        self.setup = setup
        self.iss = iss
        self.manifest = manifest
        self.manifest_path = manifest_path
        self.expected_setup_name = expected_setup_name


def _resolve(args, repo_root):
    """解析并核对前置条件：清单、产物名、安装包、.iss。任何一条不成立就直接失败。"""
    manifest_path = repo_root / "payload" / "payload-manifest.json"
    if not manifest_path.is_file():
        raise VerifyError(f"找不到 payload 清单：{manifest_path}（先跑 tools\\python\\python.exe build\\py\\build.py）")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8-sig"))
    if not (manifest.get("install_files") or []):
        raise VerifyError(
            f"payload 清单里没有 install_files（构建期记录）：{manifest_path} 是旧版构建写的，payload 要重跑一次构建")

    expected_setup_name = f"DSH-AB-{manifest.get('dshab_version')}-{manifest.get('dsh_version')}-setup.exe"
    iss = Path(args.iss) if args.iss else repo_root / "installer" / "dsh-ab.iss"
    if not iss.is_file():
        raise VerifyError(f"找不到安装器脚本：{iss}")

    if args.setup:
        setup = Path(args.setup)
        if setup.name != expected_setup_name:
            raise VerifyError(
                f"产物名不符合 DSH-AB-<dshab版本>-<dsh版本>-setup.exe：实际 {setup.name}，期望 {expected_setup_name}")
    else:
        candidates = [path for path in (repo_root / "dist").glob("*.exe") if path.name == expected_setup_name]
        if not candidates:
            raise VerifyError(f"找不到安装程序：dist\\{expected_setup_name}")
        setup = max(candidates, key=lambda path: path.stat().st_mtime)
    if not setup.is_file():
        raise VerifyError(f"找不到安装程序：{setup}")

    install_dir = Path(args.dir) if args.dir else repo_root / ".tmp-verify" / "install"
    return Paths(install_dir.resolve(), setup.resolve(), iss.resolve(), manifest, manifest_path,
                 expected_setup_name)


def _parse_args(argv):
    parser = argparse.ArgumentParser(description="DSH-AB 验收：13 条源码级门禁 + 5 个安装/卸载场景")
    parser.add_argument("--dir", default=None, help=r"安装目录（默认 <仓库根>\.tmp-verify\install）")
    parser.add_argument("--setup", default=None, help=r"安装包（默认 dist\<清单说的产物名>）")
    parser.add_argument("--iss", default=None, help=r"安装器脚本（默认 <仓库根>\installer\dsh-ab.iss）")
    parser.add_argument("--port", type=int, default=3190, help="静默安装用的生产端口（默认 3190）")
    parser.add_argument("--timeout-sec", type=int, default=900, help="单个安装/卸载进程的限时（默认 900）")
    parser.add_argument("--refuse-sec", type=int, default=300, help="「必须被拒绝」那两次运行的限时（默认 300）")
    parser.add_argument("--report", default=None, help=r"报告路径（默认 <仓库根>\.tmp-verify\report.txt）")
    return parser.parse_args(argv)


def main(argv=None):
    args = _parse_args(argv)
    repo_root = Path(__file__).resolve().parents[2]
    log_dir = repo_root / ".tmp-verify"
    log_dir.mkdir(parents=True, exist_ok=True)
    report_path = Path(args.report) if args.report else log_dir / "report.txt"
    rep = Report(report_path, repo_root)
    print(f"report: {report_path}")

    try:
        paths = _resolve(args, repo_root)
        rep.header({
            "安装目录": paths.install_dir,
            "安装包": paths.setup,
            "安装器脚本": paths.iss,
            "payload 清单": paths.manifest_path,
            "生产端口": args.port,
            "安装/卸载限时 / 拒绝限时": f"{args.timeout_sec} / {args.refuse_sec} 秒",
            "日志目录": log_dir,
        })

        rep.section("前置条件")
        rep.record("P1 payload 清单存在且有构建期文件记录", True,
                   f"{paths.manifest_path}（{len(paths.manifest.get('install_files') or [])} 个文件）")
        rep.record("P2 产物名符合 DSH-AB-<dshab版本>-<dsh版本>-setup.exe", True, paths.expected_setup_name)
        rep.record("P3 安装包存在", True, str(paths.setup))
        rep.record("P4 安装器脚本存在", True, str(paths.iss))

        rep.section("源码级门禁（13 条，installer\\dsh-ab.iss + naming.go，实现：dshab_build.isscheck）")
        problems = isscheck.check(paths.iss, repo_root)
        for index, problem in enumerate(problems, start=1):
            rep.record(f"源码级门禁 #{index}", False, problem)
        if problems:
            raise VerifyError(f"{len(problems)} 条源码级门禁没过：{'；'.join(problems)}")
        rep.record("源码级门禁 13 条全过", True, str(paths.iss))

        cfg = Config(
            repo_root=repo_root,
            install_dir=paths.install_dir,
            setup=paths.setup,
            log_dir=log_dir,
            manifest=paths.manifest,
            port=args.port,
            timeout_sec=args.timeout_sec,
            refuse_sec=args.refuse_sec,
        )
        scenarios.run(cfg, rep)
        print(f"OK install+uninstall exit=0 dir={cfg.install_dir}")
        return 0
    except VerifyError as exc:
        print(str(exc), file=sys.stderr)
        print(f"report: {report_path}", file=sys.stderr)
        return 1
    except Exception:
        traceback.print_exc()
        print(f"report: {report_path}", file=sys.stderr)
        return 1
    finally:
        rep.write()


if __name__ == "__main__":
    raise SystemExit(main())
