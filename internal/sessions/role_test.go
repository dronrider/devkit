package sessions

import "testing"

// Таблица «команда -> роль» и подписи отрезков ленты (DK-871). Роль выводится
// из повода записи реестра, своего поля в строке не заводится, и одинаково
// она выводится у любого писателя: команды доски одни и в терминале, и у
// сессии, поднятой панелью.

// Повод читается из строки и доезжает до читателя: структура Bind его прежде
// теряла, и роль отрезка выводить было нечем.
func TestParseLineKeepsReason(t *testing.T) {
	sid, b, ok := ParseLine("2026-09-08T12:00:00 сессия abc-1 задача DK-871 проект devkit" +
		" дерево /tmp/d транскрипт /tmp/t.jsonl источник работа повод agentctl stage ревью" +
		" tmux task-DK-871 панель %1\n")
	if !ok {
		t.Fatal("строка не разобралась")
	}
	if sid != "abc-1" || b.Task != "DK-871" {
		t.Fatalf("разбор рассыпался: %+v", b)
	}
	if b.Reason != "agentctl stage ревью" {
		t.Errorf("повод не доехал до читателя: %q", b.Reason)
	}
}

// Свёртка берёт повод свежей записи: он говорит о последнем действии сессии,
// и подпись отрезка читается со свёртки тем же словом, что и с записей.
func TestLastKeepsFreshReason(t *testing.T) {
	recs := []Bind{
		{Task: "DK-871", Source: ByOrder, Reason: "startup", Tmux: "task-DK-871"},
		{Task: "DK-871", Source: BySrc, Reason: "agentctl stage разработка"},
		{Task: "DK-871", Source: BySrc, Reason: "правка файла"},
	}
	if got := Last(recs).Reason; got != "правка файла" {
		t.Errorf("повод свёртки: %q, ждал свежий", got)
	}
}

// Таблица сводит команды доски в роли, аргументы команды роли не меняют, и
// повод без роли остаётся неизвестным.
func TestRoleByCommand(t *testing.T) {
	cases := []struct {
		reason string
		want   string
		ok     bool
	}{
		{"agentctl stage постановка", RoleGroom, true},
		{"agentctl stage вычитка", RoleDev, true},
		{"agentctl stage разработка", RoleDev, true},
		{"agentctl stage доработка", RoleDev, true},
		{"agentctl stage ревью", RoleReview, true},
		{"agentctl stage проверка", RoleCheck, true},
		{"agentctl pick --goal", RoleGoal, true},
		{"agentctl budget --goal /tmp/DK-1253.toml", RoleGoal, true},
		{"taskctl run", RoleDev, true},
		{"taskctl move DK-871 check", RoleDev, true},
		{"taskctl review show", RoleDev, true},
		{"shipctl start DK-871", RoleDev, true},
		{"shipctl smoke DK-871", RoleCheck, true},
		{"правка файла", RoleDev, true},
		// Рождения пишет хук старта любому подъёму: словами хука конвейер
		// от разговора не отличить, и роли они не говорят.
		{"startup", "", false},
		{"resume", "", false},
		{"compact", "", false},
		// Отвязка и возврат работу называют, ролью не говорят.
		{"стоп со строки доски", "", false},
		{"возврат в работу ответом человека", "", false},
		{"рука", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := Role(c.reason)
		if got != c.want || ok != c.ok {
			t.Errorf("Role(%q) = %q,%v, ждал %q,%v", c.reason, got, ok, c.want, c.ok)
		}
	}
}

// Этап стоит в поводе команды stage и читается из него словом вида
// деятельности, у прочих команд этапа нет.
func TestStageOfReason(t *testing.T) {
	cases := []struct{ reason, want string }{
		{"agentctl stage разработка", "разработка"},
		{"agentctl stage проверка --by glm-5.3", "проверка"},
		{"taskctl run", ""},
		{"правка файла", ""},
		{"agentctl stage ", ""},
		{"startup", ""},
	}
	for _, c := range cases {
		if got := Stage(c.reason); got != c.want {
			t.Errorf("Stage(%q) = %q, ждал %q", c.reason, got, c.want)
		}
	}
}

// Подпись отрезка берётся свежей записью с ролью: заход проходит роли по ходу
// работы, и стык ленты подписан итогом. Записи без роли подпись не меняют:
// ревью, ушедшее в ожидание человека, остаётся ревью. Конвейер без записей
// работы узнаётся рождением taskctl run, а разговор без работы остаётся
// без роли, и подпись выходит неизвестной.
func TestRoleStageOfPass(t *testing.T) {
	cases := []struct {
		name string
		recs []Bind
		role string
		stage string
		known bool
	}{
		{
			name: "груминг перетёк в исполнение",
			recs: []Bind{
				{Source: ByOrder, Reason: "startup"},
				{Source: BySrc, Reason: "agentctl stage постановка"},
				{Source: BySrc, Reason: "agentctl stage разработка"},
			},
			role: RoleDev, stage: "разработка", known: true,
		},
		{
			name: "ревью с ожиданием человека",
			recs: []Bind{
				{Source: BySrc, Reason: "agentctl stage ревью"},
				{Source: ByOff, Task: "DK-1", Reason: "стоп со строки доски"},
			},
			role: RoleReview, stage: "ревью", known: true,
		},
		{
			name: "конвейер без записей работы",
			recs: []Bind{
				{Source: ByOrder, Reason: "taskctl run", Tmux: "task-DK-1"},
				{Source: ByOrder, Reason: "startup"},
			},
			role: RoleDev, stage: "", known: true,
		},
		{
			name: "разговор без работы",
			recs: []Bind{
				{Source: ByOrder, Reason: "startup", Tmux: "chat-DK-1-1"},
			},
			known: false,
		},
		{
			name: "пустой список",
			known: false,
		},
	}
	for _, c := range cases {
		role, stage, known := RoleStage(c.recs)
		if role != c.role || stage != c.stage || known != c.known {
			t.Errorf("%s: RoleStage = %q,%q,%v, ждал %q,%q,%v",
				c.name, role, stage, known, c.role, c.stage, c.known)
		}
	}
}

// Слово подписи одно на всех читателей: роль с этапом через двоеточие,
// неизвестная роль называется словами и от пустой роли не отличается молчанием.
func TestRoleWord(t *testing.T) {
	if got := RoleWord(RoleDev, "разработка", true); got != "исполнение: разработка" {
		t.Errorf("подпись с этапом: %q", got)
	}
	if got := RoleWord(RoleReview, "", true); got != "ревью" {
		t.Errorf("подпись без этапа: %q", got)
	}
	if got := RoleWord("", "", false); got != RoleUnknown {
		t.Errorf("подпись неизвестной роли: %q", got)
	}
}
