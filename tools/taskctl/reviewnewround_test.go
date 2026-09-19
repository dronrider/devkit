package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReviewCleanNewRoundCLI: `review clean ... --new-round` заявляет новый
// круг ревью, когда сравнить с прежним вердиктом нечем (DK-812, замечания
// 4-6 второго круга ревью: приём verdictIntroducedAt, искавший основание по
// тексту строки, убран совсем, а сравнение по sha остаётся автоматическим).
// На ecd27dc9 такого ключа не было вовсе: второй `review clean` после
// headless-вердикта не проходил никаким способом, флаг не разбирался бы.
//
// Тест гоняется через собранный CLI (exec.Command), а не вызовом
// cmdReviewClean напрямую: с этой правки у функции другая сигнатура (добавлен
// параметр newRound), и regcheck, переносящий на старый код только тестовые
// файлы, не смог бы собрать пакет со старой cmdReviewClean и новым вызовом.
// Через бинарь регрессия проверяется без этой зависимости.
func TestReviewCleanNewRoundCLI(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	run := func(args ...string) (string, error) {
		out, err := exec.Command("go", append([]string{"run", ".", "-C", root}, args...)...).CombinedOutput()
		return string(out), err
	}
	if _, err := run("review", "level", "XR-005", "1", "неопределённость 0, не критично"); err != nil {
		t.Fatal(err)
	}
	// Headless-вердикт первого круга, как выглядел файл задачи до DK-812.
	got := readTaskFile(t, root, "XR-005")
	edited := strings.Replace(got, "## Сценарий проверки",
		"- Вердикт: без замечаний. первый круг чист\n\n## Сценарий проверки", 1)
	if edited == got {
		t.Fatalf("маркер раздела не нашёлся в файле задачи:\n%s", got)
	}
	if err := os.WriteFile(taskFileAbs(root, "XR-005"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "docs/tasks/XR-005.md")
	gitOut(t, root, "commit", "-q", "-m", "docs(tasks): XR-005 ревью: первый круг чист")

	if err := os.WriteFile(filepath.Join(root, "code.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "code.txt")
	gitOut(t, root, "commit", "-q", "-m", "fix: XR-005 правка после красного слияния")

	if _, err := run("review", "level", "XR-005", "1", "второй круг, неопределённость 0"); err != nil {
		t.Fatal(err)
	}

	if out, err := run("review", "clean", "XR-005", "второй круг чист", "--new-round"); err != nil {
		t.Fatalf("--new-round должен провести законный второй круг: %v\n%s", err, out)
	} else if !strings.Contains(out, "вердикт без замечаний до") {
		t.Fatalf("вывод не назвал sha второго круга: %s", out)
	}
}
