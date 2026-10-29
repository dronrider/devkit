package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/dronrider/devkit/internal/draftmeta"
)

// cmdSafeBoardList requires the CLI board lock and returns only safe titles.
func cmdSafeBoardList(root, sect string, debtOnly, jsonOut bool) (string, error) {
	board, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", fmt.Errorf("доска не прочиталась для безопасного списка")
	}
	keys := []string{SectInProgress, SectCheck, SectBacklog, SectBlocked}
	if sect != "" {
		key := normalizeStatus(sect)
		if _, ok := board.Sects[key]; !ok {
			return "", fmt.Errorf("неизвестная секция %q", sect)
		}
		keys = []string{key}
	}
	result := jsonBoard{Prefix: board.Prefix, Sections: []jsonSection{}}
	var lines []string
	for _, key := range keys {
		section := jsonSection{Key: key, Title: sectTitles[key], Rows: []jsonRow{}}
		lines = append(lines, sectTitles[key])
		for _, row := range board.Sects[key].Rows {
			if debtOnly && !isDebt(row.Title) {
				continue
			}
			title, kind := "ждёт локальную модель", ""
			if draftmeta.GuardRecordLocked(root, row.ID) == nil {
				base, _, _, _, _, _, _, _, _, _, _ := splitTitle(row.Title)
				title = strings.TrimSpace(base)
				kind = row.Type
			}
			section.Rows = append(section.Rows, jsonRow{ID: row.ID, Title: title, Type: kind})
			lines = append(lines, fmt.Sprintf("%s: %s", row.ID, title))
		}
		result.Sections = append(result.Sections, section)
	}
	if sect == "" && !debtOnly {
		archive, err := LoadArchive(archivePath(root))
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("архив не прочитался для безопасного списка")
		}
		section := jsonSection{Key: "archive", Title: "Архив", Rows: []jsonRow{}}
		lines = append(lines, section.Title)
		if archive != nil {
			for _, row := range archive.Rows {
				title, kind := "ждёт локальную модель", ""
				if draftmeta.GuardRecordLocked(root, row.ID) == nil {
					title, kind = row.Cells[1], row.Cells[2]
				}
				section.Rows = append(section.Rows, jsonRow{ID: row.ID, Title: title, Type: kind})
				lines = append(lines, fmt.Sprintf("%s: %s", row.ID, title))
			}
		}
		result.Sections = append(result.Sections, section)
	}
	if jsonOut {
		return marshal(result)
	}
	return strings.Join(lines, "\n"), nil
}
