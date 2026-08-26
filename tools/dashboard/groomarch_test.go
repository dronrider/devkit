package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Разбор десяти черновиков оставляет десять мёртвых разговоров, и человек
// убирает их руками (замечание пользователя). Дашборд убирает их сам, но
// только по твёрдому следу на диске, не трогая ни ожидания человека, ни
// непрочитанного.

// groomChat заводит разговор груминга: первой репликой стоит заказ, каким его
// пишет сам дашборд.
func groomChat(t *testing.T, e *testEnv, sid, id string, said time.Time) {
	t.Helper()
	writeSession(t, e.home, e.proj, "", sid,
		saidLine(groomPrompt(id, ""), said)+
			fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text",`+
				`"text":"Завёл строку %s."}]},"timestamp":%q}`, id, said.Add(time.Minute).UTC().Format(time.RFC3339))+"\n",
		said.Add(time.Minute))
}

// writeDraft кладёт файл записи накопителя: живой файл значит, что разбор
// твёрдого следа не оставил.
func writeDraft(t *testing.T, projPath, id string) {
	t.Helper()
	path := filepath.Join(projPath, filepath.FromSlash(draftFileRel(id)))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# черновик "+id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func archived(t *testing.T, srv *server, sid string) bool {
	t.Helper()
	return srv.chatStoreRead(sid).Archived
}

// Твёрдый исход это след на диске: черновика нет, строка на доске стоит.
// Такой разговор уходит в архив сам, и уходит той же дорогой, что и рукой.
func TestGroomSweepArchivesFinished(t *testing.T) {
	e, _ := chatEnv(t)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	groomChat(t, e, sid, "XR-4", time.Now().Add(-2*time.Hour))
	// Человек разговор открывал: непрочитанного в нём нет. Черновика нет
	// вовсе, а строка XR-4 стоит на доске фикстуры.
	e.s.chatSeenMark(sid)
	e.s.groomSweep(e.proj)
	if !archived(t, e.s, sid) {
		t.Error("разговор с твёрдым исходом остался в списке: человеку опять убирать руками")
	}
}

// Незаконченный разбор не трогаем: файл записи лежит на месте, строки нет.
func TestGroomSweepKeepsUnfinished(t *testing.T) {
	e, _ := chatEnv(t)
	sid := "bbbb2222-2222-4222-8222-222222222222"
	groomChat(t, e, sid, "XR-77", time.Now().Add(-2*time.Hour))
	writeDraft(t, e.proj, "XR-77")
	e.s.groomSweep(e.proj)
	if archived(t, e.s, sid) {
		t.Error("разговор без твёрдого исхода убран: разбор ещё не кончился")
	}
}

// Агент стоит на вопросе к человеку: такой разговор не трогаем, даже когда
// след на диске есть. Живой пример пользователя это чаты груминга с вопросами.
func TestGroomSweepKeepsWaiting(t *testing.T) {
	e, _ := chatEnv(t)
	sid := "cccc3333-3333-4333-8333-333333333333"
	groomChat(t, e, sid, "XR-4", time.Now().Add(-2*time.Hour))
	writeAsk(t, e.proj, "XR-4", time.Now().Add(10*time.Minute), sid, "чинить копией или общим модулем?")
	e.s.groomSweep(e.proj)
	if archived(t, e.s, sid) {
		t.Error("разговор с вопросом к человеку убран в архив: вопрос уедет с экрана")
	}
}

// Ответ агента, которого человек не видел, уборку останавливает. Разговор,
// который ни разу не открывали, считается непрочитанным весь: возраст хвоста
// тут не мера, вопрос к человеку висит в чате и через сутки.
func TestGroomSweepKeepsUnread(t *testing.T) {
	e, _ := chatEnv(t)
	sid := "dddd4444-4444-4444-8444-444444444444"
	groomChat(t, e, sid, "XR-4", time.Now().Add(-2*time.Hour))
	e.s.groomSweep(e.proj)
	if archived(t, e.s, sid) {
		t.Error("разговор, который ни разу не открывали, убран в архив вместе с ответом")
	}

	// Человек открыл разговор: отметка показа встала, и непрочитанного больше
	// нет. Ставит её чтение ленты панелью.
	e.s.chatSeenMark(sid)
	e.s.groomSweep(e.proj)
	if !archived(t, e.s, sid) {
		t.Error("прочитанный разговор с твёрдым исходом всё равно остался в списке")
	}
}

// Ответ пришёл после показа: человек его не видел, и уборка ждёт.
func TestGroomSweepKeepsAnswerAfterSeen(t *testing.T) {
	e, _ := chatEnv(t)
	sid := "ffff6666-6666-4666-8666-666666666666"
	groomChat(t, e, sid, "XR-4", time.Now().Add(-2*time.Hour))
	e.s.chatSeenMark(sid)
	// Свежая запись поверх показа: агент сказал что-то после ухода человека.
	groomChat(t, e, sid, "XR-4", time.Now())
	e.s.groomSweep(e.proj)
	if archived(t, e.s, sid) {
		t.Error("разговор со свежим ответом поверх показа убран в архив")
	}
}

// Чужие разговоры сторож не трогает: заказ груминга пишет сам дашборд, и по
// нему разговор разбора отличается от разговора конвейера и от свободного чата.
func TestGroomSweepSkipsOtherChats(t *testing.T) {
	e, _ := chatEnv(t)
	sid := "eeee5555-5555-4555-8555-555555555555"
	// Свободный чат, начатый с номера задачи: человек так и пишет, и одного
	// номера в первой реплике для разбора мало.
	writeSession(t, e.home, e.proj, "", sid,
		saidLine("XR-4 почему тут ранг такой", time.Now().Add(-2*time.Hour)), time.Now().Add(-2*time.Hour))
	e.s.groomSweep(e.proj)
	if archived(t, e.s, sid) {
		t.Error("свободный разговор убран сторожем груминга: обкатка идёт только на разборе")
	}
	for _, said := range []string{"XR-4 почему тут ранг такой", "Выполни XR-4", "Продолжай выполнение XR-4"} {
		if got := groomChatID(chatEntry{First: said}); got != "" {
			t.Errorf("реплика %q сочтена заказом груминга: %q", said, got)
		}
	}
	if got := groomChatID(chatEntry{First: groomPrompt("XR-9", "")}); got != "XR-9" {
		t.Errorf("заказ груминга не узнан: %q", got)
	}
}
