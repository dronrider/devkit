package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Реплика в мёртвое окно (DK-1399). Разговор задачи переживает сессию, и
// безадресная строка лежит во входе, пока не придёт хозяин: припаркованную
// строку будит сторожок, разработку и проверку поднимает лестница, а строке
// Backlog, закрытой задаче и черновику ждать некому. Ход чужой сессии отбивал
// такие строки подхватом на каждом инструменте, по пятьсот отказов за день, и
// человек не узнавал о них ничего: ни окна, ни записи, ни зова.
//
// Правило адресации стоит у отправителя. Ручка реплики и ответы на вопросы не
// кладут строку в доставку, когда адресат мёртв: живой строке доски работа
// ещё вернётся (лестница подъёма, обход ждущих), а мёртвой адресат реплику уже
// не заберёт, и она откладывается записью в файл задачи с причиной. Лежащие
// строки, написанные до правила или обогнавшие смерть адресата, подметает
// дозор этой же правкой: он сводит вечный повтор за один свой шаг.

// chatHoldStep это шаг дозора мёртвых окон. Порядок тот же, каким ходит сторож
// цикла цели, и повтор доставки сходится за один шаг.
const chatHoldStep = 5 * time.Minute

// holdStamp это метка отложенной реплики, тем же форматом, каким подписываются
// строки разговора (lineStamp в internal/chat).
const holdStamp = "2006-01-02 15:04"

// deadAddresseeWhy называет причину, по которой реплика в разговор задачи не
// доедет, либо отвечает пустой строкой, когда адресат жив или работу поднимут.
// Мерка та же, что у самой доставки: безадресную строку забирает ведущая
// сессия (taskHead), строку In progress и Check поднимает по реплике лестница
// подъёма, строку Blocked будит сторожок, и лежащая реплика у всех них это
// рабочий механизм пробуждения, а не мусор. Остальным запись не достанется
// никогда: строки Backlog и Done ходом не поднимаются, закрытой задачи на
// доске нет, а черновик ведёт живая сессия либо никто.
func (s *server) deadAddresseeWhy(projPath, id string, rows map[string]boardRow) string {
	// Запись с назначением остаётся адресатом доставки (LLD DK-1422, решение
	// 4): строку из входа забирает подхват следующего носителя, и дозору её
	// подметать незачем. Мера стоит раньше разбора строки: закрытая запись и
	// черновик с назначением ждут носителя той же строкой входа, что и живая
	// работа, а прежде ручка отказывала им на разборе строки доски.
	if s.mainChatBind(id).Chat != "" {
		return ""
	}
	if row, onBoard := rows[id]; onBoard {
		if replySection(row.Sect) || row.Sect == "blocked" {
			return ""
		}
		if s.taskHead(projPath, id) != "" {
			return ""
		}
		return fmt.Sprintf("строка стоит в %s, работу по ней никто не ведёт", row.Section)
	}
	if s.taskHead(projPath, id) != "" {
		return ""
	}
	if draftHere(projPath, id) {
		return "у черновика нет строки доски, и сессию, которая его вела, за входом уже не ждут"
	}
	if arch, closed := s.archiveRows(projPath)[id]; closed {
		return fmt.Sprintf("задача закрыта %s и лежит в архиве", arch.Closed)
	}
	return "строки на доске проекта нет"
}

// askAddressee меряет адресата розданного вопроса: ответ ложится безадресной
// строкой во вход дерева вопроса, и забрать его способен только ведущий задачу.
// Возврат это причина мёртвого окна и стояние задачи на доске: строке Backlog
// ответ откладывается в файл, а черновику и закрытой задаче отказывается.
// Задача берётся из признака, а при пустом поле из имени разговора: вопрос
// розданной работы зовёт задачу по имени, и пустое поле там значит лишь то,
// что спрашивала личная сессия, мера которой не задача, а разговор.
func (s *server) askAddressee(h handedAsk) (why string, onBoard bool) {
	id := strings.TrimSpace(h.Ask.Task)
	if id == "" {
		if tail, ok := strings.CutPrefix(h.Name, chat.TaskName("")); ok {
			id = strings.ToUpper(tail)
		}
	}
	if !goalIDRe.MatchString(id) {
		return "", false
	}
	rows, err := s.boardRowsOf(h.Tree)
	if err != nil {
		return "", false
	}
	_, onBoard = rows[id]
	return s.deadAddresseeWhy(h.Tree, id, rows), onBoard
}

