package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/mainchat"
	"github.com/dronrider/devkit/internal/sessions"
)

// Таблица тестов классификатора миграции по перечню LLD DK-1422, решение 7:
// четырнадцать строк ниже это те же строки таблицы документа, чистые тесты
// гоняют классификатор над фикстурой migSource без всякого IO, а строка
// таблицы, которой нужен журнал либо ручка, проверяется на стенде с временным
// домом.

// migFix собирает снимок источников с сессиями и настройкой на месте: ленты
// проставляет всем названным сессиям, если тест не решил иначе.
func migFix(t *testing.T, sids map[string][]sessionBind, extra func(*migSource)) migSource {
	t.Helper()
	src := migSource{
		Sessions: sids, Store: map[string]chatStore{}, Inbox: map[string]bool{},
		HeadLock: map[string]bool{}, Runs: map[string]bool{},
		GoalTurns: map[string][]string{}, Born: map[string]string{},
		Drafts: map[string]bool{}, Feeds: map[string]string{},
		Busy: map[string]bool{}, Heads: map[string]sessionHead{},
	}
	for sid := range sids {
		src.Feeds[sid] = migFeedLive
		src.Heads[sid] = sessionHead{First: "реплика " + sid, Said: "2026-10-20T10:00:00"}
	}
	if extra != nil {
		extra(&src)
	}
	return src
}

// migOne гоняет разбор одной записи и отдаёт её случай.
func migOne(src migSource, rec string) migCase {
	return migScan(src, []string{rec})[0]
}

// Строка 1. П1 по источникам «заказ» и «рука» назначает разговор записи:
// оба слова ставятся действием по строке записи, и подтверждение говорит
// словом «привязка». «Дерево» и касания свёртки в П1 не входят: сессия
// бокового дерева работает задачей (П2), а снятая привязка гаснет.
func TestMigP1PinnedAssigns(t *testing.T) {
	for _, tc := range []struct {
		source string
	}{
		{sessions.ByOrder},
		{sessions.ByHand},
	} {
		src := migFix(t, map[string][]sessionBind{
			"pin-1": {{Task: "XR-1", Source: tc.source, Project: "demo",
				Time: "2026-10-01T10:00:00"}},
		}, nil)
		got := migOne(src, "XR-1")
		if got.Kind != migClear || got.Auto != "pin-1" {
			t.Fatalf("источник %s: %+v", tc.source, got)
		}
		if len(got.Cands) != 1 || len(got.Cands[0].Proof) != 1 ||
			got.Cands[0].Proof[0] != migProofBind {
			t.Fatalf("источник %s: подтверждение не названо привязкой: %+v", tc.source, got.Cands)
		}
	}
	// Дерево это работа, а не привязка: разговор назначается, но словом
	// «работа».
	src := migFix(t, map[string][]sessionBind{
		"pin-2": {{Task: "XR-1", Source: sessions.ByTree, Project: "demo",
			Time: "2026-10-01T10:00:00"}},
	}, nil)
	if got := migOne(src, "XR-1"); got.Kind != migClear || got.Auto != "pin-2" ||
		got.Cands[0].Proof[0] != migProofWork {
		t.Fatalf("дерево не назначило работой: %+v", got)
	}
	// Снятие гасит касание целиком: задачи у разговора больше нет, и кандидат
	// не собирается вовсе, а не висит без подтверждений.
	src = migFix(t, map[string][]sessionBind{
		"pin-3": {{Task: "XR-1", Source: sessions.ByOrder, Project: "demo",
			Time: "2026-10-01T10:00:00"}},
	}, nil)
	src.Sessions["pin-3"] = append(src.Sessions["pin-3"], sessionBind{
		Task: "XR-1", Source: sessions.ByOff, Project: "demo", Time: "2026-10-01T12:00:00"})
	if got := migOne(src, "XR-1"); got.Kind != migNoTalks || len(got.Cands) != 0 {
		t.Fatalf("снятая привязка оставила кандидата: %+v", got)
	}
}

