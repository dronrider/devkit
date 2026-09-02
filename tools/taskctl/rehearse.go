package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/freshtree"
	"github.com/dronrider/devkit/internal/taskform"
)

// RehearseParams это ключи обкатки сценария: шаги, названные руками, и предел
// на шаг. Пустые Steps значат «взять шаги из файла задачи».
type RehearseParams struct {
	Steps   []string
	Timeout time.Duration
	Now     time.Time
}

// stepLimit это предел на один шаг обкатки. Сценарий пишется под живую
// проверку, шаг в нём быстрый, а вставший шаг иначе держит заход исполнителя до
// упора и возвращает работу незакоммиченной.
const stepLimit = 10 * time.Minute

// cmdRehearse обкатывает сценарий задачи в свежем дереве и собранном
// окружении: то, что зелено в прогретом чекауте с живым HOME, на чужой машине
// краснеет, и всплывает это уже после слияния (DK-138, DK-641). Шаги берутся из
// ограждённых блоков раздела «Сценарий проверки» либо приходят ключом --step,
// каждый гоняется своим прогоном, вывод целиком ложится в раздел «Проверка», а
// отметка обкатки открывает ворота перевода в Check. Красный шаг отметки не
// ставит, но вывод пишет: разбирать провал надо по реальному выводу, а не по
// пересказу.
func cmdRehearse(root, id string, p RehearseParams) (string, error) {
	path := taskFilePath(root, id)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: файла задачи нет (%s): завести «taskctl file %s» и описать сценарий", id, path, id)
	}
	doc := string(data)
	steps := p.Steps
	if len(steps) == 0 {
		text, found, _ := readSectionFromPath(path, scenarioSection)
		if !found {
			return "", fmt.Errorf("%s: в docs/tasks/%s.md нет раздела «Сценарий проверки», обкатывать нечего: описать шаги (%s) и повторить", id, id, taskform.Doc)
		}
		steps = scenarioSteps(text)
	}
	if len(steps) == 0 {
		return "", fmt.Errorf("%s: в разделе «Сценарий проверки» нет ограждённого блока с командами: положить команды шагов в блок ```sh либо назвать их ключом --step \"команда\" (по ключу на шаг)", id)
	}
	sha, err := gitRevParse(root, "HEAD")
	if err != nil {
		return "", fmt.Errorf("%s: не узнать коммит для свежего дерева: %v", id, err)
	}
	sha = strings.TrimSpace(sha)
	run, err := freshtree.Start(root, sha, "taskctl-rehearse-")
	if err != nil {
		return "", fmt.Errorf("%s: свежее дерево на %s не выложилось: %v", id, shortSha(sha), err)
	}
	defer run.Cleanup()
	limit := p.Timeout
	if limit <= 0 {
		limit = stepLimit
	}
	var runs []stepRun
	failed := 0
	for _, s := range steps {
		r := runStep(run, s, limit)
		runs = append(runs, r)
		if !r.ok {
			failed++
		}
	}
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	// Отпечаток берётся с того текста сценария, который обкатка только что
	// прогнала: раздел «Проверка» в него не входит, и запись прогона отметку
	// не отменяет.
	block := rehearsalBlock(runs, sha, taskform.ScenarioPrint(doc), now, failed, len(run.Tools))
	// Прошлая запись обкатки уносится: повтор прогона иначе копит в «Проверке»
	// блоки шагов и отметки, и какая из них относится к нынешнему коммиту,
	// глазами уже не видно.
	body := taskform.InsertIntoSection(dropRehearsal(doc), taskform.Verification, block...)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("%s: вывод обкатки не записан в %s: %v", id, path, err)
	}
	rel := "docs/tasks/" + id + ".md"
	if failed > 0 {
		return "", fmt.Errorf("%s: обкатка красная, шагов упало %d из %d, вывод лежит в %s разделом «Проверка»: разбирать провал и повторить rehearse, отметки для move check нет",
			id, failed, len(runs), rel)
	}
	return fmt.Sprintf("%s: обкатка зелёная, шагов %d, свежее дерево %s, вывод и отметка в %s; дальше taskctl move %s check",
		id, len(runs), shortSha(sha), rel, id), nil
}

// stepRun это один прогнанный шаг: команда, её вывод и исход.
type stepRun struct {
	cmd  string
	out  string
	ok   bool
	note string
}

// runStep гоняет шаг в свежем дереве и с собранным окружением. Убивается шаг
// по группе процессов: команда сценария поднимает и служебные процессы
// (сервер, хук), и убитый в одиночку шелл оставил бы их держать дерево. Отказ
// по нехватке команды дополняется разбором прогона: в чистом окружении «command
// not found» это первое, обо что спотыкается сценарий, и по одному коду
// возврата не видно ни имени команды, ни того, где её искали.
func runStep(run *freshtree.Run, step string, limit time.Duration) stepRun {
	cmd := exec.Command("sh", "-c", step)
	cmd.Dir, cmd.Env = run.Tree, run.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := runCollect(cmd, limit)
	switch {
	case err == nil:
		return stepRun{cmd: step, out: out, ok: true}
	case strings.HasPrefix(err.Error(), "предел"):
		return stepRun{cmd: step, out: out, note: err.Error()}
	default:
		note := "провал: " + err.Error()
		if why := run.Diagnose(out); why != "" {
			note += ", " + why
		}
		return stepRun{cmd: step, out: out, note: note}
	}
}

