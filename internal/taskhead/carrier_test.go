package taskhead

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// DK-1562: headless-ступень без носителя отступает без спавна оболочки.
// Стенд без tmux и без сессии зовущего, запрос без заказа: раньше лестница
// спавнила оболочку в никуда и ждала принятия замка, теперь зовёт человека.
func TestHeadlessBacksOffWithoutCarrier(t *testing.T) {
	s := newStand(t, false, true)
	// Адрес сессии зовущего тут не при чём: стенд играет голое окружение,
	// и переменная живой сессии машины в него протекать не должна.
	t.Setenv(sessions.SessionEnv, "")
	// Связка DEVKIT_GROOM_* режет подъём раньше лестницы (GuardGroomRun)
	// и протекает в стенд из живой сессии машины. Без гашения вместо
	// проверки носителя приходит ошибка разбора.
	t.Setenv("DEVKIT_GROOM_SOURCE", "")
	t.Setenv("DEVKIT_GROOM_MODE", "")
	q := s.req()
	q.Adopt = 500 * time.Millisecond
	res, err := Raise(q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Code == CodeRaised || res.Rung == RungHeadless {
		t.Fatalf("голова поднялась без носителя:\n%s", s.why(res))
	}
	if _, err := os.Stat(filepath.Join(s.home, "runner.log")); !os.IsNotExist(err) {
		t.Fatalf("оболочка спавнулась без носителя:\n%s", s.why(res))
	}
	if !strings.Contains(strings.Join(res.Lines, "\n"), "headless без носителя") {
		t.Fatalf("лестница не назвала отступ:\n%s", s.why(res))
	}
}
