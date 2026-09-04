package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const usageText = `dashboard: веб-дашборд агентской разработки (LLD DK-112)

  serve [--static <dir>]      поднять сервер: конфиг ~/.devkit/dashboard.local
                              (root = <корень поиска проектов>, addr, port,
                              token), порт по умолчанию 7112, журнал
                              ~/.devkit/dashboard.log. Флаг --static читает
                              статику с диска вместо вшитой: правка видна по
                              F5 без пересборки
  secret [--rotate]           напечатать токен входа (родится сам, если его
                              ещё нет); --rotate заменяет секрет, и все
                              выданные куки разом мертвы
  check [<ID>...] [-C <дир>]  поднять прогон агентской части сценария проверки
                              по строкам Check проекта: та же tmux-сессия
                              task-<ID>, что у кнопки экрана, с заказом
                              «прогони сценарий и закрой». Без ID берётся вся
                              секция Check, корень по умолчанию текущий.
                              Зовут её выкат без человека в окне (shipctl
                              merge при autonomous и ship после выката) и тик
                              сторожка страховкой; строка отчёта печатается на
                              каждую задачу, выход 1 значит нужный подъём не
                              вышел
  round <ID>... [-C <дир>]    начать второй круг чужого ревью: живой сессии
                              задачи заказ идёт репликой в её окно, а без
                              живой сессии поднимается новая, той же
                              tmux-сессией task-<ID>. Зовёт команду тик
                              сторожка, увидевший ответ автора в тредах MR
  smoke [--keep]              сквозной прогон по API на синтетическом
                              окружении (свой дом, свой проект, фикстуры
                              вместо чужих программ): доска, запуск, стоп,
                              сообщение цели и уведомление о стопе в ленте;
                              пройдя, печатает «dashboard smoke: ok».
                              --keep оставляет временное окружение на месте

Вход по токену из конфига: страница /login, кука на 30 дней. Без входа не
отдаётся ни одна строка данных; открыт один /healthz с версией, аптаймом,
числом проектов и ошибками конфига.
`

//go:embed static
var embedded embed.FS

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

func main() {
	if versionRequested() {
		return
	}
	args := os.Args[1:]
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fatal(fmt.Errorf("дом не нашёлся: %v", err))
	}
	switch args[0] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		staticDir := fs.String("static", "", "читать статику с диска, для разработки")
		fs.Parse(args[1:])
		if err := cmdServe(home, *staticDir); err != nil {
			fatal(err)
		}
	case "secret":
		fs := flag.NewFlagSet("secret", flag.ExitOnError)
		rotate := fs.Bool("rotate", false, "заменить секрет: все выданные куки гаснут")
		fs.Parse(args[1:])
		token, err := cmdSecret(home, *rotate)
		if err != nil {
			fatal(err)
		}
		fmt.Println(token)
	case "check":
		fs := flag.NewFlagSet("check", flag.ExitOnError)
		dir := fs.String("C", ".", "корень проекта с доской")
		fs.Parse(args[1:])
		// Слова про исход уже напечатаны построчно, и провал добавляет к ним
		// только код возврата: зовущий читает строки, а не пересказ ошибки.
		if err := cmdCheck(home, *dir, fs.Args(), os.Stdout); err != nil {
			if errors.Is(err, errCheckRunFailed) {
				os.Exit(1)
			}
			fatal(err)
		}
	case "round":
		fs := flag.NewFlagSet("round", flag.ExitOnError)
		dir := fs.String("C", ".", "корень проекта с доской")
		fs.Parse(args[1:])
		if err := cmdRound(home, *dir, fs.Args(), os.Stdout); err != nil {
			if errors.Is(err, errCheckRunFailed) {
				os.Exit(1)
			}
			fatal(err)
		}
	case "smoke":
		fs := flag.NewFlagSet("smoke", flag.ExitOnError)
		keep := fs.Bool("keep", false, "оставить временное окружение прогона")
		fs.Parse(args[1:])
		if err := cmdSmoke(os.Stdout, *keep); err != nil {
			// Провал называет шаг и причину: код возврата без слов ничего не
			// говорит тому, кто гонял прогон руками.
			fmt.Fprintf(os.Stderr, "dashboard smoke: провал, %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n%s", args[0], usageText)
		os.Exit(2)
	}
}

// cmdSecret печатает токен входа; не будь его в конфиге, он рождается тут же.
// --rotate заменяет секрет: подпись кук перестаёт сходиться, и все входы
// разом отозваны.
func cmdSecret(home string, rotate bool) (string, error) {
	cfg, err := LoadConfig(home)
	if err != nil {
		return "", err
	}
	if cfg.Token != "" && !rotate {
		return cfg.Token, nil
	}
	token, err := newSecret()
	if err != nil {
		return "", err
	}
	if err := saveToken(cfg.Path, token); err != nil {
		return "", err
	}
	return token, nil
}

// openLog открывает журнал ~/.devkit/dashboard.log на дозапись. В журнал идут
// старты и остановки, входы и провалы входа, ошибки; чтения не пишутся, иначе
// опрос открытых экранов зарастил бы его за день.
func openLog(home string) (func(format string, args ...any), func() error) {
	path := filepath.Join(home, filepath.FromSlash(logRel))
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "журнал %s не открылся (%v), пишу в stderr\n", path, err)
		return func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}, func() error { return nil }
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(f, time.Now().Format("2006-01-02T15:04:05")+" "+format+"\n", args...)
	}
	return logf, f.Close
}

