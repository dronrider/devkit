#!/bin/sh
# Съёмщик остатка Codex для agentctl quota refresh. Контракт съёмщика в
# docs/lld/DK-033-universal-kit.md, раздел «Контракт съёмщика».
#
# Источник остатка это штатный codex app-server: он поднимается из PATH с
# ключом --stdio, говорит JSON-RPC строками через перевод строки и отвечает на
# account/rateLimits/read. Модельный ход и открытый чат для чтения не нужны,
# вход берётся глобальный из ~/.codex. На stdout попадают только проценты и
# даты сброса: токены и данные учётной записи не печатаются.
set -eu

if [ -z "${DEVKIT_HARNESS:-}" ]; then
    echo "переменная DEVKIT_HARNESS пуста: её передаёт agentctl quota refresh" >&2
    exit 1
fi

python3 - <<'PY'
import datetime, json, queue, subprocess, sys, threading, time

# Потолок ожидания на весь обмен: зависший app-server не должен держать
# периодический сборщик квот бесконечно.
DEADLINE_SECS = 30
# Окно классифицируется длиной из ответа, а не позицией primary/secondary:
# недельное приезжает и тем и другим местом, и позиция ничего не обещает.
WINDOWS = {300: "window5h_all", 10080: "week_all"}

try:
    proc = subprocess.Popen(
        ["codex", "app-server", "--stdio"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL, text=True, bufsize=1)
except OSError as e:
    sys.exit("codex app-server не поднялся из PATH: %s" % e)

def kill():
    if proc.poll() is None:
        proc.kill()
    proc.wait()

def fail(why):
    sys.stderr.write(why + "\n")
    sys.exit(1)

# Чтение идёт нитью с очередью: select по pipe не видит строк, уже посаженных
# в буфер читателем, и смешение двух способов роняло бы обмен на ровном месте.
lines = queue.Queue()
def read_lines():
    for line in proc.stdout:
        lines.put(line)
    lines.put(None)
threading.Thread(target=read_lines, daemon=True).start()
deadline = time.monotonic() + DEADLINE_SECS

def send(msg):
    try:
        proc.stdin.write(json.dumps(msg) + "\n")
        proc.stdin.flush()
    except OSError as e:
        fail("записать в app-server не вышло: %s" % e)

def answer(want, what):
    while True:
        left = deadline - time.monotonic()
        if left <= 0:
            fail("app-server не ответил на %s за %d с" % (what, DEADLINE_SECS))
        try:
            line = lines.get(timeout=left)
        except queue.Empty:
            fail("app-server не ответил на %s за %d с" % (what, DEADLINE_SECS))
        if line is None:
            fail("процесс app-server вышел, не ответив на %s" % what)
        try:
            msg = json.loads(line)
        except ValueError:
            continue
        if isinstance(msg, dict) and msg.get("id") == want:
            return msg

# Порождённый сервер обязан завершаться в любом исходе, и весь обмен с разбором
# стоит под finally: к kill приходят и честный отказ, и сломавшийся разбор.
try:
    # Приветствие повторяет подъём дашборда (tools/dashboard/appchat.go,
    # appSpawn) тем же clientInfo и capabilities. Живой прогон обходился одним
    # clientInfo, поэтому experimentalApi здесь не условие метода, а сверка
    # составом с дашбордом.
    send({"jsonrpc": "2.0", "id": 1, "method": "initialize",
          "params": {"clientInfo": {"name": "devkit-quota", "version": "0"},
                     "capabilities": {"experimentalApi": True}}})
    hello = answer(1, "initialize")
    if "error" in hello:
        fail("initialize не прошёл: %s" % hello["error"].get("message"))
    send({"jsonrpc": "2.0", "method": "initialized", "params": {}})
    send({"jsonrpc": "2.0", "id": 2, "method": "account/rateLimits/read", "params": {}})
    reply = answer(2, "account/rateLimits/read")
    if "error" in reply:
        fail("источник квот отказал: %s" % reply["error"].get("message"))
    result = reply.get("result")
    if not isinstance(result, dict):
        fail("ответ не похож на квоту Codex: %s" % json.dumps(result)[:200])

    # Ответ несёт снимок дважды: одиночный rateLimits и карту по limit_id. Карта
    # точнее называет бакет подписки, поэтому приоритет у её ключа codex.
    by_id = result.get("rateLimitsByLimitId")
    if by_id is None:
        snap = result.get("rateLimits")
    elif not isinstance(by_id, dict) or "codex" not in by_id:
        fail("карта лимитов без ключа codex: %s" % json.dumps(by_id)[:200])
    else:
        snap = by_id["codex"]
    if not isinstance(snap, dict):
        fail("в ответе нет снимка лимита: %s" % json.dumps(result)[:200])

    rows = {}
    unclassified = []
    for place in ("primary", "secondary"):
        window = snap.get(place)
        if window is None:
            continue
        if not isinstance(window, dict) or not isinstance(window.get("usedPercent"), int):
            fail("окно %s без процента потраченного: %s" % (place, json.dumps(window)[:200]))
        length = window.get("windowDurationMins")
        name = WINDOWS.get(length) if isinstance(length, int) else None
        if length is None:
            # Протокол обновлений разреженный, и null здесь нетронутое поле, а не
            # ноль: подменять его нулём значило бы притянуть окно наугад.
            unclassified.append(place)
            continue
        if name is None:
            fail("у окна %s незнакомая длина %s мин, разбор отказан" % (place, length))
        if name in rows:
            fail("оба окна ответа длиной %s мин, окна перепутаны" % length)
        rows[name] = window
    if "week_all" not in rows:
        why = "в ответе нет обязательного недельного окна"
        if unclassified:
            why += " (окно %s приехало без длины и не классифицируется)" % " и ".join(unclassified)
        fail(why)

    # taken минутой, вслед строки окон: нетронутое окно без даты сброса это
    # законная строка формата (parseBucket в quota.go), процент выше потолка
    # подрезается.
    now = datetime.datetime.now().replace(second=0, microsecond=0)
    print("taken = " + now.strftime("%Y-%m-%dT%H:%M"))
    for name in ("window5h_all", "week_all"):
        window = rows.get(name)
        if window is None:
            continue
        line = "%s = %d%%" % (name, min(window["usedPercent"], 100))
        resets = window.get("resetsAt")
        if resets is not None:
            line += " сброс " + datetime.datetime.fromtimestamp(resets).strftime("%Y-%m-%dT%H:%M")
        print(line)
except SystemExit:
    raise
except Exception as e:
    sys.stderr.write("обмен с app-server сломался: %r\n" % e)
    sys.exit(1)
finally:
    kill()
PY
