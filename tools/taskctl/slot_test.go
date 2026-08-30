package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupSlot кладёт доску, архив и файлы задач без git: тесты, которым нужен
// возраст строк или ветки, заводят репозиторий сами, как у progress-тестов.
func setupSlot(t *testing.T, board string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		boardPath(root):   board,
		archivePath(root): fixtureArchive,
		filepath.Join(root, "docs", "tasks", "XR-005.md"): "# XR-005\n" + fixtureScenario + fixtureVerification,
	}
	for p, content := range files {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// gitBoard заводит репозиторий и коммитит доску: возраст строк из boardTimes
// честен только на чистой доске, поэтому файл обязан попасть в коммит.
func gitBoard(t *testing.T, root string) {
	t.Helper()
	gitOut(t, root, "init", "-q", "-b", "main")
	gitOut(t, root, "config", "user.email", "test@test")
	gitOut(t, root, "config", "user.name", "test")
	gitOut(t, root, "add", "docs/TASKS.md")
	gitOut(t, root, "commit", "-q", "-m", "init")
}

// fakeTmux подменяет tmux скриптом с данным списком сессий: занятость дерева
// читается по tmux-сессиям конвейера, и стенд держит их сам.
func fakeTmux(t *testing.T, sessions string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '" + sessions + "\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// travelTime сдвигает часы утилиты вперёд: возраст строк и коммитов считается
// от timeNow, и стенд не ждёт настоящего простоя.
func travelTime(t *testing.T, d time.Duration) {
	t.Helper()
	was := timeNow
	timeNow = func() time.Time { return was().Add(d) }
	t.Cleanup(func() { timeNow = was })
}

// TestSlotScore сводит формулу с ручным счётом по строкам «Проверки на живых
// строках» цели DK-395. Три строки одной полосы P2: свежая S с рангом 44
// (W = 0.8, score 55.0), M после ревью с рубежом 0.89, простоявшая сутки
// (W = 0.11 + 0.55 = 0.66, score 62.1, берём первой), она же остановленная
// в середине кода с рубежом 0.35 после суток простоя (W = 0.65 + 0.55 = 1.20,
// score 34.2, уступает свежей S). Ранги тут с бонусом за дешевизну (DK-428):
// S и M поднимают собственную сумму на 2 и на 1. Машина даёт те же числа тем же порядком.
func TestSlotScore(t *testing.T) {
	root := setupSlot(t, `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-321 | Остановленная в середине кода | task | P2 | 40 (25+8+1+0+6) | M | - |

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-320 | После ревью, ждёт приёмки | task | P2 | 40 (25+8+1+0+6) | M | - |

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-331 | Свежая | task | P2 | 42 (25+9+1+0+7) | S | - |
| XR-330 | Мелкая, полоса ниже | task | P3 | 10 (0+4+0+0+6) | S | - |

## Blocked

Нет.
`)
	withSections(t, root, "XR-320", "Выкат")
	gitBoard(t, root)
	branchWithCommit(t, root, "xr-321")
	// main уезжает на один коммит: справка расхождения обязана его назвать.
	if err := os.WriteFile(filepath.Join(root, "drift.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "drift.txt")
	gitOut(t, root, "commit", "-q", "-m", "main ушёл")
	travelTime(t, 25*time.Hour)

	out, err := cmdSlot(root, batchDefaultLimit, "slot")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"slot: XR-320",
		"P2:",
		"  1. XR-320 (M, R 41): score 62.1, W 0.66 (C 1.00, F 0.89 «ревью пройдено, слито и выкачено», V 0.55), простой 25.0 ч",
		"  2. XR-331 (S, R 44): score 55.0, W 0.80 (C 0.80, F 0.00 «строка заведена», V 0.00)",
		"  3. XR-321 (M, R 41): score 34.2, W 1.20 (C 1.00, F 0.35 «первый коммит кода», V 0.55), простой 25.0 ч, main ушёл на 1",
		"P3:",
		"  4. XR-330 (S, R 12): score 15.0, W 0.80 (C 0.80, F 0.00 «строка заведена», V 0.00)",
	}, "\n")
	if out != want {
		t.Fatalf("порядок кандидатов:\n%s\n\nожидал:\n%s", out, want)
	}
}

// TestSlotReturnPay держит границу платы за возврат: час простоя даёт
// 0.3 + 0.02 = 0.32, дальше плата растёт до потолка 0.55. В файле цели
// в примере про час простоя плата названа 0.35, а формула даёт 0.32: утилита
// считает по формуле, и расхождение названо в файле задачи DK-404.
func TestSlotReturnPay(t *testing.T) {
	root := setupSlot(t, `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-321 | Остановленная в середине кода | task | P2 | 40 (25+8+1+0+6) | M | - |

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Blocked

Нет.
`)
	gitBoard(t, root)
	branchWithCommit(t, root, "xr-321")

	travelTime(t, time.Hour)
	out, err := cmdSlot(root, batchDefaultLimit, "slot")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "score 42.3, W 0.97 (C 1.00, F 0.35 «первый коммит кода», V 0.32)") {
		t.Fatalf("час простоя не дал V 0.32 и W 0.97:\n%s", out)
	}
}

// TestSlotGates гоняет все ворота по одной строке на ворота: отказ приходит
// по первой непройденной, и строка попадает ровно в одну группу. Человек
// недоступен из-за припаркованного вопроса старше часа, занятость дерева
// видна tmux-сессией, лимит пачки исчерпан строкей, взятой раньше по порядку
// обхода.
func TestSlotGates(t *testing.T) {
	root := setupSlot(t, `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-104 | Пользовательская [приёмка: user] | task | P3 | 10 (0+5+1+0+4) | S | - |
| XR-100 | Занята живой работой | task | P2 | 40 (25+8+1+0+6) | M | - |

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-101 | Ждёт соседа [после XR-108] | task | P3 | 16 (0+10+1+0+5) | S | - |
| XR-102 | Мутная | task | P3 | 15 (0+8+2+0+5) | S | - |
| XR-103 | Без цены | task | P3 | 12 (0+6+1+0+5) | - | - |
| XR-105 | Годная | task | P3 | 12 (0+6+1+0+5) | S | [tasks/XR-105.md](tasks/XR-105.md) |
| XR-109 | Годная, но лишняя | task | P3 | 20 (0+10+1+0+9) | S | - |
| XR-110 | Та же ссылка, что у XR-105 | task | P3 | 10 (0+5+0+0+5) | S | [tasks/XR-105.md](tasks/XR-105.md) |

## Blocked

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-106 | Припаркована вопросом [блок: вопрос: ждём ответа] | task | P3 | 9 (0+4+0+0+5) | S | - |
| XR-107 | Припаркована окружением [блок: окружение: ждём среду] | task | P3 | 9 (0+4+0+0+5) | S | - |
| XR-108 | Внешний блокер [блок: роутер DE недоступен] | task | P3 | 9 (0+4+0+0+5) | S | - |
`)
	gitBoard(t, root)
	fakeTmux(t, "task-XR-100\\t1\\t1000")
	travelTime(t, 2*time.Hour)

	out, err := cmdSlot(root, 1, "slot")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"slot: XR-105",
		"P3:",
		"  1. XR-105 (S, R 14): score 17.5, W 0.80 (C 0.80, F 0.00 «строка заведена», V 0.00)",
		"отказы:",
		"  человек недоступен, приёмка вида user: XR-104",
		"  дерево занято живой работой: XR-100",
		"  незакрытая предпосылка: XR-101 (ждут XR-108)",
		"  неопределённость выше 1: XR-102 (2)",
		"  цена вне курса S/M/L: XR-103 (-)",
		"  потолок пачки 1 исчерпан: XR-109",
		"  ссылка та же, что у отобранной: XR-110 (как у XR-105)",
		"  висящий вопрос без ответа: XR-106",
		"  окружение задачи неготово: XR-107",
		"  внешний блокер: XR-108 (роутер DE недоступен)",
	}, "\n")
	if out != want {
		t.Fatalf("ворота:\n%s\n\nожидал:\n%s", out, want)
	}
}