// waiterAlive меряет живость ждущего: ответ розданного вопроса ложится
// безадресной строкой во вход дерева вопроса, и забирает её ход сессии,
// названной в признаке. Пока та сессия жива, ворота мёртвого окна не
// срабатывают, какой бы ни была строка доски: вопрос розданной работы живёт
// разговором, а не строкой. Мера это свежесть транскрипта, тот же рубеж, что
// у чужого окна в списке чатов: ждущий ход сидит в ожидании и подписывает
// транскрипт сам. Транскрипт ждущего ищется в двух деревьях, отвечающем и
// дерева вопроса: исполнитель спрашивает из своего дерева, диспетчер из
// основного. Делегат своего транскрипта может не иметь, и живым его считает
// родитель из реестра: розданный субагент живёт, пока идёт раздавший разговор.
func (s *server) waiterAlive(projPath, tree, waiter string) bool {
	if waiter == "" {
		return false
	}
	if s.chatForeignLive(projPath, waiter) || s.chatForeignLive(tree, waiter) {
		return true
	}
	parent := s.binds()[waiter].Parent
	return parent != "" && (s.chatForeignLive(projPath, parent) || s.chatForeignLive(tree, parent))
}

// projectAt ищет проект по пути дерева: розданный вопрос лежит в дереве
// проекта, и меру его адресата спрашивают у доски именно этого проекта.
func (s *server) projectAt(path string) *Project {
	projects, _ := s.projects()
	for i := range projects {
		if filepath.Clean(projects[i].Path) == filepath.Clean(path) {
			return &projects[i]
		}
	}
	return nil
}

// boardRowsOf читает строки живой доски проекта. Чтение с памятью ответа то же,
// что у ручек (projectBoard), и отказ оставляется вызывающему: мера адресата
// без доски не складывается.
func (s *server) boardRowsOf(projectPath string) (map[string]boardRow, error) {
	raw, err := s.projectBoard(projectPath)
	if err != nil {
		return nil, err
	}
	return parseBoardRows(raw)
}

// holdFile ищет файл, в который откладывается реплика мёртвого окна: файл
// задачи, черновика или закрытой задачи в архиве, тем же порядком, каким их
// находит экран задачи. Файла нет, и записи нет: исход остаётся строкой
// журнала, потому что вносить текст задачи, у которой файла нет, некуда.
func (s *server) holdFile(projectPath, id string) (abs, rel string, ok bool) {
	for _, cand := range []struct{ abs, rel string }{
		{filepath.Join(projectPath, "docs", "tasks", id+".md"), "tasks/" + id + ".md"},
		{filepath.Join(projectPath, "docs", "tasks", "drafts", id+".md"), "tasks/drafts/" + id + ".md"},
	} {
		if _, err := os.Stat(cand.abs); err == nil {
			return cand.abs, cand.rel, true
		}
	}
	if arch, closed := s.archiveRows(projectPath)[id]; closed && arch.Link != "" {
		abs := filepath.Join(projectPath, "docs", filepath.FromSlash(arch.Link))
		if _, err := os.Stat(abs); err == nil {
			return abs, arch.Link, true
		}
	}
	return "", "", false
}

