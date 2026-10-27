package taskform

import (
	"strings"
	"testing"
)

func TestScenarioCardsStayWithinOwnHeading(t *testing.T) {
	good := "### СЦ-1\nСитуация: путь\nДействие: выбрать\nРезультат: виден\nПольза: сверить\nРекомендация: включить, нужен результат\nОграничение: один путь\nИсточник: запись\n"
	scenarios := []UserScenario{{ID: "СЦ-1"}}
	if err := ValidateScenarioCards(good, scenarios); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"", strings.Replace(good, "Польза: сверить\n", "", 1),
		"Польза: вне карточки\n" + strings.Replace(good, "Польза: сверить\n", "", 1),
		strings.Replace(good, "Польза: сверить\n", "", 1) + "### СЦ-2\nПольза: другой сценарий\n",
		strings.Replace(good, "Польза: сверить\n", "```text\nПольза: только пример\n```\n", 1),
		"```text\n" + good + "```\n",
		strings.Replace(good, "Польза: сверить\n", "#### Примечание\nПольза: другой блок\n", 1),
	} {
		if err := ValidateScenarioCards(bad, scenarios); err == nil {
			t.Fatalf("чужое или отсутствующее поле принято:\n%s", bad)
		}
	}
	if err := ValidateScenarioCards(good+good, scenarios); err == nil {
		t.Fatal("повтор карточки принят")
	}
}
