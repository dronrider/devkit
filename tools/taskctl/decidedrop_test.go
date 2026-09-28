package main

import (
	"os"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
)

// Ответ человека снимает признак ожидания того же захода. До DK-1204 признак
// снимали только панель и wake, а decide --answer из окна tmux и решение
// «--by человек» головой сессии оставляли файл на месте: плашка «Заход ждёт
// ответа» стояла на решённой развилке до перезапуска захода. Признак всегда
// лежит в основном чекауте (runAsk пишет его через stage.MainRoot), а
// отвечают и оттуда, и из дерева задачи, поэтому снятие обязано дойти до
// основного чекаута с любого корня.

// askInMain кладёт признак ожидания задачи в основной чекаут и отдаёт путь.
func askInMain(t *testing.T, root, id string) string {
	t.Helper()
	ask := chat.Ask{Session: "sess-1", Task: id, Questions: []chat.Question{{Text: "куда катить"}}}
	if err := chat.WriteAsk(root, chat.TaskName(id), ask); err != nil {
		t.Fatal(err)
	}
	return chat.AskPath(root, chat.TaskName(id))
}

// askDroppedWord это слово ответа команды о снятом признаке. Строка стоит
// литералом, а не константой кода: regcheck собирает тест на старой базе, где
// константы ещё нет.
const askDroppedWord = "признак ожидания снят"

func askStands(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

var dropForks = [][]string{
	{"выкат", "в прод", "куда катить?", "в стенд", "в прод"},
}

func TestDecideAnswerDropsAsk(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", dropForks)
	path := askInMain(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "выкат 2"}, nil, nil)
	if askStands(path) {
		t.Fatalf("ответ человека оставил признак ожидания: %s", path)
	}
	if !strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды не назвал снятие признака:\n%s", got)
	}
}

func TestDecideCloseByHumanDropsAsk(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", dropForks)
	path := askInMain(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Name: "выкат", By: "человек", Text: "в стенд, там дешевле"}, nil, nil)
	if askStands(path) {
		t.Fatalf("решение человека оставило признак ожидания: %s", path)
	}
	if !strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды не назвал снятие признака:\n%s", got)
	}
}

// Решение исполнителя это не ответ человека: вопрос к человеку, если он стоит,
// остаётся ждать, и признак снимать нечем.
func TestDecideCloseByExecutorKeepsAsk(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", dropForks)
	path := askInMain(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Name: "выкат", By: "исполнитель", Text: "в прод, как рекомендовано"}, nil, nil)
	if !askStands(path) {
		t.Fatalf("решение исполнителя сняло признак ожидания человека: %s", path)
	}
	if strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды назвал снятие, которого не было:\n%s", got)
	}
}

// Без признака ответ молчит о снятии: слово «снят» без файла было бы выдумкой.
func TestDecideAnswerWithoutAskSaysNothing(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", dropForks)
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "выкат 2"}, nil, nil)
	if strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды назвал снятие признака, которого не было:\n%s", got)
	}
}

// Передача развилки исполнителю с --by человек это не ответ на вопрос:
// развилка остаётся открытой у исполнителя, и признак стоит дальше.
func TestDecideLeaveByHumanKeepsAsk(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", dropForks)
	path := askInMain(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Name: "выкат", Leave: true, By: "человек", Text: "пусть решает исполнитель"}, nil, nil)
	if !askStands(path) {
		t.Fatalf("передача развилки сняла признак ожидания: %s", path)
	}
	if strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды назвал снятие, которого не было:\n%s", got)
	}
}

// Боевая раскладка: признак лежит в основном чекауте, а отвечает исполнитель
// из дерева задачи (taskctl -C <worktree> decide --answer). Корень ответа
// приводится к основному чекауту, иначе признак искался бы в каталоге,
// которого в дереве задачи не бывает.
func TestDecideAnswerFromWorktreeDropsMainAsk(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", dropForks)
	gitSetup(t, root)
	wt := addWorktree(t, root, "xr-005")
	path := askInMain(t, root, "XR-005")
	got := chatRun(t, wt, DecideParams{ID: "XR-005", Answer: "выкат 2"}, nil, nil)
	if askStands(path) {
		t.Fatalf("ответ из дерева задачи оставил признак в основном чекауте: %s", path)
	}
	if !strings.Contains(got, askDroppedWord) {
		t.Fatalf("ответ команды не назвал снятие признака:\n%s", got)
	}
}
