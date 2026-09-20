package stage

import "testing"

// TestExecutorMissing (DK-911, замечание ревью): запись хука без модели
// узнаётся как этап без исполнителя, а рукописная строка и запись с моделью
// нет.
func TestExecutorMissing(t *testing.T) {
	nameless := Stage{Kind: Dev, Note: "субагент по определению exec-high, работа d1"}
	if note, missing := ExecutorMissing(nil, []Stage{nameless}); !missing || note != nameless.Note {
		t.Fatalf("этап без модели не узнан: %q %v", note, missing)
	}
	named := Stage{Kind: Dev, Note: "субагент opus/high по определению exec-high, работа d2"}
	if _, missing := ExecutorMissing(nil, []Stage{nameless, named}); missing {
		t.Fatal("свежий этап с моделью принят за безымянный")
	}
	if _, missing := ExecutorMissing([]string{"- Разработка: субагент по определению exec-low, работа x, 2026-09-20 10:00-11:00."}, nil); !missing {
		t.Fatal("строка «Хода работы» без модели не узнана")
	}
	if _, missing := ExecutorMissing([]string{"- Разработка: правил сам, 2026-09-20 10:00-11:00."}, nil); missing {
		t.Fatal("рукописная строка принята за запись хука без модели")
	}
	if _, missing := ExecutorMissing([]string{"- Ревью: субагент по определению review-high, работа r, 2026-09-20 10:00."}, nil); missing {
		t.Fatal("ревью без модели принято за этап исполнителя")
	}
}