// holdInbox вписывает отложенные реплики в раздел «Входящие» файла задачи.
// Раздел тот же, каким живой цели пишутся реплики человека, и будущий
// исполнитель читает его вместе с постановкой. Раскладку держит одна
// addInboxLine: у формы файла задачи раздела нет, и заводится он тем же
// писателем и на том же месте, что и у цели, а не второй раскладкой в конец
// файла. Повтор укладки не удваивает запись: сходимость идёт после записи
// файла, и не сошедшийся вход повторил бы её следующим проходом.
func holdInbox(path string, lines []string, why string, now time.Time) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	var fresh []string
	for _, l := range lines {
		said := chat.Said(l)
		if strings.Contains(text, "«"+said+"»") {
			continue
		}
		word := "Реплика"
		if to := chat.Addressee(l); to != "" {
			word = "Реплика сессии " + to
		}
		fresh = append(fresh, fmt.Sprintf("- %s отложено из чата задачи: %s. %s: «%s»",
			now.Format(holdStamp), why, word, said))
	}
	if len(fresh) == 0 {
		return nil
	}
	for _, l := range fresh {
		text = addInboxLine(text, l)
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// chatHoldWrite вписывает реплики мёртвого окна в файл задачи с причиной.
// Первый возврат это «легло в файл», второй «сошлось»: файла нет, и исход
// остаётся строкой журнала; файл был, а записи не вышло, и строки из входа
// убирать нельзя, они остались единственной копией сказанного.
func (s *server) chatHoldWrite(p *Project, id string, lines []string, why string) (filed, ok bool) {
	abs, rel, has := s.holdFile(p.Path, id)
	if !has {
		return false, true
	}
	if err := holdInbox(abs, lines, why, s.now()); err != nil {
		s.logf("реплики задачи %s в %s не отложились в файл: %v", id, p.Name, err)
		return false, false
	}
	if note := commitDocs(p.Path, boardCommitMsg(id, "реплики мёртвого окна отложены в файл задачи"), rel); note != "" {
		s.logf("%s", note)
	}
	return true, true
}

// chatHoldSweep это один проход дозора мёртвых окон: лежащие строки разговоров
// задач, чьи адресаты мертвы, откладываются в файлы задач с причиной. Живые
// входы не трогаются: у припаркованных и разрабатываемых строк лежащая реплика
// это механизм пробуждения, и мера адресата у дозора та же, что у доставки.
func (s *server) chatHoldSweep() {
	projects, _ := s.projects()
	for _, p := range projects {
		ents, err := os.ReadDir(chat.Root(p.Path))
		if err != nil {
			continue
		}
		order, lying := []string{}, map[string][]string{}
		for _, e := range ents {
			name, hit := strings.CutSuffix(e.Name(), chat.Suffix)
			if !hit || e.IsDir() || !strings.HasPrefix(name, "task-") {
				continue
			}
			id := strings.ToUpper(strings.TrimPrefix(name, "task-"))
			if !goalIDRe.MatchString(id) {
				continue
			}
			if lines := chat.ReadLines(chat.Path(p.Path, name)); len(lines) > 0 {
				order = append(order, id)
				lying[id] = lines
			}
		}
		if len(order) == 0 {
			continue
		}
		rows, err := s.boardRowsOf(p.Path)
		if err != nil {
			s.logf("дозор мёртвых окон в %s: доска не прочиталась: %v", p.Name, err)
			continue
		}
		for _, id := range order {
			why := s.deadAddresseeWhy(p.Path, id, rows)
			if why == "" {
				continue
			}
			filed, ok := s.chatHoldWrite(&p, id, lying[id], why)
			if !ok {
				continue
			}
			if _, err := chat.TakeLines(p.Path, chat.TaskName(id), lying[id]); err != nil {
				s.logf("реплики задачи %s в %s отложены, но из входа не ушли: %v", id, p.Name, err)
				continue
			}
			if filed {
				s.logf("реплики чата %s в %s отложены в файл задачи: %s", chat.TaskName(id), p.Name, why)
				s.saidMark(saidTaskKey(id), fmt.Sprintf("реплики отложены в файл задачи: %s", why))
			} else {
				s.logf("реплики чата %s в %s сняты без записи: файла задачи нет, исход остался в журнале: %s",
					chat.TaskName(id), p.Name, why)
				s.saidMark(saidTaskKey(id), fmt.Sprintf("реплики сняты без записи, файла задачи нет: %s", why))
			}
			s.holdSay(&p, id, why, filed)
		}
	}
}

// chatHoldKeeper это сам дозор: первый проход идёт стартом, выкат подметает
// накопившееся сразу, дальше проходы идут шагом.
func (s *server) chatHoldKeeper(stop <-chan struct{}) {
	s.chatHoldSweep()
	t := time.NewTicker(chatHoldStep)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.chatHoldSweep()
		}
	}
}

// holdSay зовёт человека к отложенной реплике: дозор сошёл, повторов больше
// нет, и случай должен доехать до человека, а не остаться строкой журнала.
// Зов идёт из дозора, а не из ручки: человек, только что писавший реплику,
// читает ответ ручки, и второй баннер об одном молчании хуже одного. Канал тот
// же, что у осиротевшей задачи (nolead.go), и повод тот же: работа не идёт.
func (s *server) holdSay(p *Project, id, why string, filed bool) {
	np := notifierPath(s.cfg.Roots)
	if np == "" {
		s.logf("реплика задаче %s отложена: %s", id, notifierMissing)
		return
	}
	title := fmt.Sprintf("%s: реплика задаче %s отложена в файл задачи", p.Name, id)
	tail := "реплика отложена записью в файл задачи и ждёт исполнителя там"
	if !filed {
		title = fmt.Sprintf("%s: реплика задаче %s снята без записи", p.Name, id)
		tail = "файла задачи нет, реплика осталась строкой журнала дашборда"
	}
	body := fmt.Sprintf("%s. Ответа адресат не ждёт, %s.", strings.TrimRight(why, ". "), tail)
	cmd := exec.Command("python3", np, "--reason", noLeadReason,
		"--task", id, "--project", p.Name, title, body)
	cmd.Dir = p.Path
	if out, err := cmd.CombinedOutput(); err != nil {
		s.logf("реплика задаче %s отложена, а уведомление не ушло: %v (%s)",
			id, err, strings.TrimSpace(string(out)))
	}
}
