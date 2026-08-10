package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Формат строки версии один на утилиты devkit: из неё берёт коммит doctor, и
// разъехавшийся формат ломал бы сверку молча. Утилита, забывшая version.go,
// до этого теста не доходит, она не собирается.
var versionLineRe = regexp.MustCompile(`^` + toolName + ` \S+ \(\S+\)\n$`)

func TestPrintVersionLine(t *testing.T) {
	var b bytes.Buffer
	if !printVersion([]string{"--version"}, &b) {
		t.Fatal("--version не разобран")
	}
	if !versionLineRe.MatchString(b.String()) {
		t.Fatalf("строка версии не по формату «<имя> <версия> (<коммит>)»: %q", b.String())
	}
}

// Голое «--» разбор останавливает: после него идёт чужая полезная нагрузка. У
// exec это команда подпроцесса, и --version внутри неё принадлежит ей, а не
// secretctl. Напечатать версию и выйти нулём значит соврать обвязке, что
// секрет подставился.
func TestPrintVersionStopsAtSeparator(t *testing.T) {
	var b bytes.Buffer
	if printVersion([]string{"exec", "TOKEN", "--", "env", "--version"}, &b) || b.Len() > 0 {
		t.Fatalf("--version из чужой команды после «--» принят за свой флаг: %q", b.String())
	}
	if !printVersion([]string{"--version"}, &b) {
		t.Fatal("свой --version не разобран")
	}
	if !versionLineRe.MatchString(b.String()) {
		t.Fatalf("строка версии не по формату: %q", b.String())
	}
}

func TestPrintVersionLeavesOtherArgs(t *testing.T) {
	var b bytes.Buffer
	if printVersion([]string{"--help"}, &b) || b.Len() > 0 {
		t.Fatalf("чужой аргумент принят за --version: %q", b.String())
	}
	if printVersion(nil, &b) || b.Len() > 0 {
		t.Fatalf("запуск без аргументов принят за --version: %q", b.String())
	}
}

// Тот же разбор на собранном бинаре: юнит на printVersion сам по себе не
// доказывает, что main зовёт его до разбора аргументов.
func TestVersionFlagOnTheBinary(t *testing.T) {
	bin := buildSecretctl(t)
	for _, args := range [][]string{{"--version"}} {
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if !versionLineRe.MatchString(string(out)) {
			t.Fatalf("%v: строка версии не по формату: %q", args, out)
		}
	}
	// Хвост после «--» это чужое дело: утилита обязана дойти до своей логики и
	// сказать, что там не так, а не напечатать версию и выйти нулём.
	args := []string{"exec", "NOPE", "--", "echo", "--version"}
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err == nil {
		t.Fatalf("%v: молчаливый ноль вместо разбора аргументов: %q", args, out)
	}
	if strings.Contains(string(out), toolName+" "+version+" ("+commit+")") {
		t.Fatalf("%v: напечатана версия вместо работы: %q", args, out)
	}
}

// buildSecretctl собирает бинарь во временную директорию. GOWORK=off: чужой
// go.work на машине увёл бы сборку из модуля утилиты, как у соседей.
func buildSecretctl(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), toolName)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка не прошла: %v\n%s", err, out)
	}
	return bin
}