// Строка 2. П2 по работе: заходы под именем task-<R> при записи работы R,
// записи реестра с источником «работа», вход черновика и записи витков цели.
func TestMigP2WorkAssigns(t *testing.T) {
	// Окно конвейера при runs-файле: носителем становится само окно.
	src := migFix(t, map[string][]sessionBind{
		"conv-1": {{Project: "demo", Tmux: "task-XR-2", Time: "2026-10-01T10:00:00"}},
	}, func(s *migSource) { s.Runs["XR-2"] = true })
	got := migOne(src, "XR-2")
	if got.Kind != migClear || got.Auto != "tmux-task-XR-2" ||
		got.Cands[0].Proof[0] != migProofWork {
		t.Fatalf("окно конвейера с runs-файлом: %+v", got)
	}
	// Записи реестра с источником «работа»: свёртка WorksOn.
	src = migFix(t, map[string][]sessionBind{
		"conv-2": {{Task: "XR-2", Source: sessions.BySrc, Project: "demo",
			Time: "2026-10-01T11:00:00"}},
	}, nil)
	got = migOne(src, "XR-2")
	if got.Kind != migClear || got.Auto != "conv-2" {
		t.Fatalf("запись работы с источником работа: %+v", got)
	}
	// Черновику достаточно непустого входа: замка головы и runs-файла у него
	// не бывает, и заходы под именем task-<R> без них работают не по П2
	// конвейера, а по входу черновика.
	src = migFix(t, map[string][]sessionBind{
		"conv-3": {{Project: "demo", Tmux: "task-XR-3", Time: "2026-10-01T12:00:00"}},
	}, func(s *migSource) { s.Drafts["XR-3"], s.Inbox["XR-3"] = true, true })
	got = migOne(src, "XR-3")
	if got.Kind != migClear || got.Auto != "tmux-task-XR-3" || got.Cands[0].Proof[0] != migProofWork {
		t.Fatalf("вход черновика: %+v", got)
	}
	// Цели достаточно записей витков: носитель «цикл цели» называет цель
	// строкой реестра витка.
	src = migFix(t, map[string][]sessionBind{
		"goal-1": {{Project: "demo", Time: "2026-10-01T13:00:00",
			Carrier: "цикл цели XR-4"}},
	}, nil)
	got = migOne(src, "XR-4")
	if got.Kind != migClear || got.Auto != "goal-1" {
		t.Fatalf("виток цели: %+v", got)
	}
}

// Строка 3. Назначение по свежести исключено: свежий транскрипт и свежая
// запись реестра роль не доказывают, и в споре со старым подтверждённым
// разговором выигрывает подтверждённый, а не свежий.
func TestMigFreshnessNotEnough(t *testing.T) {
	src := migFix(t, map[string][]sessionBind{
		"prov-5": {{Task: "XR-5", Source: sessions.ByOrder, Project: "demo",
			Time: "2026-09-01T10:00:00"}},
		"fresh-5": {{Project: "demo", Tmux: "task-XR-5", Time: "2026-10-24T09:00:00"}},
	}, func(s *migSource) {
		s.Heads["prov-5"] = sessionHead{First: "старый подтверждённый", Said: "2026-09-01T10:05:00"}
		s.Heads["fresh-5"] = sessionHead{First: "свежая реплика", Said: "2026-10-24T09:05:00"}
	})
	got := migOne(src, "XR-5")
	if got.Kind != migClear || got.Auto != "prov-5" {
		t.Fatalf("свежесть победила подтверждение: %+v", got)
	}
	if len(got.Cands) != 2 {
		t.Fatalf("свежий разговор не вошёл в кандидаты: %+v", got)
	}
	// Свежий разговор без подтверждений сам по себе не назначается.
	solo := migFix(t, map[string][]sessionBind{
		"fresh-6": {{Project: "demo", Tmux: "task-XR-5", Time: "2026-10-24T09:00:00"}},
	}, func(s *migSource) {
		s.Heads["fresh-6"] = sessionHead{First: "свежая реплика", Said: "2026-10-24T09:05:00"}
	})
	if got := migOne(solo, "XR-5"); got.Kind != migDisputed || got.Auto != "" {
		t.Fatalf("свежий разговор без подтверждений назначил: %+v", got)
	}
}