func cmdServe(home, staticDir string) error {
	cfg, err := LoadConfig(home)
	if err != nil {
		return err
	}
	logf, closeLog := openLog(home)
	defer closeLog()
	// Каталог планов заводится демоном: агент пишет туда файл своего плана, и
	// заставлять его думать о создании каталога незачем.
	if err := os.MkdirAll(planDir(realHomeOr(home)), 0o755); err != nil {
		logf("каталог планов не завёлся: %v", err)
	}
	// Свой конец канала живых сессий: агент, получивший реплику, отвечает на
	// адрес отправителя, и без слушателя его ход уходит в ошибку доставки.
	defer peerListen(logf)()
	if cfg.Token == "" {
		// Секрет рождается на машине при первом старте; по сети он не ездит,
		// печатает его команда dashboard secret.
		token, err := newSecret()
		if err != nil {
			return err
		}
		if err := saveToken(cfg.Path, token); err != nil {
			return err
		}
		cfg.Token = token
		logf("секрет входа создан в %s, напечатать: dashboard secret", cfg.Path)
	}
	// Секрет помощника askpass (DK-772) рождается заново на каждом старте, в
	// отличие от куки входа: рестарт демона сам собой гасит зависшие ожидания
	// пароля, а помощник, переживший старый демон, узнаётся по неверному
	// секрету, а не по протухшей записи в памяти. Пишется он под настоящий дом
	// машины, а не под cfg.Home: HOME поднятой сессии это именно настоящий дом
	// (launchEnv), и там же его будет искать помощник.
	askpassSecret, err := newSecret()
	if err != nil {
		return err
	}
	askHome := realHomeOr(home)
	if err := writeAskpassSecret(askHome, askpassSecret); err != nil {
		logf("секрет помощника askpass не записался в %s: %v, sudo и ssh из чата останутся без пароля",
			askpassSecretPath(askHome), err)
		askpassSecret = ""
	}
	var static fs.FS
	if staticDir != "" {
		static = os.DirFS(staticDir)
		logf("статика читается с диска: %s", staticDir)
	} else {
		sub, err := fs.Sub(embedded, "static")
		if err != nil {
			return err
		}
		static = sub
	}
	srv := newServer(cfg, static, logf)
	srv.askpassSecret = askpassSecret
	ln, err := net.Listen("tcp", cfg.ListenAddr())
	if err != nil {
		logf("старт не удался: %v", err)
		return err
	}
	httpSrv := httpServer(srv.handler())
	// Снимок квоты держит свежим сам демон: без этого он обновлялся только
	// стартом сессии, и на экране почти всегда стоял часовой давности.
	// Просроченный вход снимается тем же кругом, а не ленивым нажатием
	// «Войти»: открытая и забытая ссылка иначе держит tmux с живым клиентом.
	keeperStop := make(chan struct{})
	defer close(keeperStop)
	go srv.quotaKeeper(keeperStop)
	go srv.loginKeeper(keeperStop)
	// Сторож поднятых сессий: смерть подъёма замечает он, когда на разговор
	// никто не смотрит (chatwatch.go).
	go srv.chatWatchKeeper(keeperStop)
	for _, e := range cfg.Errs {
		logf("конфиг: %s", e)
	}
	logf("старт %s %s (%s) на %s", toolName, version, commit, cfg.ListenAddr())

	stop := make(chan string, 2)
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		stop <- "сигнал остановки"
	}()
	stopWatch := watchBinary(5*time.Second, func() {
		stop <- "бинарь заменён выкатом"
	})
	defer stopWatch()

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()
	select {
	case why := <-stop:
		logf("остановка: %s", why)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpSrv.Shutdown(ctx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		logf("сервер упал: %v", err)
		return err
	}
}

// httpServer собирает сервер с пределами. ReadHeaderTimeout отрезает молча
// висящие соединения: демон слушает все интерфейсы, и без предела их можно
// набрать сколько угодно. WriteTimeout не ставится сознательно: дальше по
// серии едут SSE-потоки, которым положено жить долго.
func httpServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// watchBinary следит за инодой собственного бинаря: выкат devkit это
// пересборка бинарей в PATH без правки обвязки, и демон, заметив замену,
// доживает начатые ответы и выходит, а KeepAlive launchd поднимает уже новый.
// Работает и при ручном go build мимо выката.
func watchBinary(every time.Duration, onChange func()) (stop func()) {
	done := make(chan struct{})
	stop = func() { close(done) }
	path, err := os.Executable()
	if err != nil {
		return stop
	}
	start, ok := inodeOf(path)
	if !ok {
		return stop
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				// Момент замены файла ещё нет, пропускается: инода сверится
				// следующим тиком, когда новый бинарь ляжет на место.
				if ino, ok := inodeOf(path); ok && ino != start {
					onChange()
					return
				}
			}
		}
	}()
	return stop
}

func inodeOf(path string) (uint64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Ino, true
}
