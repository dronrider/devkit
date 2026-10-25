package taskhead

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"
)

func TestWorkingSeesBeatNotPid(t *testing.T) {
	// Working это взгляд снаружи на замок: дашборд и сторож по нему отличают
	// идущую работу от замерзшего окна (DK-1454). Живой pid тут не довод:
	// работой замок доказывает свежая отметка жизни.
	path := LockPath(t.TempDir(), "DK-1")
	if Working(path) {
		t.Fatal("пустой замок назван работой")
	}
	owner := liveProc(t)
	if err := Take(path, owner); err != nil {
		t.Fatal(err)
	}
	freshBeat(t, path)
	if !Working(path) {
		t.Fatal("живой владелец со свежей отметкой не назван работой")
	}
	staleBeat(t, path, -3*time.Hour)
	if Working(path) {
		t.Fatal("замерзший владелец назван работой")
	}
}

func TestStaleBeatIsOneForLockAndShell(t *testing.T) {
	// Одно число у замка и у оболочки конвейера. Оболочка касается beat на
	// признаках жизни и сама снимает молчащее окно двумя окнами молчания, а
	// замок этим рубежом подхватывает замерзшую саму оболочку. Разойдись
	// числа, замок снимал бы окно, которое оболочка ещё собиралась дождаться,
	// либо держал замок вечно. Сторож пары как у
	// TestReplyOrderIsOneForShellAndCallers.
	src, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(TaskRunRel)))
	if err != nil {
		t.Fatal(err)
	}
	stale := regexp.MustCompile(`(?m)^STALE_AFTER = (\d+)`).FindSubmatch(src)
	if stale == nil {
		t.Fatalf("в %s нет константы STALE_AFTER, оболочка порог замерзания не знает", TaskRunRel)
	}
	secs, err := strconv.Atoi(string(stale[1]))
	if err != nil || time.Duration(secs)*time.Second != lockStale {
		t.Fatalf("порог замка %s, а у оболочки %s с: одно число разошлось", lockStale, stale[1])
	}
	mute := regexp.MustCompile(`(?m)^MUTE_SECONDS = (\d+)`).FindSubmatch(src)
	muteSecs, err := strconv.Atoi(string(mute[1]))
	if err != nil {
		t.Fatal(err)
	}
	if lockStale < 2*time.Duration(muteSecs)*time.Second {
		t.Fatalf("порог замка %s ближе собственного срока оболочки (два окна молчания по %d с)",
			lockStale, muteSecs)
	}
}