// Строка 4. Имя окна с ID без строки реестра из П1 и без записи работы из П2
// не назначается: имя окна только касание.
func TestMigWindowNameNotEnough(t *testing.T) {
	src := migFix(t, map[string][]sessionBind{
		"win-1": {{Project: "demo", Tmux: "task-XR-6", Time: "2026-10-01T10:00:00"}},
	}, nil)
	got := migOne(src, "XR-6")
	if got.Kind != migDisputed || got.Auto != "" || len(got.Cands) != 1 ||
		got.Cands[0].Key != "tmux-task-XR-6" || len(got.Cands[0].Proof) != 0 {
		t.Fatalf("имя окна без реестра и работы назначило: %+v", got)
	}
}

// Строка 5. Упоминание ID в тексте реплик не назначает: классификатор текст
// не читает, касание текстом не становится.
func TestMigMentionNotEnough(t *testing.T) {
	src := migFix(t, map[string][]sessionBind{
		"txt-1": {{Project: "demo", Time: "2026-10-01T10:00:00"}},
	}, func(s *migSource) {
		s.Heads["txt-1"] = sessionHead{First: "возьми задачу XR-7 в работу", Said: "2026-10-01T10:01:00"}
	})
	if got := migOne(src, "XR-7"); got.Kind != migNoTalks || len(got.Cands) != 0 {
		t.Fatalf("упоминание ID стало касанием: %+v", got)
	}
}

// Строка 6. Единственный найденный транскрипт не назначается: единственность
// разговора не подтверждение, кандидата без признаков нет вовсе.
func TestMigSingleTranscriptNotEnough(t *testing.T) {
	src := migFix(t, map[string][]sessionBind{
		"lone-1": {{Project: "demo", Time: "2026-10-01T10:00:00"}},
	}, nil)
	if got := migOne(src, "XR-8"); got.Kind != migNoTalks {
		t.Fatalf("единственный транскрипт назначил: %+v", got)
	}
}

// Строка 7. Цепочка продолжений наследует доказанность головы и назначается
// целиком, а без подтверждённого члена цепочка не доказывает ничего.
func TestMigChainInheritsProof(t *testing.T) {
	// Голова подтверждена П1, продолжение поднято позже своим транскриптом:
	// разговор один, и носителем назначается свежий заход цепочки.
	src := migFix(t, map[string][]sessionBind{
		"old-7": {{Task: "XR-9", Source: sessions.ByOrder, Project: "demo",
			Time: "2026-10-01T10:00:00"}},
		"new-7": {{Project: "demo", Time: "2026-10-02T10:00:00"}},
	}, func(s *migSource) { s.Store["new-7"] = chatStore{From: "old-7"} })
	got := migOne(src, "XR-9")
	if got.Kind != migClear || got.Auto != "new-7" || got.Cands[0].Proof[0] != migProofBind {
		t.Fatalf("цепочка от подтверждённой головы не назначилась: %+v", got)
	}
	// Та же цепочка без подтверждённого члена: касание окна без записи работы,
	// продолжение доклеилось, а подтверждения не набралось.
	src = migFix(t, map[string][]sessionBind{
		"old-8": {{Project: "demo", Tmux: "task-XR-10", Time: "2026-10-01T10:00:00"}},
		"new-8": {{Project: "demo", Time: "2026-10-02T10:00:00"}},
	}, func(s *migSource) { s.Store["new-8"] = chatStore{From: "old-8"} })
	got = migOne(src, "XR-10")
	if got.Kind != migDisputed || got.Auto != "" || got.Cands[0].Key != "tmux-task-XR-10" {
		t.Fatalf("цепочка без подтверждённого члена назначила: %+v", got)
	}
}