// runCollect ждёт команду до предела и отдаёт её вывод. Буфер читается только
// после Wait: до него в него льют горутины exec.Cmd, и чтение вперёд Wait это
// гонка, а не просто неполный вывод.
func runCollect(cmd *exec.Cmd, limit time.Duration) (string, error) {
	var buf strings.Builder
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case err := <-done:
		return buf.String(), err
	case <-timer.C:
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return buf.String(), fmt.Errorf("предел %s пройден, шаг убит", limit)
	}
}

// rehearsalBlock складывает запись обкатки для раздела «Проверка»: строка
// отметки, по которой ворота узнают прогон, и вывод каждого шага целиком.
// Вывод идёт ограждённым блоком, иначе разметка файла задачи ломается о первую
// же строку вывода, начатую решёткой или маркером списка.
func rehearsalBlock(runs []stepRun, sha, print string, now time.Time, failed, tools int) []string {
	verdict := "все зелёные"
	if failed > 0 {
		verdict = fmt.Sprintf("красных %d", failed)
	}
	// Число собранных утилит стоит в отметке рядом с коммитом: по нему видно,
	// что шагам досталась ветка, а не установленные бинари машины.
	out := []string{"", fmt.Sprintf("%s %s, свежее дерево %s, сценарий %s, временный HOME, утилит дерева %d, шагов %d, %s.",
		taskform.RehearsalNote, now.Format("2006-01-02 15:04"), shortSha(sha), print, tools, len(runs), verdict)}
	if failed > 0 {
		// Отметка ворот ставится только зелёной обкаткой, а вывод красной всё
		// равно нужен глазами: без него разбирать провал нечем.
		out[1] = fmt.Sprintf("%s %s, свежее дерево %s, сценарий %s, утилит дерева %d, шагов %d, %s, ворота закрыты.",
			taskform.RehearsalFailNote, now.Format("2006-01-02 15:04"), shortSha(sha), print, tools, len(runs), verdict)
	}
	for i, r := range runs {
		mark := "зелёный"
		if !r.ok {
			mark = r.note
		}
		out = append(out, "", fmt.Sprintf("Шаг %d (%s):", i+1, mark), "", "```console",
			"$ "+r.cmd)
		out = append(out, strings.Split(strings.TrimRight(r.out, "\n"), "\n")...)
		out = append(out, "```")
	}
	return out
}

// shellLangs это языки ограждённого блока, чьё содержимое считается командами.
// Блок без языка и блок чужого языка (json, text, вывод примера) обкатка не
// трогает: сценарий сплошь и рядом иллюстрируют конфигом или куском вывода, и
// прогонять такое как команды значит стрелять наугад.
var shellLangs = map[string]bool{
	"sh": true, "bash": true, "shell": true, "zsh": true, "console": true,
}

// scenarioSteps собирает команды шагов из shell-блоков раздела «Сценарий
// проверки». Проза раздела командой не считается: в обратных кавычках там
// ходят пути, имена файлов и куски объяснения. Строка приглашения («$ »)
// отбрасывается, комментарии и пустые строки пропускаются.
func scenarioSteps(text string) []string {
	var steps []string
	fence, take := "", false
	for _, ln := range strings.Split(text, "\n") {
		t := strings.TrimSpace(ln)
		if mark := fenceMarker(t); mark != "" {
			if fence == "" {
				fence, take = mark, shellLangs[strings.ToLower(strings.TrimSpace(t[len(mark):]))]
				continue
			}
			if mark[0] == fence[0] && len(mark) >= len(fence) && strings.TrimSpace(t[len(mark):]) == "" {
				fence, take = "", false
			}
			continue
		}
		if !take || t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		steps = append(steps, strings.TrimPrefix(t, "$ "))
	}
	return steps
}

// fenceMarker отдаёт ограждение в начале строки (три и больше знака ` или ~) и
// пустую строку, когда строка не ограждение.
func fenceMarker(line string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(line) && line[n] == c {
			n++
		}
		if n >= 3 {
			return line[:n]
		}
	}
	return ""
}

// dropRehearsal уносит из файла задачи прошлую запись обкатки: строку отметки
// (зачтённую или красную) и всё, что писала та же команда, то есть заголовки
// «Шаг N» с их ограждёнными блоками. Первая посторонняя строка запись кончает,
// так что вложенный руками вывод и проза раздела остаются на месте.
func dropRehearsal(doc string) string {
	lines := strings.Split(doc, "\n")
	mask, _ := taskform.FenceMask(lines)
	var out []string
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if mask[i] || !(strings.HasPrefix(t, taskform.RehearsalNote) || strings.HasPrefix(t, taskform.RehearsalFailNote)) {
			out = append(out, lines[i])
			continue
		}
		for i++; i < len(lines); i++ {
			t := strings.TrimSpace(lines[i])
			if t == "" || strings.HasPrefix(t, "Шаг ") {
				continue
			}
			if mark := fenceMarker(t); mark != "" && mask[i] {
				for i++; i < len(lines) && mask[i]; i++ {
				}
				i--
				continue
			}
			break
		}
		i--
		// Хвостовые пустые строки съедены вместе с записью, а разделу нужна
		// пустая строка перед следующим заголовком: её вернёт вставка новой
		// записи, здесь же остаётся не приклеить прозу к заголовку раздела.
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// shortSha режет коммит до двенадцати знаков: столько же показывают отказы
// слияния, и глазами они сходятся.
func shortSha(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// stepList это повторяемый ключ --step: шаг на ключ, порядок ключей это
// порядок шагов. Своя реализация flag.Value нужна потому, что flag держит одно
// значение на имя, а сценарий это список.
type stepList []string

func (l *stepList) String() string { return strings.Join(*l, "; ") }

func (l *stepList) Set(v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("пустой шаг")
	}
	*l = append(*l, v)
	return nil
}
