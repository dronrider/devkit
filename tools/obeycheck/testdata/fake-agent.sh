#!/bin/sh
# Подложная модель стенда: вместо claude -p делает ровно то, что предписано
# файлом behaviour из раскладки. Промпт приходит на stdin, напечатанное уезжает
# в транскрипт, работа оседает файлами в проекте, как у настоящего прогона.
prompt=$(cat)
echo "промпт: $prompt"
echo "HOME=$HOME"
echo "pwd=$(pwd)"
echo "раскладка=$OBEY_LAYOUT сценарий=$OBEY_SCENARIO повтор=$OBEY_REPEAT"
{
	echo "HOME=$HOME"
	echo "pwd=$(pwd)"
	echo "origin=$OBEY_ORIGIN"
	echo "seed=$OBEY_SEED"
	echo "hooks=$(git config core.hooksPath)"
	echo "промпт: $prompt"
} > env.txt

behaviour=$(cat behaviour 2>/dev/null || echo idle)
case "$behaviour" in
green) : > done.txt ;;
flaky) [ "${OBEY_REPEAT:-1}" -le 2 ] && : > done.txt ;;
slow) sleep 30 ;;
angry)
	: > done.txt
	echo "прогон кончился ошибкой, работа при этом сделана" >&2
	exit 3
	;;
idle) ;;
*)
	echo "неизвестное поведение $behaviour" >&2
	exit 9
	;;
esac
exit 0
