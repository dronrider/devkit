// Package obey держит привязку сценария стенда к тексту, который он меряет:
// разбор ключа «предмет», проверку привязки по дереву, отпечаток текста
// предмета и список путей, чей текст едет в контекст агента (LLD DK-805,
// решения 1 и 2). Читателей у привязки двое, стенд obeycheck и ворота слияния
// shipctl, и второй копии разбора им не нужно: разойдясь на первой же правке,
// копии стали бы отбивать слияние прогону, который сами и зачли.
package obey

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Key это ключ шапки сценария, которым тот называет свой предмет.
const Key = "предмет"

// Subject это один предмет сценария: путь от корня devkit и, необязательно,
// заголовок раздела второго уровня. Путь без раздела привязывает сценарий к
// файлу целиком.
type Subject struct {
	Path    string
	Section string
}

// String собирает предмет обратно в запись файла сценария.
func (s Subject) String() string {
	if s.Section == "" {
		return s.Path
	}
	return s.Path + " «" + s.Section + "»"
}

// Join собирает список предметов в одну строку значения ключа.
func Join(subs []Subject) string {
	var parts []string
	for _, s := range subs {
		parts = append(parts, s.String())
	}
	return strings.Join(parts, "; ")
}

// ParseSubjects разбирает значение ключа «предмет»: список через «;», элемент
// это путь и через пробел необязательный заголовок раздела в ёлочках.
func ParseSubjects(value string) ([]Subject, error) {
	var out []Subject
	for _, part := range strings.Split(value, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		s := Subject{Path: part}
		if i := strings.Index(part, "«"); i >= 0 {
			if !strings.HasSuffix(part, "»") {
				return nil, fmt.Errorf("предмет %q: раздел открыт ёлочкой и не закрыт", part)
			}
			s.Path = strings.TrimSpace(part[:i])
			s.Section = strings.TrimSpace(strings.TrimSuffix(part[i+len("«"):], "»"))
			if s.Section == "" {
				return nil, fmt.Errorf("предмет %q: пустой заголовок раздела", part)
			}
		}
		if s.Path == "" {
			return nil, fmt.Errorf("предмет %q: пустой путь", part)
		}
		if filepath.IsAbs(s.Path) || strings.Contains(s.Path, "..") {
			return nil, fmt.Errorf("предмет %q: жду путь от корня devkit", part)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("пустое значение ключа «%s»", Key)
	}
	return out, nil
}

// Verify проверяет привязку по дереву: путь обязан существовать, а названный
// раздел стоять в файле заголовком «## » вне ограждённых блоков.
// Переименованный скилл или раздел оставил бы сценарий без предмета молча, и
// прогон остался бы зелёным на тексте, которого больше нет.
func Verify(root string, s Subject) error {
	full := filepath.Join(root, s.Path)
	fi, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("предмета %s в дереве нет", s.Path)
	}
	if fi.IsDir() {
		return fmt.Errorf("предмет %s это директория, жду файл", s.Path)
	}
	if s.Section == "" {
		return nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("предмет %s: %v", s.Path, err)
	}
	if _, ok := SectionBody(string(data), s.Section); !ok {
		return fmt.Errorf("в %s нет раздела «%s»", s.Path, s.Section)
	}
	return nil
}

// VerifyAll проверяет по дереву весь список предметов.
func VerifyAll(root string, subs []Subject) error {
	for _, s := range subs {
		if err := Verify(root, s); err != nil {
			return err
		}
	}
	return nil
}

// Covers отвечает, покрывает ли предмет названный раздел файла. Пустое имя
// раздела это вопрос отбора `--for`, где раздела ещё нет: покрывает ли предмет
// этот файл хоть чем-нибудь.
func Covers(s Subject, file, section string) bool {
	if s.Path != file {
		return false
	}
	return section == "" || s.Section == "" || s.Section == section
}

// CoversAny отвечает тот же вопрос по списку предметов сценария.
func CoversAny(subs []Subject, file, section string) bool {
	for _, s := range subs {
		if Covers(s, file, section) {
			return true
		}
	}
	return false
}