// TestSlotTreeCeiling: потолок незакрытых деревьев жжёт свежие старты и
// перебивается окружением для стендов. Деревьев в стенде нет, поэтому потолок
// 0 это чистый отказ всем строкам без ветки.
func TestSlotTreeCeiling(t *testing.T) {
	root := setupSlot(t, `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-201 | Первая | task | P3 | 20 (0+10+1+0+9) | S | - |
| XR-202 | Вторая | task | P3 | 10 (0+5+0+0+5) | S | - |

## Blocked

Нет.
`)
	t.Setenv(slotTreesEnv, "0")
	out, err := cmdSlot(root, batchDefaultLimit, "slot")
	if err != nil {
		t.Fatal(err)
	}
	want := "slot: -\nотказы:\n  потолок деревьев 0 исчерпан: XR-201, XR-202"
	if out != want {
		t.Fatalf("потолок деревьев:\n%s\n\nожидал:\n%s", out, want)
	}
	// Без окружения потолок это лимит плюс запас, и строки проходят.
	t.Setenv(slotTreesEnv, "")
	if out, err = cmdSlot(root, batchDefaultLimit, "slot"); err != nil ||
		!strings.HasPrefix(out, "slot: XR-201\n") {
		t.Fatalf("без потолка кандидаты не прошли:\n%s (%v)", out, err)
	}
}

