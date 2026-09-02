#!/usr/bin/env python3
"""Настройки пользователя машины: род, в котором агент пишет о себе.

  python3 user.py
      напечатать заданный род и путь конфига.

  python3 user.py --gender <мужской|женский>
      записать род и переписать страницу, которую сессии читают импортом.

Мимикрия велит держаться голоса пользователя, а род первого лица («проверил»
или «проверила») из корпуса не выводится. О себе пользователь пишет редко, и
дефолт модели совпадает с его родом случайно. Промахнувшись, текст выдаёт
агента с первого предложения, и видно это снаружи, в ревью и в описаниях задач.
Поэтому род называет сам пользователь, один раз на машину.

Хранение раздвоено нарочно. Значение живёт в `~/.devkit/user.local` строкой
`gender = женский`, рядом с остальными машинными конфигами, а в контекст сессии
едет не оно, а развёртка `~/.devkit/user.md`. Глобальная точка правил зовёт её
импортом, и текст приезжает и в сессию, и в субагента, и в вычитку. Без
развёртки пришлось бы вписывать род в тело самой точки, а её переписывает
генератор правил целиком, и раскладка стенда послушания легла бы под ту же
руку.

Страница лежит на месте всегда, даже когда род не задан. Импорт в пустоту
глобальную точку ломает, а заглушка ещё и говорит агенту, чего не хватает.
"""
import os
import sys
from pathlib import Path

CONFIG = "~/.devkit/user.local"
PAGE = "~/.devkit/user.md"
KEY = "gender"
GENDERS = ("мужской", "женский")
# Формы, которыми страница объясняет род: прилагательное в предложном падеже и
# пара глаголов примером. Таблицей, а не склонением по правилу: родов два, а
# правило склонения тянуло бы за собой словарь ради двух строк.
FORMS = {
    "мужской": ("мужского", "мужском", "проверил", "сверил"),
    "женский": ("женского", "женском", "проверила", "сверила"),
}
SET_HINT = "devkitctl user --gender <%s>" % "|".join(GENDERS)


def path_of(spec, home=None):
    """Путь из шапки модуля с подставленным домом. Тесты гоняют свой дом,
    живой прогон настоящий."""
    home = Path(home) if home else Path(os.path.expanduser("~"))
    return home / (spec[2:] if spec.startswith("~/") else spec.lstrip("/"))


def gender(home=None):
    """Заданный род или пустая строка. Разбор терпимый, как у остальных
    машинных конфигов. Незнакомое значение это тот же незаданный род, и
    находка доктора называет его вместе с командой."""
    try:
        text = path_of(CONFIG, home).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""
    for ln in text.splitlines():
        key, sep, val = ln.partition("=")
        if sep and key.strip() == KEY and val.strip() in GENDERS:
            return val.strip()
    return ""


def page_text(value):
    """Страница, которую сессия читает импортом. Заданный род идёт указанием,
    незаданный просьбой его назвать. Сессия видит нехватку тем же текстом, не
    дожидаясь доктора."""
    head = ("# Пользователь машины\n\n"
            "Страницу пишет `devkitctl user`, руками её не править. В контекст "
            "сессии она\nедет импортом из глобальной точки правил.\n\n")
    if value not in FORMS:
        return (head + "Род первого лица не задан, и агент пишет о себе дефолтом модели. "
                       "Пока он\nне назван, о себе лучше писать так, чтобы род не всплывал, а "
                       "пользователя\nспросить и записать ответ командой `%s`.\n" % SET_HINT)
    genitive, prepositional, first, second = FORMS[value]
    return (head + "Пользователь %s рода. О себе в текстах от первого лица писать в %s\n"
                   "роде («%s», «%s»): в чате, в описаниях задач, в ревью, в тексте\n"
                   "коммита и в комментариях кода.\n"
            % (genitive, prepositional, first, second))


def check(fix, home=None):
    """Находка доктора: род не задан, либо страница разошлась с конфигом.

    Страницу доктор кладёт сам и без спроса. Её зовёт импортом глобальная
    точка, а импорт в пустоту у клиента не разворачивается вовсе, и вместе с
    ним тихо пропадает всё, что точка везёт. Род доктор не угадывает.
    Назвать его может только человек.
    """
    findings, fixed = [], []
    value = gender(home)
    page = path_of(PAGE, home)
    want = page_text(value)
    try:
        got = page.read_text(encoding="utf-8", errors="replace")
    except OSError:
        got = None
    if got != want:
        if fix:
            page.parent.mkdir(parents=True, exist_ok=True)
            page.write_text(want, encoding="utf-8")
            fixed.append("написана страница рода первого лица: %s" % page)
        else:
            findings.append("%s: страница рода первого лица %s, и сессия читает импорт в пустоту; "
                            "написать: devkitctl doctor --fix"
                            % (page, "разошлась с конфигом" if got is not None else "не написана"))
    if not value:
        findings.append("род первого лица не задан (%s): агент пишет о себе дефолтом модели, и в "
                        "текстах наружу чужой род видно с первой фразы; поставить: %s"
                        % (path_of(CONFIG, home), SET_HINT))
    return findings, fixed


def set_gender(value, home=None):
    """Записать род и переписать страницу. Конфиг перечитывается построчно.
    Рядом с родом лягут другие настройки пользователя, и затирать их нельзя."""
    if value not in GENDERS:
        raise ValueError("род это %s, а не %r" % (" или ".join(GENDERS), value))
    conf = path_of(CONFIG, home)
    conf.parent.mkdir(parents=True, exist_ok=True)
    lines, seen = [], False
    if conf.exists():
        for ln in conf.read_text(encoding="utf-8", errors="replace").splitlines():
            key, sep, _ = ln.partition("=")
            if sep and key.strip() == KEY:
                lines.append("%s = %s" % (KEY, value))
                seen = True
            else:
                lines.append(ln)
    if not seen:
        lines.append("%s = %s" % (KEY, value))
    conf.write_text("\n".join(lines) + "\n", encoding="utf-8")
    page = path_of(PAGE, home)
    page.parent.mkdir(parents=True, exist_ok=True)
    page.write_text(page_text(value), encoding="utf-8")
    return conf, page


def main(argv):
    value = ""
    if argv and argv[0] == "--gender":
        if len(argv) < 2:
            sys.stderr.write("род не назван: %s\n" % SET_HINT)
            return 2
        value = argv[1]
    elif argv:
        sys.stderr.write("непонятный ключ %r: %s\n" % (argv[0], SET_HINT))
        return 2
    if not value:
        now = gender()
        print("род первого лица: %s" % (now or "не задан"))
        print("конфиг: %s" % path_of(CONFIG))
        print("страница в контекст: %s" % path_of(PAGE))
        if not now:
            print("поставить: %s" % SET_HINT)
        return 0 if now else 1
    try:
        conf, page = set_gender(value)
    except ValueError as err:
        sys.stderr.write("%s\n" % err)
        return 2
    print("род первого лица: %s (%s)" % (value, conf))
    print("страница в контекст переписана: %s" % page)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
