// Package team читает команду машины: участников с ролями, людей и агентских
// моделей. Файл живёт в ~/.devkit/team.toml рядом с user.md (развилка
// «дом-команды» DK-1468): каналы людей чувствительны и в гит не идут, а
// профили агентских участников остаются лестницей ярусов и определениями kit.
// Пишет файл taskctl team, читают taskctl (назначение на строку, профиль,
// напоминания) и agentctl (сужение вердикта выбора агента), поэтому разбор
// лежит в общем каркасе, а не в одной утилите.
package team

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/subtoml"
)

// sufRe узнаёт суффикс назначения «[исп: имя]» в заголовке строки доски.
// Назначение стоит между планом и сроком, то есть не всегда в самом конце
// заголовка, поэтому привязки к концу строки у регулярки нет: taskctl
// снимает хвосты с конца и попадает на неё последним, а читатели сырого
// заголовка (agentctl, дашборд) ищут её по месту.
var sufRe = regexp.MustCompile(`\[исп: ([A-Za-z0-9_-]+)\]`)

// TitleHandle читает handle назначенного участника из заголовка строки,
// пустая строка значит «назначения нет».
func TitleHandle(title string) string {
	if m := sufRe.FindStringSubmatch(title); m != nil {
		return m[1]
	}
	return ""
}

// Path это путь файла команды: ~/.devkit/team.toml.
func Path(home string) string { return filepath.Join(home, ".devkit", "team.toml") }

// Роли участника на строке: разработка, ревью, тестирование, инспектор
// (разговор 2026-09-27, DK-1198). Ключи латинские, как у роли agentctl pick:
// exec и review уже заняты этим словарём, check и inspect ложатся рядом.
// Инспектор сегодня только объявляется: контроль задач по сроку это отдельная
// строка состава цели, и потребитель роли приедет ею.
const (
	RoleExec    = "exec"
	RoleReview  = "review"
	RoleCheck   = "check"
	RoleInspect = "inspect"
)

// roleNames это словарь ролей с их русскими именами для печати.
var roleNames = map[string]string{
	RoleExec:    "разработка",
	RoleReview:  "ревью",
	RoleCheck:   "тестирование",
	RoleInspect: "инспектор",
}

// RoleName даёт русское имя роли, незнакомому ключу возвращается он сам.
func RoleName(role string) string {
	if n, ok := roleNames[role]; ok {
		return n
	}
	return role
}

// tiers это лестница ярусов тем же словарем, каким её печатает agentctl
// (tools/agentctl/harness.go) и поднимает проверяющего internal/checkrun.
// Словарь стабильный, копия в каркасе дешевле зависимости утилит друг от
// друга.
var tiers = map[string]bool{"mini": true, "base": true, "pro": true, "max": true}

// Participant это участник команды. Handle латинский (его допускает имя
// секции файла), по нему назначение едет суффиксом строки; имя, канал и
// модель нужны людям и срезу профиля.
type Participant struct {
	Handle  string
	Kind    string // human или agent
	Name    string
	Model   string // модель агентского участника: по её меткам срез профиля
	Tier    string // ярус агентского участника, вердикт сужается до него
	Roles   []string
	Channel string // канал человека для напоминаний
}

// Human и Agent это значения поля kind.
const (
	Human = "human"
	Agent = "agent"
)

