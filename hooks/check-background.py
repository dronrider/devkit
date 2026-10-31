#!/usr/bin/env python3
"""Рубеж синхронности (DK-678): PreToolUse-хук отбивает фоновый запуск там, где
фон не доживает до отчёта.

Сессия без окна (`claude -p`, ею дашборд поднимает работу с экрана) кончает ход
финальным текстом, и харнес добивает недождавшихся фоновых детей примерно через
десять минут после него (замер DK-136, три прогона дали десять минут и одну
секунду). Всё, что уехало в фон, гибнет на середине: работа остаётся
незакоммиченным диффом в дереве задачи, задача висит в работе, а провал выходит
тихим, без ошибки и без строки в журнале. Так за один день 31 августа встали
три сессии конвейера, DK-661 на фоновом `shipctl merge`, DK-652 на асинхронном
субагенте и XR-285 на headless `agentctl run`. В живом окне фон законен, там
ребёнок переживает конец хода и будит сессию уведомлением, и такой вызов рубеж
пропускает молча.

Признак фона лежит во входе инструмента: и Bash, и делегирование зовутся с
`run_in_background: true`, и на PreToolUse это видно до запуска, поэтому отказ
жёсткий, а не находка вдогонку. Синхронные вызовы рубеж не трогает вовсе, в том
числе несколько разом одним сообщением: параллельность пачки держится ими.

У двух инструментов разный ответ на пропущенное поле, и разводит их имя
инструмента. У Bash пропуск это синхронный запуск. У делегирования пропуск это
фон: харнес поднимает субагента асинхронно по умолчанию, вызов без поля
возвращает `status = "async_launched"` (замер на ревью DK-678, три прогона
подряд и снятый живьём образец `pre-tool-use-agent-plain.json`). Синхронное
делегирование в headless поэтому зовётся с `run_in_background: false` явно, чего
скиллы `board-batch` и `board-ship` и требуют.

Вторая дыра закрывалась тем же рубежом (DK-722). Синхронный вызов долгой
команды конвейера поля фона не ставит, а харнес сам уводит его в фон по своим
120 секундам, и вместе с выходом головы команда гибнет: шесть проходов DK-720
умерли на `shipctl merge`, в файлах вывода только пометка `[killed]`. Поэтому
в headless-сессии рубеж отбивает и голый вызов долгой команды из списка ниже,
а отказ называет переживаемый шаг `cmdout --detach`. Список это команды,
минутные по природе: слияние, выкат и откат с полным прогоном тестов, обкатка,
стенд и двойной прогон регрессии. Голый `go test` в списке нет, его уже держит
рубеж прямого прогона (check-bare-test.py, DK-1219), и его замена
`devkitctl test` остаётся законным синхронным вызовом ниже потолка Bash.

Вида сессии в событии хука нет, и считается он по окружению клиента, которое
хук наследует:

  DEVKIT_HEADLESS
      сессию поднял печатным режимом сам devkit, и он про это сказал. Признак
      этот первый, потому что за него отвечает подъёмщик, а не клиент: метку
      ставит команда сессии (у дашборда это headlessEnv), и та же команда
      снимает унаследованную метку вместе с прочим чужим окружением. Живому окну
      с экрана она поэтому не достаётся, а печатной сессии достаётся при любом
      наследстве tmux. Метку, пришедшую мимо подъёмщика devkit, рубеж принимает
      на веру: цена вранья тут лишний синхронный ход, а не потерянная работа.
  CLAUDE_CODE_ENTRYPOINT со значением sdk-cli, sdk-ts или sdk-py
      этими значениями клиент метит печатный режим и сессии SDK сам. `claude -p`
      ставит sdk-cli, когда переменной нет вовсе или в ней стоит cli (разбор
      бинаря 2.1.252, там же снят живой замер), живому окну достаётся cli или
      claude-vscode.
  DEVKIT_RUN_DEPTH
      сессию подняла команда `agentctl run`, а она зовёт клиента печатным
      режимом всегда. Переменная нужна отдельно от первой: окружение уезжает в
      подпроцесс целиком, и headless-ребёнок живого окна донесёт до себя
      claude-vscode родителя.

Одной переменной клиента мало, и это не осторожность, а замер DK-691. Окно
конвейера поднимается в tmux, а tmux-сервер переживает ту сессию, из которой
его завели однажды, и раздаёт её окружение всем новым окнам: снятый замер дал
новому окну CLAUDE_CODE_ENTRYPOINT=claude-vscode, CLAUDECODE=1 и
CLAUDE_CODE_SESSION_ID чужой сессии. Своего значения клиент поверх такого
наследства не ставит, `claude -p` ставит sdk-cli только там, где переменной нет
или в ней стоит cli. Рубеж считал конвейер живым окном и пропускал фон, а фон
гиб вместе с головой: так 01.09 встали обе конвейерные сессии, DK-655 на
субагенте и DK-658 на фоновом прогоне.

Режимы:
  check-background.py --hook [протокол]
                          хук на PreToolUse: JSON события на stdin, смотрится
                          tool_input.run_in_background. Разбор входа и канал
                          ответа по имени протокола из hookio.py, голый --hook
                          это claude-code, ответ exit 2
  check-background.py --why
                          чем рубеж считает эту сессию и что из этого следует
"""
import os
import re
import sys

