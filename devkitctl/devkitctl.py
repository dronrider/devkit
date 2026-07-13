#!/usr/bin/env python3
"""devkitctl: обвязка devkit в проекте одной командой.

  devkitctl new --prefix XX [--name "..."] [--no-board] [-C dir]
      подключить проект: CLAUDE.md из шаблона (импорты пересчитываются под
      реальный путь до devkit), git-хуки, доска через taskctl init;
      --no-board для проектов с внешним трекером

  devkitctl doctor [-C dir]
      проверить обвязку: импорты CLAUDE.md разворачиваются, git-хуки и
      PostToolUse-хуки подключены, taskctl в PATH и не старее исходников,
      инварианты доски (taskctl lint), локальные markdown-ссылки не битые

Выход 0 всё в порядке, 1 есть находки, 2 ошибка запуска.
"""
import argparse
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

DEVKIT = Path(__file__).resolve().parent.parent
HOOK_SCRIPTS = ("check-symbols.py", "check-memory.py", "check-sensitive.py")
SKIP_DIRS = {".git", "node_modules", "vendor", "target", "local-docs",
             ".venv", "venv", "__pycache__", ".idea", ".vscode"}
LINK_RE = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")


def run(args, cwd=None):
    p = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
    return p.returncode, (p.stdout + p.stderr).strip()


def project_root(start):
    rc, out = run(["git", "-C", start, "rev-parse", "--show-toplevel"])
    if rc == 0:
        return Path(out), True
    return Path(start).resolve(), False


def check_links(root):
    findings = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            if not fn.endswith(".md"):
                continue
            md = Path(dirpath) / fn
            rel = os.path.relpath(md, root)
            try:
                lines = md.read_text(encoding="utf-8", errors="replace").splitlines()
            except OSError:
                continue
            for i, ln in enumerate(lines, 1):
                for m in LINK_RE.finditer(ln):
                    target = m.group(1).split("#")[0]
                    if not target or "://" in target or target.startswith("mailto:"):
                        continue
                    if not (md.parent / target).exists():
                        findings.append("%s:%d: битая ссылка (%s)" % (rel, i, m.group(1)))
    return findings


def check_git_hooks(root):
    hooks_dir = (DEVKIT / "hooks").resolve()
    rc, hp = run(["git", "config", "core.hooksPath"], cwd=root)
    if rc == 0 and hp:
        cand = Path(os.path.expanduser(hp))
        if not cand.is_absolute():
            cand = root / cand
        if cand.exists() and cand.resolve() == hooks_dir:
            return None
    else:
        rc, gp = run(["git", "rev-parse", "--git-path", "hooks"], cwd=root)
        pre = (Path(gp) if os.path.isabs(gp) else root / gp) / "pre-commit"
        if pre.exists() and Path(os.path.realpath(pre)).parent == hooks_dir:
            return None
    rel = os.path.relpath(hooks_dir, root)
    return "git-хуки devkit не подключены; из корня проекта: git config core.hooksPath %s" % rel


def doctor(start):
    findings = []
    root, in_git = project_root(start)
    if not in_git:
        findings.append("не git-репозиторий: %s" % root)
    claude = root / "CLAUDE.md"
    if not claude.exists():
        findings.append("нет CLAUDE.md в корне проекта; подключение: devkitctl new --prefix XX")
    else:
        for i, ln in enumerate(claude.read_text(encoding="utf-8").splitlines(), 1):
            ln = ln.strip()
            if not ln.startswith("@"):
                continue
            target = Path(os.path.expanduser(ln[1:]))
            if not target.is_absolute():
                target = root / target
            if not target.exists():
                findings.append("CLAUDE.md:%d: импорт %s не разворачивается (devkit склонирован рядом?)" % (i, ln))
    if in_git:
        f = check_git_hooks(root)
        if f:
            findings.append(f)
    settings = Path(os.path.expanduser("~/.claude/settings.json"))
    text = settings.read_text(encoding="utf-8") if settings.exists() else ""
    for script in HOOK_SCRIPTS:
        if script not in text:
            findings.append("PostToolUse-хук %s не подключён в %s (hooks/README.md)" % (script, settings))
    if (root / "docs" / "TASKS.md").exists():
        tc = shutil.which("taskctl")
        if not tc:
            findings.append("есть доска, а taskctl не в PATH: cd %s/taskctl && go build -o ~/go/bin/taskctl ." % DEVKIT)
        else:
            src = max((p.stat().st_mtime for p in (DEVKIT / "taskctl").glob("*.go")), default=0)
            if os.path.getmtime(tc) < src:
                findings.append("бинарь taskctl старее исходников devkit, пересобрать его")
            rc, out = run([tc, "-C", str(root), "lint"])
            if rc != 0:
                findings.append("taskctl lint: %s" % out)
    findings += check_links(root)
    for f in findings:
        print(f)
    if findings:
        sys.stderr.write("находок: %d\n" % len(findings))
        return 1
    print("обвязка в порядке")
    return 0


