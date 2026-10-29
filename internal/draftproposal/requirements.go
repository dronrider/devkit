package draftproposal

import (
	"fmt"
	"strings"

	"github.com/dronrider/devkit/internal/drafttypes"
	"github.com/dronrider/devkit/internal/team"
)

// Required validates fields supplied by the draft type before staging an add.
// Existing taskctl commands still validate the board, form and field formats.
func Required(def drafttypes.Definition, p Proposal, members []team.Participant) error {
	values := map[string]string{"title": p.Title, "body": p.Text, "type": p.BoardType, "rank": p.Rank, "size": p.Size, "accept": p.Accept, "assignee": p.Assignee, "due": p.Due, "control": p.Control}
	for _, field := range def.Required {
		if strings.TrimSpace(values[field]) == "" {
			return fmt.Errorf("%s: для типа %s необходимо поле %s", p.ID, def.Name, field)
		}
	}
	if def.Name == "agreement" {
		person, ok := team.Find(members, p.Assignee)
		if !ok || person.Kind != team.Human || !person.HasRole(team.RoleExec) {
			return fmt.Errorf("%s: договорённости нужен человек с ролью exec", p.ID)
		}
	}
	if def.Name == "milestone" && strings.TrimSpace(p.Due) == "" {
		return fmt.Errorf("%s: для вехи необходим срок", p.ID)
	}
	return nil
}
