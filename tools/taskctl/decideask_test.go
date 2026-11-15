package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
)

// TestDecideAskNeedsHint: вопрос без рекомендации отказывает (DK-974). Первая
// голова DK-941 спросила тремя вариантами без рекомендации, верного среди
// них не было, и цель простояла ночь. Отказ называет обход и ключ --tie.
func TestDecideAskNeedsHint(t *testing.T) {
	root := setup(t)
	_, err := cmdDecide(root, DecideParams{ID: "XR-005", Ask: "разрез", Text: "куда вынести раздел?", Opts: []string{"свой скилл", "файл рядом"}, Now: decideDay})
	if err == nil {
		t.Fatal("вопрос без рекомендации завёлся")
	}
	for _, w := range []string{"рекомендации", "--hint", "--tie", "Обход"} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("отказ не назвал %q: %v", w, err)
		}
	}
	if strings.Contains(readTask(t, root, "XR-005"), "«разрез»") {
		t.Fatal("отказ оставил развилку в файле")
	}
}

// TestDecideAskDraftCarrier: развилка черновика из панельной сессии получает
// признак ожидания с адресатом (DK-1679). Прежде признак не писался, у вопроса
// не было адресата, и ответ панели уезжал во вход задачи безадресной строкой,
// а сессия стояла на вопросе. Живой признак носитель не перезаписывает: вопрос
// согласования (DK-1469) снимается своим ответом. Вне панели признака нет:
// живой терминал читает вопрос прямо из ленты.
func TestDecideAskDraftCarrier(t *testing.T) {
	root := setup(t)
	drafts := filepath.Join(root, "docs", "tasks", "drafts")
	if err := os.MkdirAll(drafts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drafts, "XR-006.md"), []byte("# XR-006: мысль\n\n## Что происходит\n\nПример.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := func(name string) string {
		switch name {
		case "DEVKIT_TMUX":
			return "chat-x"
		case "CLAUDE_CODE_SESSION_ID":
			return "sid-carrier"
		}
		return ""
	}
	out, err := runDecide(root, DecideParams{ID: "XR-006", Ask: "судьба мысли", Text: "что делать с мыслью?", Hint: "оформить сейчас, боль живая", Opts: []string{"оставить в накопителе"}, Now: decideDay}, nil, env)
	if err != nil {
		t.Fatal(err)
	}
	ask, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-006")))
	if !ok {
		t.Fatal("развилка черновика не получила признак ожидания")
	}
	if ask.Session != "sid-carrier" || ask.Task != "XR-006" || len(ask.Questions) != 1 {
		t.Fatalf("признак без адресата: %+v", ask)
	}
	if !strings.Contains(ask.Questions[0].Text, "«судьба мысли»") || len(ask.Questions[0].Options) != 2 || !ask.Questions[0].Options[0].Recommended {
		t.Fatalf("вопрос признака неполон: %+v", ask.Questions[0])
	}
	if !strings.Contains(out, "признаком ожидания") {
		t.Fatalf("вывод молчит о носителе: %s", out)
	}
	askPath := chat.AskPath(root, chat.TaskName("XR-006"))
	before := readFileT(t, askPath)
	if _, err = runDecide(root, DecideParams{ID: "XR-006", Ask: "вторая мысль", Text: "спросить ещё раз?", Hint: "не спрашивать, развилка лишняя", Opts: []string{"спросить"}, Now: decideDay}, nil, env); err != nil {
		t.Fatal(err)
	}
	if readFileT(t, askPath) != before {
		t.Fatal("живой признак перезаписан новой развилкой")
	}
	plain, err := runDecide(root, DecideParams{ID: "XR-006", Ask: "терминал", Text: "вопрос живым терминалом?", Hint: "ответить самому", Opts: []string{"подумать"}, Now: decideDay}, nil, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "признаком ожидания") {
		t.Fatalf("вне панели сеанс получил признак: %s", plain)
	}
}
