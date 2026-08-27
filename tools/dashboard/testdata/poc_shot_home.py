#!/usr/bin/env python3
"""Снимок главной настоящим браузером (ветка poc-chat).

Плюс у карточки проекта и его меню это геометрия: попадание кнопки в правый
край строки и то, как меню ложится под неё, моком не проверить. Снимается тем
же способом, что и лента (poc_shot_page.py): сперва прокси с кукой входа
отдаёт собранную разметку, потом она кладётся страницей без скриптов и
снимается. Меню на живой странице развёрнуто нажатием, а нажать в
--dump-dom нечем, поэтому во второй снимок оно вписывается той же разметкой,
какую собирает makePlus.

Тем же ходом снимается любой другой экран: хэш приходит четвёртым доводом
(«devkit» это доска проекта, «/agents» раздел агентов), и тогда меню в снимок
не вписывается, оно принадлежит главной.

Ширина окна пятым доводом: телефонная вёрстка живёт за 900 точками, и
проверять её надо в её же ширине.

Зовётся: python3 testdata/poc_shot_home.py <база> <токен> <файл.png> [хэш] [ширина]
"""
import os
import re
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from poc_browser import CHROME, cookie, headless_flag, proxy
from poc_shot_page import fetch, shot_width, still

MENU = ('<div class="pmenu"><div class="pmrow">Задача</div>'
        '<div class="pmrow">Черновик</div></div>')


def dom(chrome_base, profile, hash_):
    cmd = [CHROME, headless_flag("old"), "--disable-gpu", "--no-sandbox", "--no-first-run",
           "--user-data-dir=" + profile, "--virtual-time-budget=4000", "--dump-dom",
           chrome_base + "/#" + hash_]
    # Главная не успокаивается: опросы ленты и подписок идут по таймеру, и срок
    # виртуального времени на ней не кончается. Разметку chrome печатает
    # вовремя, поэтому берётся напечатанное, а само окно снимается сроком.
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=40).stdout or ""
    except subprocess.TimeoutExpired as e:
        return (e.stdout or b"").decode("utf-8", "replace") if isinstance(e.stdout, bytes) \
            else (e.stdout or "")


def page(text, css, menu):
    html = still(text, css)
    if menu:
        # Меню разворачивается у первой карточки списка: снимок про то, как оно
        # ложится под плюс, и второй карточки для этого не нужно.
        html = html.replace('+</button></div>', '+</button>' + MENU + '</div>', 1)
    return html


def shot(base, token, out, hash_="", width="1400"):
    if not os.path.exists(CHROME):
        print("chrome не найден, снимок пропущен")
        return 1
    srv, chrome_base = proxy(base, cookie(token))
    try:
        with tempfile.TemporaryDirectory() as profile:
            text = dom(chrome_base, profile, hash_)
            css = fetch(chrome_base, re.search(r'href="(/assets/style\.css[^"]*)"', text).group(1))
    finally:
        srv.shutdown()
    shots = []
    # Меню плюса это разметка главной: на других экранах его вписывать некуда, и
    # снимок там один.
    for menu, tail in ((False, ""), (True, "-menu")) if not hash_ else ((False, ""),):
        html = os.path.join(tempfile.gettempdir(), "poc-home%s.html" % tail)
        with open(html, "w") as f:
            f.write(page(text, css, menu))
        name = out if not tail else out.replace(".png", tail + ".png")
        with tempfile.TemporaryDirectory() as profile:
            # Снимок кладётся на диск сразу, а само окно иногда не закрывается:
            # судьба снимка решается по файлу, а не по коду возврата chrome.
            try:
                subprocess.run(
                    [CHROME, headless_flag("new"), "--disable-gpu", "--no-sandbox", "--no-first-run",
                     "--hide-scrollbars", "--user-data-dir=" + profile,
                     "--window-size=%s,900" % width, "--screenshot=" + name, "file://" + html],
                    capture_output=True, text=True, timeout=40)
            except subprocess.TimeoutExpired:
                pass
        if not os.path.exists(name):
            print("снимок не получился: " + name)
            return 1
        # Снимок отвечает за ширину, которую у него просили. Полный Chrome на
        # macOS окно уже пятисот точек не открывает, и «снимок на 390» приезжал
        # пятисотточечным: телефонная вёрстка на нём выглядела вылезающей за
        # экран, а правки её «не чинили», потому что чинить было нечего (разбор
        # POC DK-397).
        got = shot_width(name)
        if got and got != int(width):
            print("снимок вышел шириной %d точек вместо %d: %s окно такой ширины не "
                  "открывает, нужен chrome-headless-shell" % (got, int(width), CHROME))
            return 1
        shots.append(name + " (" + str(got) + " точек)")
    print("снимки: " + ", ".join(shots))
    return 0


if __name__ == "__main__":
    if len(sys.argv) < 4:
        sys.stderr.write(__doc__)
        raise SystemExit(2)
    raise SystemExit(shot(*sys.argv[1:6]))
