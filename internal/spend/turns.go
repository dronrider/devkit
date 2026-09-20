package spend

// Ходы с моментами и перечень потоков машины. Свод по этапам (DK-912) читает
// поток целиком и спрашивает только сумму: у субагента весь поток это один
// этап одной задачи. Сквозные статьи (DK-913) режут поток головной сессии по
// времени: диспетчер пачки за один заход ведёт несколько задач, и ход
// достаётся той, чьё событие журнала агентов стоит перед ним.

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Turn это один ход потока: момент и расход. Ход это запрос к модели, а не
// запись журнала, дедуп тот же, что у Read.
type Turn struct {
	At    time.Time
	Usage Usage
}

// stamped это запись журнала с моментом. Тело реплики сюда не приезжает: свод
// отдаёт наружу суммы, и держать текст в памяти незачем.
type stamped struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	UUID      string `json:"uuid"`
	Stamp     string `json:"timestamp"`
	Message   struct {
		Usage *struct {
			Input      int `json:"input_tokens"`
			Output     int `json:"output_tokens"`
			CacheRead  int `json:"cache_read_input_tokens"`
			CacheWrite int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ReadTurns читает ходы потока с моментами. Момент харнес пишет по UTC, а
// читателю нужен местный: статьи режутся по датам, которые называет человек.
func ReadTurns(path string) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readTurns(f)
}

func readTurns(r io.Reader) ([]Turn, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	seen := map[string]bool{}
	var out []Turn
	for {
		line, err := readLine(br)
		// Дешёвый отсев раньше разбора json: запись хода ассистента это одна
		// строка из многих, а остальные (реплики, вызовы инструментов, их
		// ответы) стоят дороже всего журнала вместе взятого.
		if strings.Contains(line, `"assistant"`) && strings.Contains(line, `"usage"`) {
			var t stamped
			if json.Unmarshal([]byte(line), &t) == nil && t.Type == "assistant" && t.Message.Usage != nil {
				key := t.RequestID
				if key == "" {
					key = t.UUID
				}
				u := t.Message.Usage
				if !seen[key] && u.Input+u.Output+u.CacheRead+u.CacheWrite > 0 {
					seen[key] = true
					at, _ := time.Parse(time.RFC3339, t.Stamp)
					out = append(out, Turn{At: at.Local(), Usage: Usage{
						Turns: 1, Output: u.Output, Input: u.Input,
						CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
					}})
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
}

// Stream это поток журналов харнеса: у головной сессии назван её UUID, у
// работы субагента номером работы.
type Stream struct {
	Path    string
	Session string // головная сессия, пусто у потока субагента
	Work    string // работа субагента, пусто у головного потока
}

// Streams перечисляет потоки дома, тронутые не раньше since. Отсев по времени
// правки идёт до чтения: журналов на машине полтора гигабайта, а ход, попавший
// в срез, лежит только в файле, дописанном позже начала среза. Нулевое since
// это весь дом.
func Streams(home string, since time.Time) []Stream {
	if home == "" {
		return nil
	}
	var out []Stream
	heads, _ := filepath.Glob(filepath.Join(Dir(home), "*", "*.jsonl"))
	kids, _ := filepath.Glob(filepath.Join(Dir(home), "*", "*", "subagents", "agent-*.jsonl"))
	for _, p := range heads {
		if !touched(p, since) {
			continue
		}
		out = append(out, Stream{Path: p, Session: strings.TrimSuffix(filepath.Base(p), ".jsonl")})
	}
	for _, p := range kids {
		if !touched(p, since) {
			continue
		}
		work := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "agent-"), ".jsonl")
		out = append(out, Stream{Path: p, Work: work})
	}
	sortStreams(out)
	return out
}

// touched отвечает, мог ли файл нести ход после since.
func touched(path string, since time.Time) bool {
	if since.IsZero() {
		return true
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !fi.ModTime().Before(since)
}

// sortStreams держит порядок потоков устойчивым: по нему печатается свод.
func sortStreams(s []Stream) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Path < s[j-1].Path; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
