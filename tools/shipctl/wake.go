package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Обход ждущих после слияния (DK-932). Строка, припаркованная причиной
// «слияние: <ID>», ждёт, когда работа предпосылки ляжет в main. Событие
// случается тут, и merge в конце зовёт `taskctl wake`: правило совпадения,
// перевод строки и подъём головы живут в taskctl одним местом, тот же хвост
// зовут close и тик сторожка. Своей копии этих правил у shipctl нет, как нет
// её у подъёма прогона сценария (checkrun.go).

// wakeTimeout это предел обхода. Подъём ждёт передачи замка оболочке по
// полминуты на строку, и висеть дольше ему не на чем. Слияние к этому моменту
// необратимо, и держать отчёт заложником подъёма нельзя: пропущенное доберёт
// тик.
const wakeTimeout = 3 * time.Minute

// wakeNote обходит ждущих и возвращает строки для отчёта merge. Пусто, когда
// будить некого. Провал обхода слияние не роняет: слова идут в отчёт, а строку
// следующим заходом поднимет тик сторожка.
func wakeNote(root string, push bool) string {
	bin, err := exec.LookPath("taskctl")
	if err != nil {
		return "ждущих слияния не обошёл: taskctl не нашёлся в PATH, их поднимет тик сторожка либо `taskctl wake`"
	}
	args := []string{"-C", root, "wake", "--quiet"}
	if push {
		args = append(args, "--push")
	}
	ctx, cancel := context.WithTimeout(context.Background(), wakeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if ctx.Err() != nil {
		return fmt.Sprintf("обход ждущих сорвал срок в %s: пропущенное доберёт тик сторожка", wakeTimeout)
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "обход ждущих оставил строки стоять, их повторит тик сторожка: " + text
	}
	return text
}
