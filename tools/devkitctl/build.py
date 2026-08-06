#!/usr/bin/env python3
"""Сборка бинарей devkit с зашитой версией: одна точка на CI и на машину.

Решение в docs/lld/DK-149-binary-delivery.md, разделы «Решение 2» и «Сборка
релиза». Что собирается, выводится из дерева, а не из списка в коде: каталоги
tools/*/ с go.mod. По правилу языка go тут это ровно то, что ставится в PATH,
поэтому список сборки, список установки и список проверки доктора совпадают по
построению, а не по внимательности, и новая утилита попадает в релиз тем, что
появилась.

Собранное проверяется тут же, но по-разному. Бинарь своей пары GOOS/GOARCH
запускается с --version и обязан напечатать ту строку, которую в него зашивали:
утилита, забывшая флаг, роняет сборку. Кросс-собранный запустить нечем
(darwin на ubuntu даёт exec format error), и он проверяется байтами: зашитая
строка обязана найтись в артефакте. Проверка слабее, но ловит главную беду
-ldflags -X, несуществующий символ линковщик молча пропускает.
"""
import hashlib
import os
import platform
import shutil
import subprocess
import tarfile
import tempfile
from pathlib import Path

# Четыре пары под релиз. Кросс-сборка идёт на одном раннере: внешних
# зависимостей у devkit нет, cgo не нужен, матрица раннеров не окупается.
PLATFORMS = (("darwin", "amd64"), ("darwin", "arm64"),
             ("linux", "amd64"), ("linux", "arm64"))
ASSET = "devkit-%s-%s-%s.tar.gz"
SUMS = "SHA256SUMS"
# Имена GOOS/GOARCH по тому, что говорит про машину python: спрашивать go env
# было бы честнее, но своя пара нужна и там, где сборка ещё не началась.
GOOS_BY_SYSTEM = {"Darwin": "darwin", "Linux": "linux"}
GOARCH_BY_MACHINE = {"x86_64": "amd64", "amd64": "amd64",
                     "arm64": "arm64", "aarch64": "arm64"}


def run(args, cwd=None, env=None):
    p = subprocess.run([str(a) for a in args], cwd=cwd and str(cwd), env=env,
                       capture_output=True, text=True)
    return p.returncode, (p.stdout + p.stderr).strip()


def tools(devkit):
    """Утилиты релиза: каталоги tools/*/ с go.mod, по алфавиту."""
    return sorted(p.parent.name for p in (Path(devkit) / "tools").glob("*/go.mod"))


def stamp(devkit):
    """Версия и коммит из git того чекаута, из которого идёт сборка.

    На релизной сборке HEAD стоит на теге и describe даёт v0.2.0, на машине
    разработчика v0.2.0-7-gabc1234[-dirty], то есть строка версии сама говорит,
    что бинарь не релизный. Не git и не чекаут devkit, значит остаются
    умолчания, те же, что зашиты в исходник.

    Считаются только релизные теги (--match v*). В devkit живёт служебный тег
    deployed, который shipctl двигает на каждом выкате, и без отбора версия
    вышла бы «deployed-5-gbb39651»: имя тега выката вместо номера релиза.
    """
    rc, out = run(["git", "-C", str(devkit), "describe",
                   "--tags", "--match", "v*", "--always", "--dirty"])
    version = out if rc == 0 and out else "dev"
    rc, out = run(["git", "-C", str(devkit), "rev-parse", "--short=12", "HEAD"])
    commit = out if rc == 0 and out else "unknown"
    return version, commit


def version_line(name, version, commit):
    """Строка, которую печатает утилита по --version. Формат один на все шесть."""
    return "%s %s (%s)" % (name, version, commit)


def host_pair():
    """Пара GOOS/GOARCH этой машины: её бинарь единственный, что можно запустить."""
    system = platform.system()
    machine = platform.machine()
    return (GOOS_BY_SYSTEM.get(system, system.lower()),
            GOARCH_BY_MACHINE.get(machine, machine))


def go_env(goos="", goarch=""):
    env = dict(os.environ)
    # Чужой go.work на машине увёл бы сборку из модуля утилиты («directory
    # prefix . does not contain modules listed in go.work»): модулей у devkit
    # шесть, своего go.work у него нет, и в чужой они не входят.
    env["GOWORK"] = "off"
    # Без cgo бинарь одинаково работает на любой машине этой пары, а
    # кросс-сборка обходится без чужого тулчейна.
    env["CGO_ENABLED"] = "0"
    if goos:
        env["GOOS"], env["GOARCH"] = goos, goarch
    return env


