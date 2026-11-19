// Package modelcatalog хранит замены моделей для новых выборов и запусков.
// Идентификаторы в истории разговоров остаются прежними.
package modelcatalog

var replacements = [...]struct{ previous, current string }{
	{"gpt-6-sol", "gpt-6.2-sol"},
	{"gpt-6.1-sol", "gpt-6.2-sol"},
	{"gpt-6-astra", "gpt-6.2-astra"},
	{"gpt-6.1-astra", "gpt-6.2-astra"},
}

// Current возвращает актуальный ID модели. Неизвестные модели сохраняются.
func Current(model string) string {
	for _, replacement := range replacements {
		if replacement.previous == model {
			return replacement.current
		}
	}
	return model
}

// Replaces возвращает прежние ID для восстановления предпочтения браузера.
func Replaces(model string) []string {
	var previous []string
	for _, replacement := range replacements {
		if replacement.current == model {
			previous = append(previous, replacement.previous)
		}
	}
	return previous
}
