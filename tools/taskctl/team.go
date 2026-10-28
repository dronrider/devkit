package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

// Команда машины (DK-1468): участники с ролями, люди и агентские модели.
// Файл ~/.devkit/team.toml стоит рядом с user.md, каналы людей чувствительны
// и в гит не идут. Профили агентских участников остаются лестницей ярусов и
// определениями kit, команда ссылается на ярус, а не дублирует определения.

// TeamParams это поля участника, которые задаёт team add.
type TeamParams struct {
	Kind, Name, Model, Tier, Channel string
	Roles                            []string
}

// cmdTeamAdd заводит участника или правит поля уже стоящего: повторный add
// с тем же handle перезаписывает запись целиком, частичного слияния полей
// нет, иначе источник правды о составе расползся бы между двумя вызовами.
func cmdTeamAdd(home, handle string, p TeamParams) (string, error) {
	if !handleRe.MatchString(handle) {
		return "", fmt.Errorf("имя участника %q не годится: латиница, цифры, дефис и подчёркивание, как у секции в team.toml", handle)
	}
	switch p.Kind {
	case team.Human, team.Agent:
	default:
		return "", fmt.Errorf("вид %q не разбирается, жду human или agent", p.Kind)
	}
	if len(p.Roles) == 0 {
		return "", fmt.Errorf("ролей нет, жду хотя бы одну из exec/review/check/inspect ключом --role")
	}
	np := team.Participant{Handle: handle, Kind: p.Kind, Name: p.Name, Model: p.Model, Tier: p.Tier, Roles: p.Roles, Channel: p.Channel}
	if p.Kind == team.Agent {
		if np.Tier == "" {
			return "", fmt.Errorf("агентскому участнику %s нужен ярус mini/base/pro/max ключом --tier", handle)
		}
		if np.Model == "" {
			return "", fmt.Errorf("агентскому участнику нужна модель ключом --model: по её меткам в «Ходе работы» считается профиль")
		}
	}
	list, err := team.Load(home)
	if err != nil {
		return "", err
	}
	var old team.Participant
	replaced := false
	for i := range list {
		if list[i].Handle == handle {
			old = list[i]
			list[i] = np
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, np)
	}
	if err := team.Save(home, list); err != nil {
		return "", err
	}
	verb := "добавлен"
	lost := ""
	if replaced {
		verb = "заменён"
		lost = lostFields(old, np)
	}
	out := fmt.Sprintf("%s %s", handle, verb)
	if lost != "" {
		out += "; утрачено: " + lost
	}
	return out + ": " + teamLine(np), nil
}

// lostFields называет то, что перезапись команды унесла из прежней записи:
// роли, которых больше нет, и канал без замены. Перезапись целиком это
// заявленное поведение, но молча потерянная роль exec отказывает потомкам
// set --assign и remind, и замена обязана признаться в потерях на месте.
func lostFields(old, np team.Participant) string {
	var parts []string
	var roles []string
	for _, r := range old.Roles {
		if !np.HasRole(r) {
			roles = append(roles, team.RoleName(r))
		}
	}
	if len(roles) > 0 {
		parts = append(parts, "роли "+strings.Join(roles, ", "))
	}
	if old.Channel != "" && np.Channel == "" {
		parts = append(parts, "канал "+old.Channel)
	}
	return strings.Join(parts, ", ")
}