import hookio

ENTRYPOINT = "CLAUDE_CODE_ENTRYPOINT"
# Значения, которыми клиент метит печатный режим и сессии SDK. Живое окно
# ставит cli или claude-vscode, и в этот список они не попадают.
SDK_ENTRYPOINTS = ("sdk-cli", "sdk-ts", "sdk-py")
RUN_DEPTH = "DEVKIT_RUN_DEPTH"
# Метка подъёмщика: её ставит тот, кто поднимает сессию печатным режимом, и
# значением называет себя («дашборд»), чтобы отказ говорил, откуда сессия.
HEADLESS_MARK = "DEVKIT_HEADLESS"
BACKGROUND = "run_in_background"
# Долгие команды конвейера (DK-722): минуты по природе, и в headless они
# доезжают до конца только переживающим шагом. Пары слов, а не подстроки:
# короткие подкоманды тех же утилит (shipctl status) остаются синхронными.
# Список живёт тут же, где и отказ о нём, и пополняется правкой одного модуля.
LONG_STEPS = (("shipctl", "merge"), ("shipctl", "ship"), ("shipctl", "revert"),
              ("taskctl", "rehearse"), ("obeycheck",), ("regcheck",))
# Слово переживаемого шага: `cmdout --detach` снимает отказ со своего сегмента.
DETACH_MARK = "--detach"


def headless(env):
    """Чем сессия опознана как headless, либо пустая строка у живой. Признак
    возвращается строкой, а не флагом: отказ называет его человеку, иначе
    спорить с рубежом нечем и чинить ложное срабатывание не с чего."""
    mark = env.get(HEADLESS_MARK, "").strip()
    if mark:
        return "%s=%s, сессию поднял печатным режимом сам devkit" % (HEADLESS_MARK, mark)
    point = env.get(ENTRYPOINT, "")
    if point in SDK_ENTRYPOINTS:
        return "%s=%s" % (ENTRYPOINT, point)
    depth = env.get(RUN_DEPTH, "")
    if depth:
        return "%s=%s, сессию подняла команда agentctl run" % (RUN_DEPTH, depth)
    return ""


def wants_background(tool, tool_input):
    """Фоновый ли это запуск. Ложь в поле означает синхронный вызов у любого
    инструмента, строка сравнивается со словом на случай, если харнес однажды
    положит в поле «true» текстом. Пропущенное поле разбирается по имени
    инструмента: у Bash это синхронный запуск, у делегирования фоновый, потому
    что таков дефолт харнеса."""
    value = tool_input.get(BACKGROUND)
    if value is None:
        return tool == hookio.AGENT_TOOL
    if isinstance(value, str):
        return value.strip().lower() == "true"
    return bool(value)


def called_with(tool_input):
    """Как вызов выглядел во входе. Отказ называет это первой строкой, и
    пропущенное поле отличается от проставленного."""
    if tool_input.get(BACKGROUND) is None:
        return "без поля %s (такой вызов харнес поднимает фоном)" % BACKGROUND
    return "с %s: true" % BACKGROUND


def bare_long_step(command):
    """Голый ли вызов долгой команды конвейера, и какой. Команда режется на
    сегменты по переводам строк и операторам && ; | и $(, и пара ловится
    только в позиции команды сегмента: первые слова после начала строки,
    оператора или присваиваний окружения. Без этого отбит безобидный текст,
    `echo план: shipctl merge` и heredoc с той же строкой. Слово переживаемого
    шага снимает отказ с одного сегмента: соседний сегмент составной команды
    гибнет той же смертью и проверяется как обычно."""
    for line in command.split("\n"):
        for part in split_ops(line):
            step = step_of(part)
            if step:
                return step
    return ""


