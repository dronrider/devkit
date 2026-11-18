// Package modelcatalog хранит замены моделей для нового выбора чата.
// Идентификаторы в истории разговоров остаются прежними.
package modelcatalog

// Current возвращает актуальный ID модели. Неизвестные модели сохраняются.
func Current(model string) string {
	switch model {
	case "gpt-6-sol", "gpt-6.1-sol":
		return "gpt-6.2-sol"
	case "gpt-6-astra", "gpt-6.1-astra":
		return "gpt-6.2-astra"
	default:
		return model
	}
}
