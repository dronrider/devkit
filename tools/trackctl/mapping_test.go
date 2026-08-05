package main

import (
	"reflect"
	"strings"
	"testing"
)

// Чтение таблицы: любой статус из списка секции даёт эту секцию, чужая
// детализация складывается в одну секцию доски.
func TestSectionOfReadsSynonyms(t *testing.T) {
	setupEnv(t, contourFile, bindingFile)
	c, err := loadContour("corp")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status, sect string
		known        bool
	}{
		{"Open", sectBacklog, true},
		{"Backlog", sectBacklog, true},
		{"In Progress", sectInProgress, true},
		{"Development", sectInProgress, true},
		{"Review", sectCheck, true},
		{"Testing", sectCheck, true},
		{"On Hold", sectBlocked, true},
		{"Closed", sectDone, true},
		{"in progress", sectInProgress, true},
		{"Waiting for QA", "", false},
	} {
		sect, ok := c.sectionOf(tc.status)
		if ok != tc.known || sect != tc.sect {
			t.Fatalf("статус %q: жду (%q, %v), вижу (%q, %v)", tc.status, tc.sect, tc.known, sect, ok)
		}
	}
}

// Запись: целевой статус это первый элемент списка, остальные синонимы только
// для чтения. Мутация «целевым взят не первый элемент» ловится здесь.
func TestTargetStatusIsFirst(t *testing.T) {
	setupEnv(t, contourFile, bindingFile)
	c, err := loadContour("corp")
	if err != nil {
		t.Fatal(err)
	}
	for sect, want := range map[string]string{
		sectBacklog:    "Open",
		sectInProgress: "In Progress",
		sectCheck:      "Review",
		sectBlocked:    "Blocked",
	} {
		got, err := c.targetStatus(sect)
		if err != nil || got != want {
			t.Fatalf("секция %s: жду %q, вижу %q (%v)", sect, want, got, err)
		}
	}
	if _, err := c.targetStatus(sectDone); err == nil || !strings.Contains(err.Error(), "не пишет") {
		t.Fatalf("в done автоматика не пишет никогда: %v", err)
	}
}

func TestTargetStatusMissingSection(t *testing.T) {
	setupEnv(t, strings.Replace(contourFile, `blocked = ["Blocked", "On Hold"]`, "", 1), bindingFile)
	c, err := loadContour("corp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.targetStatus(sectBlocked); err == nil || !strings.Contains(err.Error(), "не расписал секцию") {
		t.Fatalf("нерасписанная секция это отказ с именем секции: %v", err)
	}
}

// {user} разворачивается в пользователя контура, а поля чужих секций и поля,
// которых в секции нет, не подставляются.
func TestFieldsForSubstitutesUser(t *testing.T) {
	setupEnv(t, contourFile, bindingFile)
	c, err := loadContour("corp")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"assignee": "ivanov", "reason": "плановая работа"}
	if got := c.fieldsFor(sectInProgress); !reflect.DeepEqual(got, want) {
		t.Fatalf("поля перехода: %v", got)
	}
	if got := c.fieldsFor(sectBlocked); got != nil {
		t.Fatalf("у секции без [fields_*] полей нет вовсе, вижу %v", got)
	}
}