// Строка 8. Два подтверждённых кандидата дают спорный случай без
// автоназначения: автоматике выбирать между двумя доказанными разговорами
// нечем, и человек решает экраном выбора.
func TestMigTwoProvenDisputed(t *testing.T) {
	src := migFix(t, map[string][]sessionBind{
		"ord-a": {{Task: "XR-11", Source: sessions.ByOrder, Project: "demo",
			Time: "2026-10-01T10:00:00"}},
		"hnd-b": {{Task: "XR-11", Source: sessions.ByHand, Project: "demo",
			Time: "2026-10-02T10:00:00"}},
	}, nil)
	got := migOne(src, "XR-11")
	if got.Kind != migDisputed || got.Auto != "" || len(got.Cands) != 2 {
		t.Fatalf("два подтверждённых не стали спором: %+v", got)
	}
	if !strings.Contains(got.Note, "2") {
		t.Fatalf("причина спора не назвала число кандидатов: %+v", got)
	}
}

// Строка 9. Общий разговор двух задач исключён из автоназначения обеим:
// инвариант журнала держит разговор основным не больше чем для одной записи,
// и задачам поднимаются отдельные чаты.
func TestMigSharedTalkExcluded(t *testing.T) {
	src := migFix(t, map[string][]sessionBind{
		"sh-1": {
			{Task: "XR-12", Source: sessions.ByOrder, Project: "demo",
				Time: "2026-10-01T10:00:00"},
			{Task: "XR-13", Source: sessions.ByOrder, Project: "demo",
				Time: "2026-10-01T11:00:00"},
		},
	}, nil)
	for _, rec := range []string{"XR-12", "XR-13"} {
		got := migOne(src, rec)
		if got.Kind != migShared || got.Auto != "" || !got.Cands[0].Shared {
			t.Fatalf("общий разговор назначен записи %s: %+v", rec, got)
		}
	}
}

// Строка 10. Пустой разговор и разговор без транскрипта не назначаются,
// состояния названы словами: «пустой» у транскрипта без реплик, «без
// транскрипта» у сессии без файла.
func TestMigEmptyAndNoTranscriptNamed(t *testing.T) {
	src := migFix(t, map[string][]sessionBind{
		"emp-1": {{Task: "XR-14", Source: sessions.ByOrder, Project: "demo",
			Time: "2026-10-01T10:00:00"}},
		"nof-1": {{Task: "XR-15", Source: sessions.ByOrder, Project: "demo",
			Time: "2026-10-01T11:00:00"}},
	}, func(s *migSource) {
		s.Feeds["emp-1"] = migFeedBlank
		s.Heads["emp-1"] = sessionHead{}
		delete(s.Feeds, "nof-1")
	})
	if got := migOne(src, "XR-14"); got.Kind != migNoFeed ||
		got.Cands[0].Feed != migFeedBlank {
		t.Fatalf("пустой разговор не назван словом: %+v", got)
	}
	if got := migOne(src, "XR-15"); got.Kind != migNoFeed ||
		got.Cands[0].Feed != migNoFeed {
		t.Fatalf("отсутствующий транскрипт не назван словом: %+v", got)
	}
}

// Строка 11. Закрытая запись назначается по тем же пунктам и подключается
// при обращении: ручка адреса находит архивную строку, и однозначный разбор
// пишет строку «миграция» прямо в ответ на первое же обращение.
func TestMigClosedRowConnectedByAddress(t *testing.T) {
	e, c := migEnv(t)
	arch := "# Архив (префикс XR)\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n" +
		"|--------|--------|-----|---|---------|--------|\n" +
		"| XR-009 | Закрытая с хвостом | bug | P1 | 2026-08-13 | [tasks/archive/2026/XR-009.md](tasks/archive/2026/XR-009.md) |\n"
	if err := os.WriteFile(filepath.Join(e.proj, "docs", "TASKS-archive.md"), []byte(arch), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSession(t, e.home, e.proj, "", "arc-9",
		sessionLine("разбор закрытой задачи", "main"),
		time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC))
	writeBinds(t, e.home, bindRecord(e.home, "2026-08-13T10:01:00", "arc-9", "XR-009", sessions.ByOrder))
	for i := 0; i < 2; i++ {
		code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-009/chat")
		if code != http.StatusOK || got.Chat != "arc-9" || got.Source != mainchat.ByMigrate {
			t.Fatalf("обращение %d не подключило закрытую запись: %d %+v", i, code, got)
		}
	}
	data := readFile(t, mainchat.Path(e.home))
	if n := strings.Count(data, "источник миграция"); n != 1 {
		t.Fatalf("строк миграции %d, жду одну:\n%s", n, data)
	}
}

