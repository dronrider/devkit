package main

import (
	"strings"
	"testing"
)

// Секция [head] (DK-931) знакома своду профиля: её ключи читаются без
// предупреждения, а незнакомый ключ в ней остаётся предупреждением.
func TestProfileHeadSectionIsKnown(t *testing.T) {
	base := "[detect]\n\n[rules]\nmode = \"import\"\nfile = \"X.md\"\nimport_line = \"@{path}\"\n\n" +
		"[delegate]\nmode = \"none\"\n\n[hooks]\n\n[quota]\n\n"
	got := profileReport("x.toml", base+"[head]\nclient = [\"x\", \"--y\"]\nbin = \"x\"\n"+
		"model = [\"--model\", \"{model}\"]\nsession = [\"--session-id\", \"{session}\"]\n"+
		"resume = [\"x\", \"--resume\", \"{session}\"]\nturn_end = \"exit\"\n", true)
	if strings.Contains(got, "warn:") || strings.Contains(got, "error:") {
		t.Fatalf("секция [head] читается с отказом или предупреждением:\n%s", got)
	}
	if !strings.Contains(got, "[head]\nclient = [\"x\", \"--y\"]") {
		t.Fatalf("секции [head] нет в разборе:\n%s", got)
	}
	got = profileReport("x.toml", base+"[head]\nclient = \"x\"\n", true)
	if !strings.Contains(got, "error:") {
		t.Fatalf("client строкой вместо массива принят:\n%s", got)
	}
}
