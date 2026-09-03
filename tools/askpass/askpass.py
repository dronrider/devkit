#!/usr/bin/env python3
"""Помощник ввода пароля для sudo и ssh без терминала (DK-772).

Строка с `!` из чата дашборда доходит до терминала живой сессии, а sudo и
ssh в ней сидят без tty: пароль показать некуда, и команда отказывает
словами про отсутствующий терминал. Дашборд подставляет себя помощником
через SUDO_ASKPASS и SSH_ASKPASS: sudo и ssh зовут этот скрипт с текстом
запроса первым аргументом и ждут пароль строкой в stdout.

Самим паролем скрипт не распоряжается. Он стучится в демон дашборда
POST /api/askpass, тот кладёт вопрос в память и показывает его в ленте
разговора закрытым полем, человек отвечает панели, а демон держит этот ход
висящим, пока не придёт ответ, отмена или не выйдет срок (120 секунд).
Пароль оседает только в теле ответа демона и тут же уходит в stdout: на
диск, в лог или куда-то ещё этот скрипт его не пишет.

Разговор называет DEVKIT_CHAT (когда он уже известен на момент подъёма
сессии) либо запасной DEVKIT_TMUX (демон находит разговор обратным поиском
по имени tmux-сессии в своём реестре). Адрес демона несёт DEVKIT_ADDR, секрет
локального входа лежит файлом под ~/.devkit/askpass.local (демон пишет его
на каждом старте и держит в памяти для сверки). Все три переменные и путь
самого этого файла кладёт launchEnv (tools/dashboard/chats.go), а раскладку
файла на машину делает `devkitctl doctor --fix` (tools/devkitctl/devkitctl.py,
check_askpass_helper).
"""
import json
import os
import sys
import urllib.error
import urllib.request

SECRET_PATH = os.path.expanduser("~/.devkit/askpass.local")
# Запас над сроком ожидания демона (askpassTimeout = 120с в askpass.go):
# сеть и раздумья человека сверх этого срока уже дело демона, не клиента.
TIMEOUT = 130


def fail(msg):
    sys.stderr.write("askpass: " + msg + "\n")
    sys.exit(1)


def read_secret():
    try:
        with open(SECRET_PATH, encoding="utf-8") as f:
            local_key = f.read().strip()
    except OSError as exc:
        fail("секрет %s не читается (%s): демон не поднят, или раскладка легла не под этот "
             "дом, devkitctl doctor --fix" % (SECRET_PATH, exc))
    if not local_key:
        fail("секрет %s пуст" % SECRET_PATH)
    return local_key


def addr_of(env_value):
    # Демон, слушающий все интерфейсы, называет адрес с пустым хостом
    # (DEVKIT_ADDR вида ":7112"): для похода изнутри той же машины пустой
    # хост подменяется петлёй, иначе "http://:7112" никуда не достучится.
    host, _, port = env_value.rpartition(":")
    if not host:
        host = "127.0.0.1"
    return host + ":" + port


def ask(addr, local_key, chat, tmux, prompt):
    body = json.dumps({"chat": chat, "tmux": tmux, "prompt": prompt}).encode("utf-8")
    req = urllib.request.Request(
        "http://%s/api/askpass" % addr, data=body, method="POST",
        headers={"Content-Type": "application/json", "X-Devkit-Askpass-Secret": local_key})
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        reason = ""
        try:
            reason = json.loads(exc.read().decode("utf-8")).get("error", "")
        except (ValueError, OSError):
            pass
        finally:
            exc.close()
        if exc.code == 410:
            fail("отменено человеком")
        if exc.code == 504:
            fail("срок ожидания пароля вышел")
        fail("демон отказал (%s): %s" % (exc.code, reason or exc.reason))
    except urllib.error.URLError as exc:
        fail("демон %s не ответил: %s" % (addr, exc.reason))


def main(argv, env):
    if len(argv) < 2:
        fail("жду текст запроса первым аргументом (так зовут sudo и ssh)")
    prompt = argv[1]

    addr_env = env.get("DEVKIT_ADDR", "")
    if not addr_env:
        fail("DEVKIT_ADDR пуст: сессия поднята не дашбордом, помощнику стучаться некуда")

    local_key = read_secret()
    answer = ask(addr_of(addr_env), local_key, env.get("DEVKIT_CHAT", ""),
                 env.get("DEVKIT_TMUX", ""), prompt)
    typed = answer.get("password", "")
    if not typed:
        fail("демон ответил без пароля")
    sys.stdout.write(typed + "\n")


if __name__ == "__main__":
    main(sys.argv, os.environ)
