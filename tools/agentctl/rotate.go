package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// execRotateDefault это порог ротации исполнителя-субагента, когда ключа
// exec_rotate_tokens в машинном конфиге нет. Число из статистики работы
// диспетчеров: типовая крупная пачка задач стоит исполнителю 470-490 тысяч
// токенов, усталость видна с 600 тысяч, деградация с 900. Порог отсекает
// исполнителя после одной крупной пачки.
const execRotateDefault = 500000

// dispatcherRotateDefault это порог ротации диспетчера-сессии по контексту,
// когда ключа dispatcher_rotate_tokens в машинном конфиге нет (DK-662).
// Число названо болью: панель /usage показывает деградацию длинных ходов
// диспетчера с 150 тысяч токенов контекста, и ниже этого числа хвост
// дешевле держать памятью, а не передачей.
const dispatcherRotateDefault = 150000

// contextEnv это переменная стенда с подставленным счётчиком контекста
// (DK-662). Тесты цикла и самопроверка оболочек не читают журналы харнеса, а
// кладут число токенов сюда: живая механика меряет транскрипт сессии, а
// стенд подставляет счётчик тем же местом, которым скилл мерит себя сам.
const contextEnv = "DEVKIT_CONTEXT_TOKENS"

// execRotate отдаёт порог ротации и повод этого числа. Ключ живёт в машинном
// конфиге, а умолчание считает agentctl, а не потребитель: число одно на
// диспетчера в чате и на дашборд, и разъехаться двум копиям константы негде.
func execRotate(l *layers, path string) (int, string) {
	if l != nil && l.ExecRotateTokens > 0 {
		return l.ExecRotateTokens, fmt.Sprintf("ключ exec_rotate_tokens машинного конфига %s", path)
	}
	if l != nil && rotateWarn(l, "exec_rotate_tokens") != "" {
		return execRotateDefault, fmt.Sprintf("умолчание agentctl, значение ключа exec_rotate_tokens в %s не годится", path)
	}
	return execRotateDefault, fmt.Sprintf("умолчание agentctl, ключа exec_rotate_tokens в %s нет", path)
}

// dispatcherRotate отдаёт порог ротации диспетчера и повод числа. Тот же
// порядок, что у исполнителя: ключ машинного конфига рядом с
// exec_rotate_tokens, умолчание agentctl без копии у потребителя.
func dispatcherRotate(l *layers, path string) (int, string) {
	if l != nil && l.DispatcherRotateTokens > 0 {
		return l.DispatcherRotateTokens, fmt.Sprintf("ключ dispatcher_rotate_tokens машинного конфига %s", path)
	}
	if l != nil && rotateWarn(l, "dispatcher_rotate_tokens") != "" {
		return dispatcherRotateDefault, fmt.Sprintf("умолчание agentctl, значение ключа dispatcher_rotate_tokens в %s не годится", path)
	}
	return dispatcherRotateDefault, fmt.Sprintf("умолчание agentctl, ключа dispatcher_rotate_tokens в %s нет", path)
}

// rotateWarn достаёт предупреждение про ключ порога, если слияние слоёв его
// оставило: битое значение ключа раскладку не роняет, но молчать о нём нельзя.
func rotateWarn(l *layers, key string) string {
	for _, w := range l.Warns {
		if strings.Contains(w, key) {
			return w
		}
	}
	return ""
}

// rotateOpts это то, чего ждёт команда rotate. Мерка контекста включается
// указанием сессии: self берёт имя из окружения самой сессии, строка
// называет сессию напрямую, и тот же случай меряет оболочка свой виток или
// свою живую голову.
type rotateOpts struct {
	oldSess string
	newSess string
	mark    bool
	// dispatch выбирает порог диспетчера вместо исполнительского: боль и
	// умолчание у них разные, и путать их молча нельзя.
	dispatch bool
	// measure включает мерку контекста сессии. Своя сессия называется
	// словом self, чужая или виток оболочки своим адресом.
	measure string
}

// cmdRotate печатает порог ротации двумя строками по образцу budget: машинное
// число первой, источник и повод второй. С ключом --mark дописывает строку
// журнала сессий о ротации: прежний и новый адрес, порог. С ключом мерки
// печатаются ещё две строки: контекст сессии и вердикт over|under, и вердикт
// этот читает оболочка, решая, пора ли пересоздавать диспетчера (DK-662).
func cmdRotate(start string, o rotateOpts) (string, error) {
	dir, err := harnessDir(start)
	if err != nil {
		return "", err
	}
	l, err := mergeLayers(dir, machineConfigPath(), projectConfigPath(start))
	if err != nil {
		return "", err
	}
	n, why := execRotate(l, machineConfigPath())
	warned := rotateWarn(l, "exec_rotate_tokens")
	if o.dispatch {
		n, why = dispatcherRotate(l, machineConfigPath())
		warned = rotateWarn(l, "dispatcher_rotate_tokens")
	}
	if warned != "" {
		why += "; " + warned
	}
	text := fmt.Sprintf("rotate: %d\n%s", n, why)
	if o.measure != "" {
		tokens, how, err := contextTokens(l, o.measure)
		if err != nil {
			return text, err
		}
		verdict := "under"
		if tokens > n {
			verdict = "over"
		}
		text += fmt.Sprintf("\ncontext: %d\nverdict: %s\n(%s)", tokens, verdict, how)
	}
	if o.mark {
		line, err := rotateMark(o.oldSess, o.newSess, n)
		if err != nil {
			return text, err
		}
		text += "\n" + line
	}
	return text, nil
}