def new(start, prefix, name, no_board):
    root, in_git = project_root(start)
    claude = root / "CLAUDE.md"
    if claude.exists():
        sys.stderr.write("CLAUDE.md уже есть, проект подключён; проверка: devkitctl doctor\n")
        return 2
    if not no_board and not prefix:
        sys.stderr.write("нужен --prefix для доски либо --no-board, когда задачи во внешнем трекере\n")
        return 2
    text = (DEVKIT / "templates" / "CLAUDE.project.md").read_text(encoding="utf-8")
    if text.startswith("<!--"):
        text = text[text.index("-->") + 3:].lstrip("\n")
    name = name or root.name
    text = text.replace("<название проекта>", name).replace("<XX>", prefix or "XX")
    rel = Path(os.path.relpath(DEVKIT, root)).as_posix()
    if rel != "../devkit":
        # Шаблон рассчитан на devkit в соседней директории; когда он лежит
        # иначе, импорты без пересчёта молча не развернутся.
        text = text.replace("@../devkit/", "@%s/" % rel)
    claude.write_text(text, encoding="utf-8")
    done = ["CLAUDE.md создан из шаблона"]
    if in_git:
        finding = check_git_hooks(root)
        if finding is None:
            done.append("git-хуки уже подключены")
        else:
            rc, gp = run(["git", "rev-parse", "--git-path", "hooks"], cwd=root)
            hdir = Path(gp) if os.path.isabs(gp) else root / gp
            custom = [p.name for p in hdir.glob("*") if p.is_file() and not p.name.endswith(".sample")]
            if custom:
                done.append("в хуках проекта уже есть свои (%s), core.hooksPath не трогается; "
                            "симлинки на хуки devkit по hooks/README.md" % ", ".join(custom))
            else:
                hooks_rel = os.path.relpath((DEVKIT / "hooks").resolve(), root)
                run(["git", "config", "core.hooksPath", hooks_rel], cwd=root)
                done.append("git-хуки: core.hooksPath = %s" % hooks_rel)
    else:
        done.append("не git-репозиторий, git-хуки не подключались")
    if no_board:
        done.append("доска не заводилась: убрать импорт RULES.board.md из CLAUDE.md и вписать внешний трекер")
    else:
        tc = shutil.which("taskctl")
        if tc:
            rc, out = run([tc, "-C", str(root), "init", "--prefix", prefix, "--name", name])
            if rc != 0:
                sys.stderr.write("taskctl init: %s\n" % out)
                return 1
            done.append(out)
        else:
            done.append("taskctl не в PATH; доска заводится после сборки: "
                        "cd %s/taskctl && go build -o ~/go/bin/taskctl . && taskctl -C %s init --prefix %s"
                        % (DEVKIT, root, prefix))
    print("\n".join(done))
    return 0


def main(argv):
    ap = argparse.ArgumentParser(prog="devkitctl", description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    d = sub.add_parser("doctor", help="проверить обвязку проекта")
    d.add_argument("-C", dest="dir", default=".", help="директория проекта")
    n = sub.add_parser("new", help="подключить новый проект")
    n.add_argument("-C", dest="dir", default=".", help="директория проекта")
    n.add_argument("--prefix", default="", help="префикс ID задач доски, заглавными (XR)")
    n.add_argument("--name", default="", help="название проекта, по умолчанию имя директории")
    n.add_argument("--no-board", action="store_true", help="без доски, задачи во внешнем трекере")
    a = ap.parse_args(argv)
    if a.cmd == "doctor":
        return doctor(a.dir)
    return new(a.dir, a.prefix.upper(), a.name, a.no_board)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