def split_ops(text):
    parts = [text]
    for sep in ("&&", ";", "|", "$("):
        parts = [piece for part in parts for piece in part.split(sep)]
    return parts


ASSIGN_RE = re.compile(r"^[A-Za-z_]\w*=")


def step_of(part):
    """Голый ли долгий шаг в одном сегменте. Позиция команды стоит после
    присваиваний окружения (`GOWORK=off regcheck ...`), а у обёртки cmdout
    после её имени и ключей: синхронный `cmdout shipctl merge` гибнет той же
    смертью, и отказ про него не меняется."""
    words = part.split()
    if DETACH_MARK in words:
        return ""
    i = 0
    while i < len(words) and ASSIGN_RE.match(words[i]):
        i += 1
    if i < len(words) and words[i] == "cmdout":
        i += 1
        while i < len(words) and words[i].startswith("-"):
            i += 1
    for step in LONG_STEPS:
        if words[i:i + len(step)] == list(step):
            return " ".join(step)
    return ""


def advice(tool):
    if tool == hookio.AGENT_TOOL:
        return ("Работай синхронно. Субагента зови с `%s: false` явно: вызов "
                "без поля харнес запускает асинхронно." % BACKGROUND)
    return ("Работай синхронно. Bash зови без %s и с timeout по размеру прогона "
            "(потолок харнеса 600000 мс, прогон длиннее дробится на куски), "
            "субагента с `%s: false`." % (BACKGROUND, BACKGROUND))


def long_report(step, sign):
    return "\n".join([
        "долгий шаг отбит рубежом синхронности DK-722: «%s» зван без переживаемого шага," % step,
        "а сессия эта headless (%s)." % sign,
        "Ход такой сессии кончается выходом процесса, и команда гибнет по потолку Bash молча.",
        "Зови переживающим шагом: cmdout --detach %s." % step,
        "Обёртка вернёт ход сразу, вывод будет копиться файлом .devkit/cmdout, код возврата ляжет в exit,",
        "а конвейер перед следующим проходом дождётся конца шага.",
        "Команды короче потолка Bash (600000 мс с явным timeout) рубеж не трогает.",
    ]) + "\n"


def report(tool, sign, called):
    return "\n".join([
        "фоновый запуск отбит рубежом синхронности DK-678: инструмент %s зван "
        "%s," % (tool, called),
        "а сессия эта headless (%s)." % sign,
        "Ход такой сессии кончается финальным текстом, и харнес добивает "
        "недождавшихся фоновых детей примерно через десять минут после него: "
        "отчёта не увидит никто, а провал выйдет тихим.",
        advice(tool),
        "Параллельность рубеж не трогает: синхронные вызовы, посланные одним "
        "сообщением, идут разом.",
    ]) + "\n"


def run_hook(protocol):
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    ti = event.get("tool_input")
    if not isinstance(ti, dict):
        return 0
    tool = hookio.text_of(event.get("tool_name")) or "без имени"
    sign = headless(os.environ)
    if wants_background(tool, ti):
        if sign:
            return hookio.reply(protocol).found(report(tool, sign, called_with(ti)))
        return 0
    # Долгий шаг без поля фона гибнет той же смертью, и ловится отдельным
    # списком, только в Bash и только в headless (DK-722).
    if tool == hookio.BASH_TOOL and sign:
        step = bare_long_step(hookio.text_of(ti.get("command")))
        if step:
            return hookio.reply(protocol).found(long_report(step, sign))
    return 0


def run_why(env, out):
    """Вид сессии словами. Рубеж молчит, когда пропускает, и человеку иначе
    нечем отличить живое окно от headless без фонового вызова наугад."""
    sign = headless(env)
    if sign:
        out.write("сессия headless (%s): фоновый запуск и голый долгий шаг "
                  "конвейера отбиваются, долгий шаг зовётся cmdout --detach\n" % sign)
        return 0
    out.write("сессия живая (%s=%s): фоновый запуск проходит, ребёнок "
              "переживает конец хода\n" % (ENTRYPOINT, env.get(ENTRYPOINT) or "пусто"))
    return 0


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-background: %s\n" % e)
            return 2
    if argv[:1] == ["--why"]:
        return run_why(os.environ, sys.stdout)
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
