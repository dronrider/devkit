package main

import (
	"bytes"
	"regexp"
	"testing"
)

// Формат строки версии один на шесть утилит devkit: из неё берёт коммит doctor,
// и разъехавшийся формат ломал бы сверку молча. Утилита, забывшая version.go,
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

func TestPrintVersionLeavesOtherArgs(t *testing.T) {
	var b bytes.Buffer
	if printVersion([]string{"--help"}, &b) || b.Len() > 0 {
		t.Fatalf("чужой аргумент принят за --version: %q", b.String())
	}
	if printVersion(nil, &b) || b.Len() > 0 {
		t.Fatalf("запуск без аргументов принят за --version: %q", b.String())
	}
}
