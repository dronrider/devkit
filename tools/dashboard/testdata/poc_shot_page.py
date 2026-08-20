#!/usr/bin/env python3
"""Снимок ленты настоящим браузером (ветка poc-chat).

Вёрстку ленты глазами мока не проверить: связка линией, кружки исхода и их
попадание в первую строку записи это чистая геометрия, и видно её только на
снимке. Снимается в два хода, потому что живую страницу дашборда chrome со
снимком не отпускает: у неё вечные опросы, и срок виртуального времени не
кончается никогда. Сперва тем же прокси с кукой входа снимается собранная
разметка (--dump-dom, так работает и poc_browser.py), потом она кладётся
рядом со стилями отдельной страницей без скриптов, и снимок делается уже с
неё.

Зовётся: python3 testdata/poc_shot_page.py <база> <токен> <проект> <сессия> <файл.png>
"""
import os
import re
import subprocess
import sys
import tempfile
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from poc_browser import CHROME, cookie, proxy


def fetch(chrome_base, path):
    with urllib.request.urlopen(chrome_base + path, timeout=20) as r:
        return r.read().decode("utf-8", "replace")


def dom(chrome_base, project, sid, profile):
    page = "%s/#%s/chat/%s" % (chrome_base, project, sid)
    out = subprocess.run(
        [CHROME, "--headless=old", "--disable-gpu", "--no-sandbox", "--no-first-run",
         "--user-data-dir=" + profile, "--virtual-time-budget=8000", "--dump-dom", page],
        capture_output=True, text=True, timeout=120)
    return out.stdout or ""


def still(text, css):
    """Страница без скриптов и опросов: тот же html, стили внутрь."""
    text = re.sub(r"(?s)<script.*?</script>", "", text)
    text = re.sub(r'(?s)<link[^>]+rel="stylesheet"[^>]*>', "<style>" + css + "</style>", text)
    return text


def shot(base, token, project, sid, out):
    if not os.path.exists(CHROME):
        print("chrome не найден, снимок пропущен")
        return 1
    srv, chrome_base = proxy(base, cookie(token))
    try:
        with tempfile.TemporaryDirectory() as profile:
            page = dom(chrome_base, project, sid, profile)
            css = fetch(chrome_base, re.search(r'href="(/assets/style\.css[^"]*)"', page).group(1))
        html = os.path.join(tempfile.gettempdir(), "poc-feed.html")
        with open(html, "w") as f:
            f.write(still(page, css))
    finally:
        srv.shutdown()
    with tempfile.TemporaryDirectory() as profile:
        subprocess.run(
            # headless=new нарочно: старый режим со снимком на этой машине
            # висит до срока, а разметку он же отдаёт исправно.
            [CHROME, "--headless=new", "--disable-gpu", "--no-sandbox", "--no-first-run",
             "--hide-scrollbars", "--user-data-dir=" + profile,
             "--window-size=1400,1100", "--screenshot=" + out, "file://" + html],
            capture_output=True, text=True, timeout=120)
    if not os.path.exists(out):
        print("снимок не получился")
        return 1
    print("снимок: %s, байт %d" % (out, os.path.getsize(out)))
    return 0


if __name__ == "__main__":
    if len(sys.argv) < 6:
        sys.stderr.write(__doc__)
        raise SystemExit(2)
    raise SystemExit(shot(*sys.argv[1:6]))
