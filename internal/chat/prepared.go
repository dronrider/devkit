package chat

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/draftproposal"
)

// PreparedQuestion это машинная часть вопроса согласования подготовленных
// задач (DK-1469, решение 4): текст и опции у вопроса обычные, а здесь лежат
// снимки и показанный список задач, по которому проверяется ответ.
type PreparedQuestion struct {
	Version int              `json:"version"`
	Tree    string           `json:"tree"`
	Anchor  string           `json:"anchor"`
	Sources []PreparedSource `json:"sources"`
}

type PreparedSource struct {
	Conclusion string                   `json:"conclusion,omitempty"`
	Revision   int                      `json:"revision,omitempty"`
	Source     string                   `json:"source"`
	Basis      string                   `json:"basis"`
	Proposals  []draftproposal.Proposal `json:"proposals"`
}

func (q PreparedQuestion) Validate() error {
	if q.Version != 1 || !filepath.IsAbs(q.Tree) || filepath.Clean(q.Tree) != q.Tree || strings.TrimSpace(q.Anchor) == "" || len(q.Sources) == 0 {
		return fmt.Errorf("неверный вопрос согласования задач")
	}
	seen := map[string]bool{}
	anchor := false
	for _, s := range q.Sources {
		if strings.TrimSpace(s.Source) == "" || strings.TrimSpace(s.Basis) == "" || seen[s.Source] || len(s.Proposals) == 0 {
			return fmt.Errorf("неверный источник подготовленных задач %q", s.Source)
		}
		seen[s.Source] = true
		if s.Source == q.Anchor {
			anchor = true
		}
		ids := map[string]bool{}
		for _, p := range s.Proposals {
			if strings.TrimSpace(p.ID) == "" || p.Revision < 1 || ids[p.ID] || p.Status != "" || p.Result != nil {
				return fmt.Errorf("неверная показанная задача %q", p.ID)
			}
			if strings.TrimSpace(p.Text) != "" {
				return fmt.Errorf("тело задачи %s не входит в вопрос", p.ID)
			}
			switch p.Outcome {
			case "add", "attach", "defer", "drop":
			default:
				return fmt.Errorf("неверный исход %q", p.Outcome)
			}
			ids[p.ID] = true
		}
	}
	if !anchor {
		return fmt.Errorf("нет исходной записи вопроса")
	}
	return nil
}

// CheckPreparedAnswer compares the request to the question, never to a fresh view.
func CheckPreparedAnswer(q PreparedQuestion, tree string, r draftproposal.ConfirmRequest) error {
	if err := q.Validate(); err != nil {
		return err
	}
	if tree != q.Tree || len(r.Decisions) == 0 {
		return fmt.Errorf("нет показанного контекста подготовленных задач")
	}
	sources := map[string]PreparedSource{}
	for _, s := range q.Sources {
		sources[s.Source] = s
	}
	snaps := map[string]string{}
	for _, s := range r.Snapshots {
		shown, ok := sources[s.Source]
		if !ok || s.Digest != shown.Basis || s.Revision != shown.Revision || snaps[s.Source] != "" {
			return fmt.Errorf("снимок %s не показан", s.Source)
		}
		snaps[s.Source] = s.Digest
	}
	used := map[string]bool{}
	decisions := map[string]bool{}
	for _, d := range r.Decisions {
		s, ok := sources[d.Source]
		if !ok || snaps[d.Source] == "" {
			return fmt.Errorf("источник %s не показан", d.Source)
		}
		key := d.Source + "\x00" + d.Proposal
		if decisions[key] {
			return fmt.Errorf("повтор решения %s", d.Proposal)
		}
		decisions[key] = true
		found := false
		for _, p := range s.Proposals {
			if p.ID == d.Proposal && p.Revision == d.Revision {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("редакция %s не показана", d.Proposal)
		}
		if d.Action != "accept" {
			return fmt.Errorf("подтвердите весь разбор; отказ обсудите в чате")
		}
		used[d.Source] = true
	}
	if len(used) != len(snaps) {
		return fmt.Errorf("лишний снимок")
	}
	for _, s := range q.Sources {
		if !used[s.Source] {
			continue
		}
		for _, p := range s.Proposals {
			if !decisions[s.Source+"\x00"+p.ID] {
				return fmt.Errorf("%s: подтвердите весь разбор целиком", s.Source)
			}
		}
	}
	return nil
}
