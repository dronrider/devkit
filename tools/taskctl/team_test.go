package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

// TestTeamAddListRm гоняет круг команды: участников заводят, состав печатается
// словами словаря ролей, повторный add перезаписывает запись, rm убирает и
// отказывает на отсутствующего (DK-1468).
func TestTeamAddListRm(t *testing.T) {
	root := setup(t)
	home := stage.Home()
	if _, err := cmdTeamAdd(home, "rider", TeamParams{Kind: team.Human, Name: "Райдер", Roles: []string{team.RoleExec, team.RoleInspect}, Channel: "telegram:@rider"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdTeamAdd(home, "glm53", TeamParams{Kind: team.Agent, Model: "glm-5.3", Tier: "pro", Roles: []string{team.RoleExec, team.RoleReview}}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdTeamList(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"rider: человек, имя Райдер; роли: разработка, инспектор; канал telegram:@rider",
		"glm53: агент, ярус pro, модель glm-5.3; роли: разработка, ревью",
	} {
		if !strings.Contains(out, part) {
			t.Fatalf("состав не несёт строку %q:\n%s", part, out)
		}
	}
	// Повторный add с тем же handle перезаписывает запись целиком и признаётся
	// в унесённых полях: молча потерянная роль exec отказала бы назначениям.
	out2, err := cmdTeamAdd(home, "rider", TeamParams{Kind: team.Human, Roles: []string{team.RoleExec}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "утрачено: роли инспектор, канал telegram:@rider") {
		t.Fatalf("замена не назвала потери: %s", out)
	}
	if _, err := cmdTeamAdd(home, "glm53", TeamParams{Kind: team.Agent, Model: "glm-5.3", Tier: "base", Roles: []string{team.RoleReview}}); err != nil {
		t.Fatal(err)
	}
	list, _ := team.Load(home)
	p, _ := team.Find(list, "glm53")
	if p.Tier != "base" || p.HasRole(team.RoleExec) {
		t.Fatalf("перезапись не заменила запись: %+v", p)
	}
	// Замена без потерь слово «утрачено» не печатает.
	out, err = cmdTeamAdd(home, "glm53", TeamParams{Kind: team.Agent, Model: "glm-5.3", Tier: "base", Roles: []string{team.RoleReview, team.RoleExec}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "утрачено") {
		t.Fatalf("замена без потерь напечатала утраты: %s", out)
	}
	if _, err := cmdTeamRm(home, "rider"); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdTeamRm(home, "rider"); err == nil {
		t.Fatal("повторное снятие должно было отбиться")
	}
	// Файл команды живёт рядом с user.md и в дерево проекта не попадает.
	rel, err := filepath.Rel(root, team.Path(home))
	if err != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("файл команды оказался внутри дерева проекта: %v", rel)
	}
}

// TestTeamAddRejectsBad: вид вне словаря, пустые роли и агент без яруса или
// модели не проходят, файл команды при отказе не меняется.
func TestTeamAddRejectsBad(t *testing.T) {
	setup(t)
	home := stage.Home()
	for name, p := range map[string]TeamParams{
		"вид вне словаря":  {Kind: "robot", Roles: []string{team.RoleExec}},
		"ролей нет":        {Kind: team.Human},
		"агент без яруса":  {Kind: team.Agent, Model: "m", Roles: []string{team.RoleExec}},
		"агент без модели": {Kind: team.Agent, Tier: "pro", Roles: []string{team.RoleExec}},
	} {
		if _, err := cmdTeamAdd(home, "x", p); err == nil {
			t.Fatalf("%s должен был отбиться", name)
		}
	}
	if _, err := os.Stat(team.Path(home)); !os.IsNotExist(err) {
		t.Fatal("отказ завёл файл команды")
	}
}

// TestTeamListEmpty: пустая команда печатает подсказку, а не пустоту: молчание
// было бы неотличимо от ошибки чтения.
func TestTeamListEmpty(t *testing.T) {
	setup(t)
	out, err := cmdTeamList(stage.Home())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "команда пуста") {
		t.Fatalf("подсказка пустой команды: %q", out)
	}
}

// TestSetAssign ставит и снимает назначение: суффикс встаёт между планом и
// сроком, JSON несёт поле assign, назначение мимо команды и мимо роли
// разработки не проходит (DK-1468).
func TestSetAssign(t *testing.T) {
	root := setup(t)
	home := stage.Home()
	if _, err := cmdTeamAdd(home, "rider", TeamParams{Kind: team.Human, Roles: []string{team.RoleExec, team.RoleInspect}, Channel: "tg:@rider"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdTeamAdd(home, "glm53", TeamParams{Kind: team.Agent, Model: "glm-5.3", Tier: "pro", Roles: []string{team.RoleReview}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Assign: "rider"}); err != nil {
		t.Fatal(err)
	}
	b, _ := LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост [исп: rider]" {
		t.Fatalf("назначение не встало: %q", got)
	}
	// Назначение встаёт между планом и сроком и переживает правку срока.
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: "2026-11-05"}); err != nil {
		t.Fatal(err)
	}
	b, _ = LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост [исп: rider] [срок: 2026-11-05]" {
		t.Fatalf("назначение не на своём месте: %q", got)
	}
	out, err := cmdListJSON(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"assign":"rider"`) {
		t.Fatalf("JSON не несёт назначение: %s", out)
	}
	// Прочерк снимает назначение.
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Assign: "-"}); err != nil {
		t.Fatal(err)
	}
	b, _ = LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост [срок: 2026-11-05]" {
		t.Fatalf("назначение не снялось: %q", got)
	}
	// Участника нет в команде.
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Assign: "ghost"}); err == nil {
		t.Fatal("назначение мимо команды прошло")
	}
	// Участник есть, но без роли разработки: назначать на строку нельзя.
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Assign: "glm53"}); err == nil {
		t.Fatal("назначение без роли разработки прошло")
	}
	board, _ := os.ReadFile(boardPath(root))
	if strings.Contains(string(board), "ghost") || strings.Contains(string(board), "glm53") {
		t.Fatal("отказ назначения оставил след на доске")
	}
}

// TestRemind: напоминание собирается адресатом из назначения и текстом из
// состояния строки, уходит в канал человека и отказывает агенту и участнику
// без канала (DK-1468).
func TestRemind(t *testing.T) {
	root := setup(t)
	home := stage.Home()
	if _, err := cmdTeamAdd(home, "rider", TeamParams{Kind: team.Human, Roles: []string{team.RoleExec}, Channel: "tg:@rider"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdTeamAdd(home, "glm53", TeamParams{Kind: team.Agent, Model: "glm-5.3", Tier: "pro", Roles: []string{team.RoleExec}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdTeamAdd(home, "bob", TeamParams{Kind: team.Human, Roles: []string{team.RoleExec}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Assign: "rider"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: "2099-01-01"}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdRemind(root, "XR-004", RemindParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"участник rider", "канал tg:@rider", "XR-004 Хвост: срок 2099-01-01"} {
		if !strings.Contains(out, part) {
			t.Fatalf("напоминание не несёт %q:\n%s", part, out)
		}
	}
	// Свой текст замещает собранный.
	out, err = cmdRemind(root, "XR-004", RemindParams{Note: "жду правку"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "XR-004 Хвост: жду правку") {
		t.Fatalf("свой текст не замещает собранный:\n%s", out)
	}
	// Ключ --to зовёт участника напрямую, назначение не обязательно.
	out, err = cmdRemind(root, "XR-005", RemindParams{To: "rider", Note: "глянь"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "участник rider") {
		t.Fatalf("напоминание по --to не дошло:\n%s", out)
	}
	// Агенту напоминание не ходит.
	if _, err := cmdRemind(root, "XR-005", RemindParams{To: "glm53"}); err == nil {
		t.Fatal("напоминание агенту прошло")
	}
	// Участник без канала остаётся без напоминания.
	if _, err := cmdRemind(root, "XR-005", RemindParams{To: "bob", Note: "x"}); err == nil {
		t.Fatal("напоминание без канала прошло")
	}
	// Без назначения, адресата и повода напоминание не собирается.
	if _, err := cmdRemind(root, "XR-005", RemindParams{}); err == nil {
		t.Fatal("напоминание без адресата прошло")
	}
}

// TestTeamShowProfile: срез профиля это review stats по участнику: авторство и
// ревью агенту считают метки «Хода работы», человеку суффикс назначения на
// строках доски (DK-1468, развилка «профиль-участника»).
func TestTeamShowProfile(t *testing.T) {
	root := setup(t)
	home := stage.Home()
	if _, err := cmdTeamAdd(home, "glm53", TeamParams{Kind: team.Agent, Model: "glm-5.3", Tier: "pro", Roles: []string{team.RoleExec, team.RoleReview}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdTeamAdd(home, "rider", TeamParams{Kind: team.Human, Roles: []string{team.RoleExec}}); err != nil {
		t.Fatal(err)
	}
	body := "## Ход работы\n\n- Разработка: субагент glm-5.3/high по определению exec-high, работа abc, минут 10, 2026-10-01 10:00-10:10.\n- Ревью: субагент glm-5.3/high по определению review-high, работа def, ходов 5, минут 3, 2026-10-01 11:00-11:03.\n\n## Ревью\n\nУровень 2 до abcd: обычная цена\n\n- [проверка, 2026-10-01] не блокирует: текст замечания: исправлено\n- [проверка, 2026-10-01] не блокирует: второе: отклонено, не нужно\n\n## Проверка\n\n- Обкатка: 2026-10-01, шагов 1, все зелёные.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-002.md"), []byte("# XR-002\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdTeamShow(root, home, "glm53")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"авторство 1", "ревью 1", "замечания 2 (исправлено 1, отклонено 1)"} {
		if !strings.Contains(out, part) {
			t.Fatalf("срез агента не несёт %q:\n%s", part, out)
		}
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Assign: "rider"}); err != nil {
		t.Fatal(err)
	}
	out, err = cmdTeamShow(root, home, "rider")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "авторство 1") {
		t.Fatalf("срез человека не считает назначение:\n%s", out)
	}
	if _, err := cmdTeamShow(root, home, "ghost"); err == nil {
		t.Fatal("профиль отсутствующего участника прошёл")
	}
}