// Строка 12. Повторный запуск идемпотентен, ручной выбор старше автоматики:
// применённое не переписывается, снятое рукой не возвращается.
func TestMigApplyIdempotentHandOlder(t *testing.T) {
	e, _ := migEnv(t)
	writeSession(t, e.home, e.proj, "", "dup-1",
		sessionLine("работа по строке", "main"),
		time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
	writeBinds(t, e.home, bindRecord(e.home, "2026-10-01T10:01:00", "dup-1", "XR-002", sessions.ByOrder))
	lines := e.migApplyLines(t)
	if len(lines) != 1 || !strings.Contains(lines[0], "XR-002 -> dup-1") {
		t.Fatalf("первое применение не назначило: %v", lines)
	}
	// Повторный прогон видит действующее назначение и пропускает запись:
	// строка «миграция» в журнале одна.
	lines = e.migApplyLines(t)
	if len(lines) != 1 || !strings.Contains(lines[0], "назначение уже стоит") {
		t.Fatalf("повторное применение не сказало пропуск: %v", lines)
	}
	if n := strings.Count(readFile(t, mainchat.Path(e.home)), "источник миграция"); n != 1 {
		t.Fatalf("строк миграции %d, жду одну", n)
	}
	// Рука сместила назначение, и автоматика слово человека не переписывает.
	if err := mainchat.Put(e.home, mainchat.Bind{
		Record: "XR-002", Chat: "by-hand", Project: "demo", Source: mainchat.ByHand}); err != nil {
		t.Fatal(err)
	}
	lines = e.migApplyLines(t)
	if len(lines) != 1 || !strings.Contains(lines[0], "назначение уже стоит") {
		t.Fatalf("автоматика перезаписала ручной выбор: %v", lines)
	}
	// Снятое рукой автоматика не возвращает.
	if err := mainchat.Put(e.home, mainchat.Bind{
		Record: "XR-002", Source: mainchat.ByOff}); err != nil {
		t.Fatal(err)
	}
	lines = e.migApplyLines(t)
	if len(lines) != 1 || !strings.Contains(lines[0], "снято") {
		t.Fatalf("автоматика вернула снятое рукой: %v", lines)
	}
}

// Строка 13. Откат восстанавливает прежние связи и не трогает переписку,
// созданную после назначения: обратные строки дописываются, назначение,
// смещённое рукой после миграции, откат не снимает.
func TestMigRollbackKeepsHandAndTalks(t *testing.T) {
	e, _ := migEnv(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if err := mainchat.Put(e.home, mainchat.Bind{
		Record: "XR-002", Chat: "mig-talk", Project: "demo", Source: mainchat.ByMigrate}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.chatStoreWrite("mig-talk", chatStore{Blank: true, Born: now.Unix(),
		Project: "demo", Task: "XR-002", Draft: "переписка после назначения"}); err != nil {
		t.Fatal(err)
	}
	if err := mainchat.Put(e.home, mainchat.Bind{
		Record: "XR-005", Chat: "gone-talk", Project: "demo", Source: mainchat.ByMigrate}); err != nil {
		t.Fatal(err)
	}
	// Рука сместила назначение второй записи уже после миграции.
	if err := mainchat.Put(e.home, mainchat.Bind{
		Record: "XR-005", Chat: "hand-talk", Project: "demo", Source: mainchat.ByHand}); err != nil {
		t.Fatal(err)
	}
	lines := migRollback(e.home, e.s.mainChatData())
	if len(lines) != 1 || !strings.Contains(lines[0], "XR-002") {
		t.Fatalf("откат снял не только миграцию: %v", lines)
	}
	binds := mainchat.Parse(e.s.mainChatData())
	if binds["XR-002"].Chat != "" || binds["XR-002"].Source != mainchat.ByOff {
		t.Fatalf("миграционное назначение не снято: %+v", binds["XR-002"])
	}
	if binds["XR-005"].Chat != "hand-talk" || binds["XR-005"].Source != mainchat.ByHand {
		t.Fatalf("откат тронул ручной выбор: %+v", binds["XR-005"])
	}
	// Переписка носителя остаётся разговором: откат снимает связи, а не
	// уничтожает записи.
	if st := e.s.chatStoreRead("mig-talk"); !st.Blank || st.Draft == "" {
		t.Fatalf("переписка носителя пропала: %+v", st)
	}
}

// Строка 14. Автоназначение не претендует на разговор, подтверждённый для
// другой записи, и на живой носитель действующей работы.
func TestMigForeignProofAndBusyCarrier(t *testing.T) {
	// Разговор подтверждён двум записям разными пунктами: П1 одной и окно
	// конвейера с runs-файлом другой. Касание двух записей делает разговор
	// общим, и ни одна запись его не получает.
	src := migFix(t, map[string][]sessionBind{
		"two-1": {
			{Task: "XR-16", Source: sessions.ByOrder, Project: "demo",
				Time: "2026-10-01T10:00:00"},
			{Project: "demo", Tmux: "task-XR-17", Time: "2026-10-01T11:00:00"},
		},
	}, func(s *migSource) { s.Runs["XR-17"] = true })
	for _, rec := range []string{"XR-16", "XR-17"} {
		if got := migOne(src, rec); got.Auto != "" {
			t.Fatalf("разговор подтверждён двум записям, %s получил назначение: %+v", rec, got)
		}
	}
	// Живой носитель действующей работы: перенос назначения ждёт стопа,
	// разбор видит живую работающую сессию и не претендует на неё.
	src = migFix(t, map[string][]sessionBind{
		"biz-1": {{Task: "XR-18", Source: sessions.ByOrder, Project: "demo",
			Time: "2026-10-01T10:00:00"}},
	}, func(s *migSource) { s.Busy["biz-1"] = true })
	got := migOne(src, "XR-18")
	if got.Kind != migDisputed || got.Auto != "" {
		t.Fatalf("живой носитель получил назначение: %+v", got)
	}
	if !strings.Contains(got.Note, "живой носитель") {
		t.Fatalf("причина не названа словами: %+v", got)
	}
}

// migEnv это стенд миграции с изолированным домом машины: реестр и журнал
// настоящего дома не расширяют снимок фикстуры.
func migEnv(t *testing.T) (*testEnv, *http.Client) {
	t.Helper()
	e := newTestEnv(t)
	other := t.TempDir()
	was := realHomeFn
	realHomeFn = func() string { return other }
	t.Cleanup(func() { realHomeFn = was })
	return e, e.loggedClient(t)
}

// migApplyLines прогоняет сбор записей, разбор и применение на стенде и
// отдаёт строки итога.
func (e *testEnv) migApplyLines(t *testing.T) []string {
	t.Helper()
	recs := e.s.migCollect()
	closed := map[string]bool{}
	projectOf := map[string]string{}
	ids := make([]string, 0, len(recs))
	for _, r := range recs {
		ids = append(ids, r.ID)
		closed[r.ID] = r.Closed
		projectOf[r.ID] = r.Project
	}
	return e.s.migApply(migScan(e.s.migSnapshot(recs), ids), projectOf, closed)
}

// Спорный разбор открывает выбор при первом входе: ручка адреса отдаёт слово
// раздела и кандидатов с датами, репликами и признаками работы, действие
// «Сделать основным» пишет строку «рука», а «Начать новый основной чат»
// заводит незачатый разговор записи.
func TestMigChoiceByAddress(t *testing.T) {
	e, c := migEnv(t)
	writeSession(t, e.home, e.proj, "", "cho-1",
		sessionLine("первый разговор записи", "main"),
		time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
	writeSession(t, e.home, e.proj, "", "cho-2",
		sessionLine("второй разговор записи", "main"),
		time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC))
	writeBinds(t, e.home,
		bindRecord(e.home, "2026-10-01T10:01:00", "cho-1", "XR-002", sessions.ByOrder),
		bindRecord(e.home, "2026-10-02T10:01:00", "cho-2", "XR-002", sessions.ByHand))
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/tasks/XR-002/chat", "")
	var got struct {
		Chat       string    `json:"chat"`
		Kind       string    `json:"kind"`
		Note       string    `json:"note"`
		Candidates []migCand `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Chat != "" || got.Kind != migDisputed || len(got.Candidates) != 2 {
		t.Fatalf("спорный разбор не открыл выбор: %+v", got)
	}
	for _, cand := range got.Candidates {
		if cand.Date == "" || cand.Last == "" || len(cand.Proof) == 0 {
			t.Fatalf("кандидат без даты, реплики либо признака работы: %+v", cand)
		}
	}
	// Выбор человека: разговор становится основным строкой «рука», и повторное
	// обращение адреса разрешается в него же.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-002/chat/bind",
		`{"chat": "cho-2"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("выбор человека не записался: %d %s", resp.StatusCode, body(t, resp))
	}
	_, after := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-002/chat")
	if after.Chat != "cho-2" || after.Source != mainchat.ByHand {
		t.Fatalf("выбранный разговор не стал основным: %+v", after)
	}
	// Новый основной чат: незачатый разговор записи, сессию поднимет первая
	// реплика, и вторая запись сюда больше не попадает.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-005/chat/new", "")
	var fresh struct {
		Chat   string `json:"chat"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &fresh); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || fresh.Chat == "" || fresh.Source != mainchat.ByHand {
		t.Fatalf("новый основной чат не завёлся: %d %+v", resp.StatusCode, fresh)
	}
	if st := e.s.chatStoreRead(fresh.Chat); !st.Blank || st.Task != "XR-005" {
		t.Fatalf("новый основной чат не привязан к записи: %+v", st)
	}
	_, done := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if done.Chat != fresh.Chat || done.Source != mainchat.ByHand {
		t.Fatalf("адрес не разрешается в новый чат: %+v", done)
	}
}

// Экран выбора гасит ввод до решения: слова клиента сторожат сам экран,
// как у плашки неназначенной записи.
func TestStaticChoiceWay(t *testing.T) {
	app, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`kind: "choice"`,
		`"Сделать основным"`,
		`"Начать новый основной чат"`,
		`chatChoiceURL("bind")`,
		`chatChoiceURL("new")`,
	} {
		if !strings.Contains(string(app), want) {
			t.Errorf("в app.js нет %s", want)
		}
	}
}

// Отчёт до применения называет разделы словами и не назначает ничего: прогон
// без --apply оставляет журнал нетронутым.
func TestMigReportWithoutApply(t *testing.T) {
	e, _ := migEnv(t)
	writeSession(t, e.home, e.proj, "", "rep-1",
		sessionLine("разговор для отчёта", "main"),
		time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
	writeBinds(t, e.home, bindRecord(e.home, "2026-10-01T10:01:00", "rep-1", "XR-002", sessions.ByOrder))
	var out strings.Builder
	recs := e.s.migCollect()
	closed := map[string]bool{}
	ids := make([]string, 0, len(recs))
	for _, r := range recs {
		ids = append(ids, r.ID)
		closed[r.ID] = r.Closed
	}
	migReport(&out, migScan(e.s.migSnapshot(recs), ids), closed)
	text := out.String()
	for _, want := range []string{"однозначные:", "XR-002 -> rep-1", "спорные:", "общие:", "без транскрипта:"} {
		if !strings.Contains(text, want) {
			t.Errorf("в отчёте нет %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(mainchat.Path(e.home)); !os.IsNotExist(err) {
		t.Fatalf("отчёт без применения тронул журнал: %v", err)
	}
}
