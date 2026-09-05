package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Сторож остаётся сторожем только пока его зовут: сквозной прогон гоняется
// тестом на каждой правке дашборда, а не ждёт, пока о нём вспомнят руками.
// Живого тут ничего нет: прогон поднимает своё окружение сам и убирает за
// собой, а домом и PATH процесса распоряжается на своё время.
func TestSmokePassesTheChain(t *testing.T) {
	var out bytes.Buffer
	if err := cmdSmoke(&out, false); err != nil {
		t.Fatalf("сквозной прогон не прошёл: %v\n%s", err, out.String())
	}
	text := out.String()
	if !strings.HasSuffix(strings.TrimSpace(text), smokeOK) {
		t.Fatalf("прогон не сказал %q:\n%s", smokeOK, text)
	}
	// Каждый пункт агентской части DoD виден строкой хода: молчаливое «ok» не
	// отличить от прогона, который половину шагов пропустил.
	for _, want := range []string{
		"доска со статусами и работами",
		"запуск работы",
		"сообщение цели",
		"ответ задаче безадресной строкой",
		"ожидание человека видно строкой доски и полкой ждущих",
		"доставка реплики витку",
		"подхват сообщения витком",
		"стоп работы",
		"реплика стоп-хука стоит служебкой с подписью",
		"уведомление о фоновой работе без портянки дисклеймера",
		"уведомление о стопе в ленте",
		"реплика и уборка при занятом окне",
		"пересчёт ранга перетаскиванием доехал до доски",
		"смерть поднятой сессии названа исходом",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в ходе прогона нет шага %q:\n%s", want, text)
		}
	}
}

// Прогон живой машины не задевает: свой дом, свой корень проектов и свои
// фикстуры вместо чужих программ, а после себя он не оставляет ни временного
// каталога, ни перебитых переменных окружения.
func TestSmokeLeavesNothingBehind(t *testing.T) {
	home, path := os.Getenv("HOME"), os.Getenv("PATH")
	// Каталоги прежних прогонов с ключом --keep тут ни при чём: считаются
	// только те, что появились за этот.
	before := smokeDirs(t)
	var out bytes.Buffer
	if err := cmdSmoke(&out, false); err != nil {
		t.Fatalf("сквозной прогон не прошёл: %v\n%s", err, out.String())
	}
	if os.Getenv("HOME") != home || os.Getenv("PATH") != path {
		t.Fatalf("прогон оставил за собой окружение: HOME=%q PATH=%q", os.Getenv("HOME"), os.Getenv("PATH"))
	}
	for dir := range smokeDirs(t) {
		if !before[dir] {
			t.Errorf("временное окружение прогона осталось на диске: %s", dir)
		}
	}
}

func smokeDirs(t *testing.T) map[string]bool {
	t.Helper()
	base, err := smokeBase()
	if err != nil {
		t.Fatal(err)
	}
	found, err := filepath.Glob(filepath.Join(base, "run-*"))
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, d := range found {
		set[d] = true
	}
	return set
}