// cmdTeamRm убирает участника из команды. Назначения на строках при этом
// остаются и читаются pick как предупреждение: снять их сразу значило бы
// править доску из машинной команды молча.
func cmdTeamRm(home, handle string) (string, error) {
	list, err := team.Load(home)
	if err != nil {
		return "", err
	}
	out := list[:0]
	found := false
	for _, p := range list {
		if p.Handle == handle {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return "", fmt.Errorf("участника %s нет в команде", handle)
	}
	if err := team.Save(home, out); err != nil {
		return "", err
	}
	return handle + ": убран из команды, назначения на строках остались, снимаются taskctl set --assign -", nil
}

// cmdTeamList печатает состав команды одной строкой на участника.
func cmdTeamList(home string) (string, error) {
	list, err := team.Load(home)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "команда пуста, первого участника заводит taskctl team add", nil
	}
	var b strings.Builder
	for _, p := range list {
		fmt.Fprintf(&b, "%s: %s\n", p.Handle, teamLine(p))
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// teamLine собирает человеческую строку участника: вид словами словаря,
// ярус или модель у агента, роли русскими именами, канал у человека.
func teamLine(p team.Participant) string {
	kind := "человек"
	parts := []string{}
	if p.Kind == team.Agent {
		kind = "агент"
		tier := p.Tier
		if p.Model != "" {
			tier += ", модель " + p.Model
		}
		parts = append(parts, "ярус "+tier)
	}
	if p.Name != "" {
		parts = append(parts, "имя "+p.Name)
	}
	roles := make([]string, 0, len(p.Roles))
	for _, r := range p.Roles {
		roles = append(roles, team.RoleName(r))
	}
	parts = append(parts, "роли: "+strings.Join(roles, ", "))
	if p.Channel != "" {
		parts = append(parts, "канал "+p.Channel)
	}
	return kind + ", " + strings.Join(parts, "; ")
}

// cmdTeamShow печатает участника и срез его профиля по доске: кто автор и
// кто ревьюил (DK-1468, развилка «профиль-участника» закрыта срезом поверх
// review stats, новый счёт ошибок с нуля не заводится). Авторство агентского
// участника считают метки «Хода работы» (хук спавна пишет модель), ревью и
// проверки теми же метками, у человека авторство считает суффикс [исп: ...]
// на строках доски и архива. Срез виден назначению строки и ревьюверу как
// вход про сильные стороны.
func cmdTeamShow(root, home, handle string) (string, error) {
	list, err := team.Load(home)
	if err != nil {
		return "", err
	}
	p, ok := team.Find(list, handle)
	if !ok {
		return "", fmt.Errorf("участника %s нет в команде, состав показывает taskctl team list", handle)
	}
	s := teamProfile(root, p)
	return fmt.Sprintf("%s: %s\nпрофиль (срез review stats): авторство %d, ревью %d, замечания %d (исправлено %d, отклонено %d), проверки %d",
		handle, teamLine(p), s.Authored, s.Reviews, s.Notes, s.Fixed, s.Rejected, s.Checks), nil
}

// profileSlice это счётчики среза по одному участнику.
type profileSlice struct {
	Authored, Reviews, Notes, Fixed, Rejected, Checks int
}

// teamProfile обходит файлы задач и строки доски и собирает срез. Считаются
// задачи, не записи: две записи разработки одной задачи это одна работа
// одного автора, и дублирование записи раздувало бы счёт на каждый круг
// доработки.
func teamProfile(root string, p team.Participant) profileSlice {
	var s profileSlice
	// проход по файлам задач: живые и архивные, архив лежит на год глубже
	var taskFiles []string
	if fs, _ := filepath.Glob(filepath.Join(root, "docs", "tasks", "*.md")); len(fs) > 0 {
		taskFiles = append(taskFiles, fs...)
	}
	if fs, _ := filepath.Glob(filepath.Join(root, "docs", "tasks", "archive", "*", "*.md")); len(fs) > 0 {
		taskFiles = append(taskFiles, fs...)
	}
	if p.Kind == team.Agent && p.Model != "" {
		for _, f := range taskFiles {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var authored, reviewed, checked bool
			for _, ln := range strings.Split(string(data), "\n") {
				t := strings.TrimSpace(ln)
				switch {
				case strings.HasPrefix(t, "- Разработка:"), strings.HasPrefix(t, "- Доработка:"):
					if m, ok := stage.Executor(t); ok && m == p.Model {
						authored = true
					}
				case strings.HasPrefix(t, "- Ревью:"):
					if m, ok := stage.Executor(t); ok && m == p.Model {
						reviewed = true
					}
				default:
					if m, ok := stage.VerifyRunner(t); ok && m == p.Model {
						checked = true
					}
				}
			}
			if authored {
				s.Authored++
			}
			if reviewed {
				s.Reviews++
				if rf, err := loadReview(f); err == nil {
					for _, n := range rf.notes {
						switch n.outcome() {
						case "исправлено":
							s.Notes++
							s.Fixed++
						case "отклонено":
							s.Notes++
							s.Rejected++
						}
					}
				}
			}
			if checked {
				s.Checks++
			}
		}
	}
	if p.Kind == team.Human {
		s.Authored = rowsAssignedTo(root, p.Handle)
	}
	return s
}

// rowsAssignedTo считает строки с назначением участника на живой доске и в
// архиве: назначение переживает close, поэтому счёт не теряется закрытием.
func rowsAssignedTo(root, handle string) int {
	n := 0
	for _, path := range []string{boardPath(root), archivePath(root)} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, ln := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(ln)
			if !strings.HasPrefix(t, "|") {
				continue
			}
			cells := strings.Split(strings.Trim(t, "|"), "|")
			if len(cells) < 2 {
				continue
			}
			if assignOf(strings.TrimSpace(cells[1])) == handle {
				n++
			}
		}
	}
	return n
}