def compile_one(devkit, name, target, version, commit, goos="", goarch=""):
    """Собрать одну утилиту с зашитой версией. Отдаёт текст находки либо None."""
    target = Path(target)
    target.parent.mkdir(parents=True, exist_ok=True)
    ldflags = "-X main.version=%s -X main.commit=%s" % (version, commit)
    rc, out = run(["go", "build", "-ldflags", ldflags, "-o", str(target), "."],
                  cwd=Path(devkit) / "tools" / name, env=go_env(goos, goarch))
    if rc != 0:
        return "%s: сборка не прошла: %s" % (name, out)
    return None


def check_run(name, target, version, commit):
    """Самопроверка запуском: бинарь обязан напечатать зашитую строку версии."""
    want = version_line(name, version, commit)
    rc, out = run([str(target), "--version"])
    got = out.splitlines()[0] if out.splitlines() else ""
    if rc != 0 or got != want:
        return "%s: ждали от --version «%s», получили «%s»" % (name, want, got or "пусто")
    return None


def check_bytes(name, target, version, commit):
    """Проверка кросс-собранного: зашитая строка обязана найтись в артефакте."""
    blob = Path(target).read_bytes()
    missing = [what for what, value in (("версии", version), ("коммита", commit))
               if value.encode("utf-8") not in blob]
    if missing:
        return ("%s: в бинаре нет зашитой %s, проверить имена переменных в -ldflags -X "
                "(несуществующий символ линковщик молча пропускает)"
                % (name, " и ".join(missing)))
    return None


def local(devkit, out, log=print):
    """Сборка под текущую машину: бинари в каталог назначения, каждый проверен
    запуском. Отдаёт список находок.
    """
    names = tools(devkit)
    if not names:
        return ["в %s/tools нет ни одного каталога с go.mod: собирать нечего" % devkit]
    version, commit = stamp(devkit)
    findings = []
    for name in names:
        target = Path(out) / name
        err = (compile_one(devkit, name, target, version, commit)
               or check_run(name, target, version, commit))
        if err:
            findings.append(err)
            continue
        log("%s -> %s" % (version_line(name, version, commit), target))
    return findings


def release(devkit, out, log=print):
    """Релизная сборка: четыре пары, тарболл на пару и SHA256SUMS рядом.

    Внутри тарболла плоско лежат все утилиты релиза. Отдаёт список находок.
    """
    names = tools(devkit)
    if not names:
        return ["в %s/tools нет ни одного каталога с go.mod: собирать нечего" % devkit]
    version, commit = stamp(devkit)
    out = Path(out)
    out.mkdir(parents=True, exist_ok=True)
    # Версия печатается первой строкой и в релизной сборке, и на машине: по
    # логу прогона видно, что именно собралось, ещё до имён тарболлов.
    log("devkit %s (%s): утилит %d, пар %d" % (version, commit, len(names), len(PLATFORMS)))
    host = host_pair()
    if host not in PLATFORMS:
        log("пара этой машины (%s/%s) не из четырёх релизных: живьём не проверяется ни одна, "
            "все четыре идут байтовой проверкой" % host)
    findings, assets = [], []
    # Собранное складывается рядом с тарболлами, а не в системной временной
    # директории: каталог назначения выбрал тот, кто звал сборку, и мусор после
    # обрыва останется на виду у него же, а не там, куда никто не смотрит.
    stage_root = Path(tempfile.mkdtemp(prefix=".stage-", dir=str(out)))
    try:
        for goos, goarch in PLATFORMS:
            pair = "%s/%s" % (goos, goarch)
            native = (goos, goarch) == host
            stage = stage_root / ("%s-%s" % (goos, goarch))
            bad = False
            for name in names:
                target = stage / name
                err = compile_one(devkit, name, target, version, commit, goos, goarch)
                if err is None:
                    check = check_run if native else check_bytes
                    err = check(name, target, version, commit)
                if err:
                    findings.append("%s: %s" % (pair, err))
                    bad = True
            if bad:
                continue
            asset = out / (ASSET % (version, goos, goarch))
            with tarfile.open(str(asset), "w:gz") as tf:
                for name in names:
                    tf.add(str(stage / name), arcname=name)
            assets.append(asset)
            log("%s: %d утилит, проверка %s -> %s"
                % (pair, len(names), "запуском" if native else "байтами", asset.name))
    finally:
        shutil.rmtree(str(stage_root), ignore_errors=True)
    if assets:
        sums = out / SUMS
        sums.write_text("".join("%s  %s\n" % (sha256(a), a.name) for a in assets),
                        encoding="utf-8")
        log("%s, строк: %d" % (sums, len(assets)))
    return findings


def sha256(path):
    h = hashlib.sha256()
    with open(str(path), "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()
