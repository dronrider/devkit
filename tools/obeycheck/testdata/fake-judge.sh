#!/bin/sh
# Подложный судья стенда: вместо claude -p отвечает по слову-примете. Режим
# первым аргументом, примета вторым. Каждый вызов дописывается в calls.log
# рабочей директории: по нему тесты видят, что судья слепой и когда его звали.
mode=${1:-word}
word=$2
prompt=$(cat)
{
	echo "HOME=$HOME"
	echo "pwd=$(pwd)"
	echo "harness=$(env | grep -E '^(OBEY_|CLAUDE)' | tr '\n' ' ')"
	echo "промпт: $prompt"
	echo "---"
} >>calls.log
calls=$(grep -c '^---$' calls.log)
# Режимы с хвостом -late отвечают по примете на калибровке (два примера) и
# срываются на клетках: vague-late отвечает не по форме, dead-late отваливается.
# Так проверяется, что ответ не по форме это красная клетка, а отвал посреди
# прогона останавливает стенд, как и на калибровке.
case "$mode" in
*-late)
	if [ "$calls" -le 2 ]; then mode=word; else mode=${mode%-late}; fi
	;;
esac
case "$mode" in
dead)
	echo "Not logged in" >&2
	exit 1
	;;
empty) exit 0 ;;
slow) sleep 30 ;;
vague)
	echo "цитата: «$(printf '%s' "$prompt" | tail -1)»"
	# Последняя строка длиннее потолка oneLine: примечание клетки обязано
	# её обрезать, а не везти абзац в таблицу и в файл задачи.
	long="скорее да, чем нет, хотя место спорное"
	for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do long="$long, и снова спорное место"; done
	echo "$long"
	;;
word)
	text=${prompt#*Текст:}
	if printf '%s' "$text" | grep -qF -e "$word"; then
		echo "цитата: «$(printf '%s' "$text" | grep -F -e "$word" | head -1 | sed 's/^ *//')»"
		echo "да"
	else
		echo "слова «$word» в тексте нет"
		echo "нет"
	fi
	;;
*)
	echo "неизвестный режим $mode" >&2
	exit 9
	;;
esac
exit 0
