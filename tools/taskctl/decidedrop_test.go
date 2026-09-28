package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
)

// Ответ человека снимает признак ожидания того же захода. До DK-1204 признак
// снимали только панель и wake, а decide --answer из окна tmux и решение
// «--by человек» головой сессии оставляли файл на месте: плашка «Заход ждёт
// ответа» стояла на решённой развилке до перезапуска захода. Спрашивают из
// дерева задачи, отвечают в основном чекауте, поэтому признак кладётся в оба
// места, и снятие обязано дойти до обоих.

// askInBothTrees кладёт признак ожидания задачи в чекаут и в дерево задачи
// рядом с ним и отдаёт оба пути.
func askInBothTrees(t *testing.T, root, id string) []string {
	t.Helper()
	tree := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+strings.ToLower(id))
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	ask := chat.Ask{Session: "sess-1", Task: id, Questions: []chat.Question{{Text: "куда катить"}}}
	var paths []string
	for _, dir := range []string{root, tree} {
		if err := chat.WriteAsk(dir, chat.TaskName(id), ask); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, chat.AskPath(dir, chat.TaskName(id)))
	}
	return paths
}

// askDroppedWord это слово ответа команды о снятом признаке. Строка стоит
// литералом, а не константой кода: regcheck собирает тест на старой базе, где
// константы ещё нет.
const askDroppedWord = "признак ожидания снят"

func askFilesLeft(paths []string) []string {
	var left []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			left = append(left, p)
		}
	}
	return left
}

func TestDecideAnswerDropsAsk(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "в прод", "куда катить?", "в стенд", "в прод"},
	})
	paths := askInBothTrees(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "выкат 2"}, nil, nil)
	if left := askFilesLeft(paths); len(left) != 0 {
		t.Fatalf("ответ человека оставил признак ожидания: %q", left)
	}
	if !strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды не назвал снятие признака:\n%s", got)
	}
}

func TestDecideCloseByHumanDropsAsk(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "в прод", "куда катить?", "в стенд", "в прод"},
	})
	paths := askInBothTrees(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Name: "выкат", By: "человек", Text: "в стенд, там дешевле"}, nil, nil)
	if left := askFilesLeft(paths); len(left) != 0 {
		t.Fatalf("решение человека оставило признак ожидания: %q", left)
	}
	if !strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды не назвал снятие признака:\n%s", got)
	}
}

// Решение исполнителя это не ответ человека: вопрос к человеку, если он стоит,
// остаётся ждать, и признак снимать нечем.
func TestDecideCloseByExecutorKeepsAsk(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "в прод", "куда катить?", "в стенд", "в прод"},
	})
	paths := askInBothTrees(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Name: "выкат", By: "исполнитель", Text: "в прод, как рекомендовано"}, nil, nil)
	if left := askFilesLeft(paths); len(left) != 2 {
		t.Fatalf("решение исполнителя сняло признак ожидания человека: осталось %q", left)
	}
	if strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды назвал снятие, которого не было:\n%s", got)
	}
}

// Без признака ответ молчит о снятии: слово «снят» без файла было бы выдумкой.
func TestDecideAnswerWithoutAskSaysNothing(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "в прод", "куда катить?", "в стенд", "в прод"},
	})
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "выкат 2"}, nil, nil)
	if strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды назвал снятие признака, которого не было:\n%s", got)
	}
}
