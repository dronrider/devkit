package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
	"github.com/dronrider/devkit/internal/trace"
)

// Группа draft prepared читает и правит подготовленные задачи записи
// (DK-1469): носитель это prepared-файл docs/tasks/prepared/<ID черновика>.md,
// запись черновика остаётся материалом без машинного блока. Старый блок
// «Предложения разбора» читается ридером совместимости, а после первого
// сохранения срезается из записи вовсе.

type preparedView struct {
	Conclusion string              `json:"conclusion"`
	Ready      bool                `json:"ready"`
	Reason     string              `json:"reason"`
	ID         string              `json:"id"`
	Snapshot   string              `json:"snapshot"`
	Basis      string              `json:"basis"`
	State      draftproposal.State `json:"state"`
	// Assigned несёт выданные номера новых задач этого сохранения: присланный
	// ключ на зарезервированный номер (DK-1564, решение 6). Пустеет на чтении
	// и правке прежних задач: их номер уже стоит в ключе.
	Assigned map[string]string `json:"assigned,omitempty"`
}

// readPrepared читает запись и её prepared-состояние одним заходом: сначала
// prepared-файл, при его отсутствии старый блок самой записи.
func readPrepared(root, id string) ([]byte, draftproposal.State, error) {
	data, err := os.ReadFile(draftPath(root, id))
	if err != nil {
		return nil, draftproposal.State{}, err
	}
	state, err := draftproposal.Load(root, id, data)
	if err != nil {
		return nil, draftproposal.State{}, err
	}
	return data, state, nil
}

func preparedViewOf(root, id string, record []byte, state draftproposal.State) (preparedView, error) {
	basis, err := preparedBasis(root, record, state)
	if err != nil {
		return preparedView{}, err
	}
	snapshot, err := preparedSnapshot(state, record)
	if err != nil {
		return preparedView{}, err
	}
	effective, reason := resolvePrepared(root, id, record, state)
	conclusion, err := preparedConclusion(root, effective)
	if err != nil {
		return preparedView{}, err
	}
	return preparedView{ID: id, Snapshot: snapshot, Basis: basis, State: effective, Ready: reason == "", Reason: reason, Conclusion: conclusion}, nil
}

func cmdDraftPrepared(root, id string) (string, error) {
	l, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer l.Close()
	drafts, err := loadDraftsWithHistory(root, true)
	if err != nil {
		return "", err
	}
	if findDraft(drafts, id) == nil {
		return "", fmt.Errorf("%s: черновик не найден", id)
	}
	record, state, err := readPrepared(root, id)
	if err != nil {
		return "", err
	}
	view, err := preparedViewOf(root, id, record, state)
	if err != nil {
		return "", err
	}
	return marshal(view)
}

// preparedChanges собирает список правок транзакции: состояние пишется в
// prepared-файл, а старый блок срезается из записи, если он там ещё был.
func preparedChanges(root, id string, record []byte, state draftproposal.State) ([]drafttx.Change, error) {
	render, err := draftproposal.RenderPrepared(state)
	if err != nil {
		return nil, err
	}
	before, err := drafttx.Read(draftproposal.PreparedPath(root, id))
	if err != nil {
		return nil, err
	}
	changes := []drafttx.Change{{Path: draftproposal.PreparedRel(id), Before: before, After: drafttx.Image{Exists: true, Data: render, Mode: 0644}}}
	material, had, err := draftproposal.Material(record)
	if err != nil {
		return nil, err
	}
	if had {
		recBefore, err := drafttx.Read(draftPath(root, id))
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(recBefore.Data, record) {
			return nil, fmt.Errorf("%s: запись изменилась, откройте её снова", id)
		}
		changes = append(changes, drafttx.Change{Path: draftRel(id), Before: recBefore, After: drafttx.Image{Exists: true, Data: material, Mode: recBefore.Mode}})
	}
	return changes, nil
}

