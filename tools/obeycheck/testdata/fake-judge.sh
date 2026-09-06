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
	echo "obey=$(env | grep '^OBEY_' | tr '\n' ' ')"
	echo "промпт: $prompt"
	echo "---"
} >>calls.log
calls=$(grep -c '^---$' calls.log)
# vague-late отвечает по примете на калибровке (два примера) и не по форме
# на клетках: так проверяется, что ответ не по форме это красная клетка, а
# не остановка.
if [ "$mode" = vague-late ]; then
	if [ "$calls" -le 2 ]; then mode=word; else mode=vague; fi
fi
case "$mode" in
dead)
	echo "Not logged in" >&2
	exit 1
	;;
empty) exit 0 ;;
slow) sleep 30 ;;
vague)
	echo "цитата: «$(printf '%s' "$prompt" | tail -1)»"
	echo "скорее да, чем нет"
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