// pages это страницы корня, которые агент читает как контракт: форма задачи,
// шкала ранга и виды приёмки. Правки правил ловятся отдельно, по началу имени
// `RULES`.
var pages = map[string]bool{"TASKFORM.md": true, "RANKING.md": true, "ACCEPTANCE.md": true}

// AgentFile отвечает, едет ли текст файла в контекст агента: скилл,
// определение субагента, файл правил или страница-контракт корня. Проза внутри
// kit/skills (README соседей, вспомогательные скрипты) сюда попадает заодно, и
// это дешевле разбора расширений. Путь ждётся от корня devkit.
func AgentFile(p string) bool {
	if strings.HasPrefix(p, "kit/skills/") || strings.HasPrefix(p, "kit/agents/") {
		return true
	}
	if strings.Contains(p, "/") {
		return false
	}
	return strings.HasPrefix(p, "RULES") || pages[p]
}

// SectionBody отдаёт тело раздела второго уровня: строки после заголовка
// «## <имя>» до следующего заголовка «## » вне ограждённых блоков. Заголовок
// внутри ограждённого блока разделом не считается, там лежит чужой пример.
func SectionBody(text, section string) ([]string, bool) {
	lines := strings.Split(text, "\n")
	var out []string
	in, found := false, false
	var f Fence
	for _, ln := range lines {
		if f.Step(ln) {
			if in {
				out = append(out, ln)
			}
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			if in {
				break
			}
			if strings.TrimSpace(ln[3:]) == section {
				in, found = true, true
			}
			continue
		}
		if in {
			out = append(out, ln)
		}
	}
	return out, found
}

// Text отдаёт нормализованный текст предмета: тело раздела у предмета с
// разделом и файл целиком у предмета без него. Нормализация снимает хвостовые
// пробелы и пустые строки, как у отпечатка сценария проверки: перевёрстка
// абзаца не меняет ни одного шага и след протухать не должна.
func Text(root string, s Subject) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, s.Path))
	if err != nil {
		return "", fmt.Errorf("предмет %s: %v", s.Path, err)
	}
	lines := strings.Split(string(data), "\n")
	if s.Section != "" {
		body, ok := SectionBody(string(data), s.Section)
		if !ok {
			return "", fmt.Errorf("в %s нет раздела «%s»", s.Path, s.Section)
		}
		lines = body
	}
	var kept []string
	for _, ln := range lines {
		if t := strings.TrimRight(ln, " \t"); t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, "\n"), nil
}

// Print это отпечаток текста предметов прогона: восемь знаков sha256 от их
// нормализованных текстов по порядку списка. По нему ворота слияния видят, что
// после прогона текст предмета правили. Правка соседнего раздела того же файла
// отпечаток не трогает: у предмета с разделом он снят с раздела.
func Print(root string, subs []Subject) (string, error) {
	var parts []string
	for _, s := range subs {
		t, err := Text(root, s)
		if err != nil {
			return "", err
		}
		parts = append(parts, s.String(), t)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])[:8], nil
}

// Fence следит за оградами блоков кода. Внутри блока строка «## ...» это текст
// примера, а не заголовок: раздел правил цитирует чужую разметку, а подготовка
// сценария везёт в heredoc чужую постановку с её заголовками. Разбор один на
// пакет и на стенд, две копии разошлись бы на первой правке, и на тильде они
// уже разошлись.
type Fence struct {
	open int
}

// Step прогоняет строку и отвечает, лежит ли она внутри блока кода. Сами
// ограды считаются частью блока. Закрывает блок только ограда не короче
// открывающей и без хвоста, поэтому вложенный блок с более длинной оградой
// внешний не рвёт.
func (f *Fence) Step(l string) bool {
	n := backticks(l)
	if f.open == 0 {
		if n > 0 {
			f.open = n
		}
		return n > 0
	}
	if n >= f.open && strings.TrimSpace(strings.TrimLeft(l, " \t`~")) == "" {
		f.open = 0
	}
	return true
}

// backticks считает знаки ограждения в начале строки, разрешая отступ. Ограда
// это три знака и больше, после открывающей может стоять язык.
func backticks(l string) int {
	t := strings.TrimLeft(l, " \t")
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(t) && t[n] == c {
			n++
		}
		if n >= 3 {
			return n
		}
	}
	return 0
}