// HasRole говорит, допущен ли участник к роли.
func (p Participant) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// valid проверяет участника по словарям: вид из двух, роли из четырёх, ярус
// обязателен агенту и запрещён человеку. Ошибка называет участника и ключ,
// чтобы править файл было можно по самому сообщению.
func (p Participant) valid() error {
	switch p.Kind {
	case Human:
		if p.Tier != "" {
			return fmt.Errorf("%s: ярус есть у агентского участника, у человека его нет", p.Handle)
		}
	case Agent:
		if p.Tier == "" {
			return fmt.Errorf("%s: агентскому участнику нужен ярус mini/base/pro/max", p.Handle)
		}
		if !tiers[p.Tier] {
			return fmt.Errorf("%s: ярус %q вне лестницы mini/base/pro/max", p.Handle, p.Tier)
		}
	default:
		return fmt.Errorf("%s: вид %q не разбирается, жду human или agent", p.Handle, p.Kind)
	}
	if len(p.Roles) == 0 {
		return fmt.Errorf("%s: ролей нет, жду хотя бы одну из exec/review/check/inspect", p.Handle)
	}
	for _, r := range p.Roles {
		if _, ok := roleNames[r]; !ok {
			return fmt.Errorf("%s: роль %q вне словаря exec/review/check/inspect", p.Handle, r)
		}
	}
	return nil
}

// Load читает команду машины. Нет файла это пустая команда без ошибки: файл
// заводится первым участником, и машина без команды работает как работала.
// Битый файл это ошибка: молча выбросить участника значило бы назначать и
// напоминать по неполным данным.
func Load(home string) ([]Participant, error) {
	data, err := os.ReadFile(Path(home))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	doc, err := subtoml.Parse(Path(home), string(data))
	if err != nil {
		return nil, err
	}
	var out []Participant
	for _, name := range doc.Order {
		if name == "" {
			continue
		}
		t := doc.Table(name)
		if t == nil {
			continue
		}
		p := Participant{Handle: name}
		if v, ok := t.Get("kind"); ok {
			p.Kind = v.Str
		}
		if v, ok := t.Get("name"); ok {
			p.Name = v.Str
		}
		if v, ok := t.Get("model"); ok {
			p.Model = v.Str
		}
		if v, ok := t.Get("tier"); ok {
			p.Tier = v.Str
		}
		if v, ok := t.Get("channel"); ok {
			p.Channel = v.Str
		}
		if v, ok := t.Get("roles"); ok {
			p.Roles = v.Arr
		}
		if err := p.valid(); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Find ищет участника по handle, пустой результат значит «нет такого».
func Find(list []Participant, handle string) (Participant, bool) {
	for _, p := range list {
		if p.Handle == handle {
			return p, true
		}
	}
	return Participant{}, false
}

// Render собирает текст файла команды: шапка и участники в порядке записи,
// ключи в одном порядке. Шапка называет писателя, чтобы файл не правили
// руками по привычке user.md.
func Render(list []Participant) string {
	var b strings.Builder
	b.WriteString("# Команда машины: участники с ролями. Пишет taskctl team, руками не править.\n")
	b.WriteString("# Каналы людей чувствительны, файл в гит не идёт и живёт рядом с user.md.\n\n")
	for _, p := range list {
		fmt.Fprintf(&b, "[%s]\n", p.Handle)
		fmt.Fprintf(&b, "kind = %s\n", subtoml.Quote(p.Kind))
		if p.Name != "" {
			fmt.Fprintf(&b, "name = %s\n", subtoml.Quote(p.Name))
		}
		if p.Model != "" {
			fmt.Fprintf(&b, "model = %s\n", subtoml.Quote(p.Model))
		}
		if p.Tier != "" {
			fmt.Fprintf(&b, "tier = %s\n", subtoml.Quote(p.Tier))
		}
		roles := make([]string, 0, len(p.Roles))
		for _, r := range p.Roles {
			roles = append(roles, subtoml.Quote(r))
		}
		fmt.Fprintf(&b, "roles = [%s]\n", strings.Join(roles, ", "))
		if p.Channel != "" {
			fmt.Fprintf(&b, "channel = %s\n", subtoml.Quote(p.Channel))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Save записывает команду в файл машины.
func Save(home string, list []Participant) error {
	if err := os.MkdirAll(filepath.Dir(Path(home)), 0o755); err != nil {
		return err
	}
	for _, p := range list {
		if err := p.valid(); err != nil {
			return err
		}
	}
	return os.WriteFile(Path(home), []byte(Render(list)), 0o644)
}