// normalizePreparedBarrier сохраняет пояснение старой карточки в описании.
// Нормализация идёт на копии до создания строки, снимок согласования прежний.
func normalizePreparedBarrier(p draftproposal.Proposal) (draftproposal.Proposal, error) {
	if p.Outcome != "add" || p.Barrier == "" {
		return p, nil
	}
	key, note, described := strings.Cut(p.Barrier, ":")
	key = strings.TrimSpace(key)
	if _, ok := trace.BarrierWanted[key]; !ok {
		return p, fmt.Errorf("%s: барьер %q не из закрытого списка (глаза, доступ, необратимость, секрет, согласие, событие)", p.ID, p.Barrier)
	}
	if described && strings.TrimSpace(note) != "" {
		paragraph := "Барьер приёмки: " + p.Barrier
		text := strings.TrimSpace(p.Text)
		if strings.HasPrefix(text, "# ") {
			heading, rest, _ := strings.Cut(text, "\n")
			p.Text = heading + "\n\n" + paragraph + "\n\n" + rest
		} else {
			p.Text = paragraph + "\n\n" + text
		}
	}
	p.Barrier = key
	return p, nil
}

func cmdDraftPreparedSave(root, id, base string, input []byte) (string, error) {
	l, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer l.Close()
	drafts, err := loadDraftsWithHistory(root, true)
	if err != nil {
		return "", err
	}
	if findDraft(drafts, id) == nil {
		return "", fmt.Errorf("%s: черновик не найден", id)
	}
	record, old, err := readPrepared(root, id)
	if err != nil {
		return "", err
	}
	if old.State == "history" {
		return "", fmt.Errorf("%s: разбор завершён и сохранён в истории; новый разбор запишите отдельным черновиком", id)
	}
	snapshot, err := preparedSnapshot(old, record)
	if err != nil {
		return "", err
	}
	if base == "" || base != snapshot {
		return "", fmt.Errorf("%s: запись изменилась, откройте её снова", id)
	}
	var incoming struct {
		Proposals      []draftproposal.Proposal `json:"proposals"`
		Conclusion     *string                  `json:"conclusion"`
		Analysis       *string                  `json:"analysis"`
		Complete       bool                     `json:"complete"`
		ReplacePending bool                     `json:"replace_pending"`
	}
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&incoming); err != nil {
		return "", err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("лишние данные после JSON")
	}
	// Новый разбор дописывает задачи, прежние сохраняются. Нерешённую задачу
	// можно править только следующей редакцией, решённую нельзя. Новая задача
	// получает полный номер доски сразу: он виден в списке и составе до
	// подтверждения, подтверждение заводит строку тем же номером, а повторное
	// сохранение номера не меняет (DK-1564, решение 6). Присланный ключ новой
	// задачи только связывает вызов с ответом через assigned.
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	if incoming.ReplacePending {
		retired := map[string]bool{}
		for _, key := range old.RetiredIDs {
			retired[key] = true
		}
		keep := map[string]bool{}
		for _, p := range incoming.Proposals {
			keep[p.ID] = true
		}
		retained := make([]draftproposal.Proposal, 0, len(old.Proposals))
		for _, p := range old.Proposals {
			if p.Status != "" || keep[p.ID] {
				retained = append(retained, p)
			} else {
				for _, key := range []string{p.ID, p.BoardID} {
					if key != "" && !retired[key] {
						old.RetiredIDs = append(old.RetiredIDs, key)
						retired[key] = true
					}
				}
			}
		}
		old.Proposals = retained
		sort.Strings(old.RetiredIDs)
	}
	index := map[string]int{}
	for i, p := range old.Proposals {
		index[p.ID] = i
	}
	seen := map[string]bool{}
	assigned := map[string]string{}
	for _, p := range incoming.Proposals {
		p, err = normalizePreparedBarrier(p)
		if err != nil {
			return "", err
		}
		if seen[p.ID] {
			return "", fmt.Errorf("повтор задачи %s", p.ID)
		}
		seen[p.ID] = true
		if p.Status != "" || p.Result != nil {
			return "", fmt.Errorf("%s: решение нельзя задать при записи разбора", p.ID)
		}
		if i, ok := index[p.ID]; ok {
			previous := old.Proposals[i]
			if previous.Status != "" {
				return "", fmt.Errorf("%s: решённую задачу изменить нельзя", p.ID)
			}
			if p.Revision != previous.Revision+1 || reflect.DeepEqual(p, previous) {
				return "", fmt.Errorf("%s: нужна следующая редакция", p.ID)
			}
			if p.BoardID != "" && p.BoardID != previous.BoardID {
				return "", fmt.Errorf("%s: закреплённый номер менять нельзя", p.ID)
			}
			p.BoardID = previous.BoardID
			old.Proposals[i] = p
		} else {
			for _, key := range old.RetiredIDs {
				if key == p.ID {
					return "", fmt.Errorf("%s: ключ прежнего результата занят", p.ID)
				}
			}
			if p.Revision != 1 {
				return "", fmt.Errorf("%s: первая редакция должна быть 1", p.ID)
			}
			sent := p.ID
			if p.Outcome != "add" {
				if p.BoardID != "" || idRe.MatchString(p.ID) {
					return "", fmt.Errorf("%s: номер доски выдаётся только новой задаче", p.ID)
				}
				index[p.ID] = len(old.Proposals)
				old.Proposals = append(old.Proposals, p)
				continue
			}
			id, err := allocID(root, b, arch, drafts)
			if err != nil {
				return "", err
			}
			p.ID = id
			p.BoardID = id
			assigned[sent] = id
			index[p.ID] = len(old.Proposals)
			old.Proposals = append(old.Proposals, p)
		}
	}
	// Унаследованная нерешённая задача без номера получает его этим же
	// сохранением: номер обязан стоять до применения решения, а не выдаваться
	// подтверждением (DK-1564, решение 6). Решённые задачи не трогаются: их
	// номер уже принадлежит строке или отказу. Закрепление идёт после проверки
	// присланных задач: отказ валидации не сжигает номера в общей области.
	for i := range old.Proposals {
		p := &old.Proposals[i]
		if p.BoardID != "" || p.Status != "" || p.Outcome != "add" {
			continue
		}
		id, err := allocID(root, b, arch, drafts)
		if err != nil {
			return "", err
		}
		p.BoardID = id
	}
	// Ссылка «после» приходит присланным ключом соседней задачи того же
	// сохранения: выданный номер встаёт и в ссылку, иначе подтверждение не
	// узнает ссылку при поиске предшественницы.
	for i := range old.Proposals {
		for j, ref := range old.Proposals[i].After {
			if id, ok := assigned[ref]; ok {
				old.Proposals[i].After[j] = id
			}
		}
	}
	if len(incoming.Proposals) > 0 {
		old.State = "active"
	}
	if incoming.Conclusion != nil {
		old.Conclusion = strings.TrimSpace(*incoming.Conclusion)
	}
	if incoming.Analysis != nil {
		old.Analysis = *incoming.Analysis
	}
	old.PackRevision++
	old.PreparedSource = ""
	if incoming.Complete {
		if err = validatePreparedBundle(root, id, record, old); err != nil {
			return "", err
		}
		old.PreparedSource, err = preparedMaterialDigest(record)
		if err != nil {
			return "", err
		}
	}
	changes, err := preparedChanges(root, id, record, old)
	if err != nil {
		return "", err
	}
	if err = l.Apply(root, changes); err != nil {
		return "", err
	}
	material, _, err := draftproposal.Material(record)
	if err != nil {
		return "", err
	}
	view, err := preparedViewOf(root, id, material, old)
	if err != nil {
		return "", err
	}
	view.Assigned = assigned
	return marshal(view)
}

func cmdDraftPreparedReject(root, id, task, base, reason string) (string, error) {
	return "", fmt.Errorf("подтверждается весь разбор черновика; обсудите или исправьте задачи и подтвердите разбор целиком")
}