// TestSlotTreeCeilingWaiters: сработавший потолок деревьев называет не только
// число, но и то, что разобрать (LLD DK-400, решение 5): припаркованные строки
// с живой веткой идут строкой «ждут разбора» с причиной парковки. Осиротевшая
// строка в In progress не ждёт, её берёт сам слот, а припаркованная без ветки
// деревья не держит и в разбор не попадает.
func TestSlotTreeCeilingWaiters(t *testing.T) {
	root := setupSlot(t, `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-303 | Осиротевшая | task | P3 | 9 (0+4+0+0+5) | S | - |

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-201 | Первая | task | P3 | 20 (0+10+1+0+9) | S | - |
| XR-202 | Вторая | task | P3 | 10 (0+5+0+0+5) | S | - |

## Blocked

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-301 | Парковка вопросом [блок: вопрос: ждём ответа] | task | P3 | 9 (0+4+0+0+5) | S | - |
| XR-302 | Парковка окружением [блок: окружение: ждём среду] | task | P3 | 9 (0+4+0+0+5) | S | - |
| XR-304 | Внешний блокер [блок: роутер DE недоступен] | task | P3 | 9 (0+4+0+0+5) | S | - |
| XR-305 | Вопрос без ветки [блок: вопрос: ждём ответа] | task | P3 | 9 (0+4+0+0+5) | S | - |
`)
	gitBoard(t, root)
	for _, id := range []string{"xr-301", "xr-302", "xr-304", "xr-303"} {
		branchWithCommit(t, root, id)
	}
	t.Setenv(slotTreesEnv, "0")
	out, err := cmdSlot(root, batchDefaultLimit, "slot")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"slot: XR-303",
		"P3:",
		"  1. XR-303 (S, R 11): score 14.5, W 0.76 (C 0.80, F 0.35 «первый коммит кода», V 0.24), простой 0.0 ч",
		"отказы:",
		"  потолок деревьев 0 исчерпан: XR-201, XR-202",
		"  висящий вопрос без ответа: XR-301, XR-305",
		"  окружение задачи неготово: XR-302",
		"  внешний блокер: XR-304 (роутер DE недоступен)",
		"ждут разбора: XR-301 (вопрос), XR-302 (окружение), XR-304 (блокер)",
	}, "\n")
	if out != want {
		t.Fatalf("потолок деревьев с разбором:\n%s\n\nожидал:\n%s", out, want)
	}
}

// TestSlotQuotaAndResource: курс квоты считает цену L в 5.5 пункта против
// 1.2 слота, а лимит 0 из agentctl budget это не ошибка, а отказ всем строкам
// с названной причиной.
func TestSlotQuotaAndResource(t *testing.T) {
	root := setupSlot(t, `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-205 | Большая | task | P3 | 11 (0+5+1+0+5) | L | - |

## Blocked

Нет.
`)
	out, err := cmdSlot(root, batchDefaultLimit, "quota")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "XR-205 (L, R 11): score 2.0, W 5.50 (C 5.50, F 0.00 «строка заведена», V 0.00)") {
		t.Fatalf("курс квоты не посчитан по 5.5:\n%s", out)
	}
	out, err = cmdSlot(root, 0, "slot")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "slot: -\nотказы:\n  нет квоты на пачку: XR-205") {
		t.Fatalf("лимит 0 это отказ, а не тишина:\n%s", out)
	}
	if _, err = cmdSlot(root, batchDefaultLimit, "tokens"); err == nil ||
		!strings.Contains(err.Error(), "slot или quota") {
		t.Fatalf("чужой ресурс не отбился: %v", err)
	}
}

// TestSlotEmptyBoard: пустая доска печатает честное «slot: -», а не пустоту:
// молчание планировщика неотличимо от сломанного вывода.
func TestSlotEmptyBoard(t *testing.T) {
	root := setupSlot(t, `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

Нет.

## Blocked

Нет.
`)
	out, err := cmdSlot(root, batchDefaultLimit, "slot")
	if err != nil {
		t.Fatal(err)
	}
	if out != "slot: -" {
		t.Fatalf("пустая доска: %q, ожидал %q", out, "slot: -")
	}
}