// contextTokens это контекст сессии токенами: вес последнего запроса её
// главного потока. Стандартный счётчик (стенд) кладёт число в окружение,
// живой путь читает транскрипт сессии у харнеса.
func contextTokens(l *layers, sid string) (int, string, error) {
	if raw := strings.TrimSpace(os.Getenv(contextEnv)); raw != "" {
		tokens, err := strconv.Atoi(raw)
		if err != nil || tokens < 0 {
			return 0, "", fmt.Errorf("%s=%s это не число токенов", contextEnv, raw)
		}
		return tokens, fmt.Sprintf("счётчик стенда %s", contextEnv), nil
	}
	if sid == "self" {
		sid = os.Getenv(parentSessionEnv)
		if sid == "" {
			return 0, "", fmt.Errorf("сессия неизвестна: %s пуст, адрес своей сессии клиент кладёт сам", parentSessionEnv)
		}
	}
	path := transcriptOf(journalHomes(l), sid)
	if path == "" {
		return 0, "", fmt.Errorf("транскрипт сессии %s не найден в журналах харнеса", sid)
	}
	tokens, err := lastRequestTokens(path)
	if err != nil {
		return 0, "", err
	}
	return tokens, fmt.Sprintf("последний запрос транскрипта %s", path), nil
}

// usageRec это минимум записи журнала, который нужен для веса сессии. Вес
// тот же, что у счётчиков devkitctl: input плюс записанное и прочитанное
// префиксом кеша, это и есть то, что модель увидит на ближайшем вызове.
type usageRec struct {
	Type    string `json:"type"`
	Message *struct {
		Usage *struct {
			Input int `json:"input_tokens"`
			Write int `json:"cache_creation_input_tokens"`
			Read  int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// lastRequestTokens вес последнего запроса транскрипта сессии. Последний
// запрос с ненулевым весом и есть текущий контекст: убывание возможно только
// после компакции, и тут его считать нечем, оно же и цель ротации.
func lastRequestTokens(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("транскрипт сессии %s не открыт: %w", path, err)
	}
	defer f.Close()
	last := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	for sc.Scan() {
		var rec usageRec
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Type != "assistant" || rec.Message == nil || rec.Message.Usage == nil {
			continue
		}
		u := rec.Message.Usage
		if weight := u.Input + u.Write + u.Read; weight > 0 {
			last = weight
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("транскрипт сессии %s не прочитан: %w", path, err)
	}
	if last == 0 {
		return 0, fmt.Errorf("в транскрипте сессии %s нет запросов с usage", path)
	}
	return last, nil
}

// rotateMark пишет строку журнала сессий о ротации и возвращает её.
// Формат именованными полями как у turn-mark.py: слово хода «ротация»,
// повод несёт порог, новая сессия отдельным полем «новая». Отказ записи
// возвращается вызывающему: след механики теряется громко, а не молча
// (замечание 7 ревью DK-1312).
func rotateMark(oldSess, newSess string, threshold int) (string, error) {
	ts := time.Now().Format("2006-01-02T15:04:05")
	line := fmt.Sprintf("%s сессия %s ход ротация повод порог-%d дерево - новая %s\n",
		ts, dashlessField(oldSess), threshold, dashlessField(newSess))
	logPath := turnsLogPath()
	if logPath == "" {
		return line, fmt.Errorf("журнал сессий не найден: DEVKIT_TURN_MARK_LOG пуст, HOME не дал пути")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return line, fmt.Errorf("журнал сессий %s не создан: %w", logPath, err)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return line, fmt.Errorf("журнал сессий %s не открыт: %w", logPath, err)
	}
	defer f.Close()
	if _, err := fmt.Fprint(f, line); err != nil {
		return line, fmt.Errorf("строка ротации не записана в %s: %w", logPath, err)
	}
	return line, nil
}

// turnsLogPath отдаёт путь журнала отметок ходов: тот же файл, что пишет
// turn-mark.py. Переменная DEVKIT_TURN_MARK_LOG перебивает умолчание.
func turnsLogPath() string {
	if p := os.Getenv("DEVKIT_TURN_MARK_LOG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".devkit", "turns.log")
}

// dashlessField пишет пустое поле дефисом, как у turn-mark.py.
func dashlessField(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "-"
	}
	return v
}
