package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestAppTaskSilenceProbe(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		fail           bool
		want           string
	}{
		{"running", `{"thread":{"id":"main","turns":[{"id":"ours","status":"inProgress"}]}}`, false, "running"},
		{"active without projection", `{"thread":{"id":"main","status":{"type":"active"},"turns":[]}}`, false, "running"},
		{"active another turn", `{"thread":{"id":"main","status":{"type":"active"},"turns":[{"id":"other","status":"inProgress"}]}}`, false, "running"},
		{"missed completion", `{"thread":{"id":"main","turns":[{"id":"ours","status":"completed"}]}}`, false, "completed"},
		{"failed turn", `{"thread":{"id":"main","turns":[{"id":"ours","status":"failed","error":{"message":"broken"}}]}}`, false, "failed"},
		{"probe failure", ``, true, "провер"},
		{"foreign turn", `{"thread":{"id":"main","turns":[{"id":"other","status":"inProgress"}]}}`, false, "провер"},
		{"foreign thread", `{"thread":{"id":"other","turns":[{"id":"ours","status":"inProgress"}]}}`, false, "провер"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				turn := &appTaskTurn{thread: "main", last: time.Now(), early: map[string]appTaskActivity{}, wake: make(chan struct{}, 1), ends: make(chan json.RawMessage, 128)}
				c := &appChat{thread: "main", busy: true, proc: &appServer{dead: make(chan struct{})}}
				calls := make(chan struct{}, 2)
				done := make(chan error, 1)
				probe := func() (json.RawMessage, error) {
					calls <- struct{}{}
					if tc.fail {
						return nil, errors.New("offline")
					}
					return json.RawMessage(tc.response), nil
				}
				confirmations := 0
				confirm := func() { confirmations++ }
				go func() { done <- c.waitTaskTurnProbe(turn, "ours", nil, 30*time.Minute, confirm, probe) }()
				time.Sleep(30 * time.Minute)
				synctest.Wait()
				select {
				case <-calls:
				default:
					select {
					case err := <-done:
						t.Fatalf("остановка до проверки: %v", err)
					default:
						t.Fatal("проверка не вызвана")
					}
				}
				if tc.want == "running" {
					select {
					case err := <-done:
						t.Fatalf("active stopped: %v", err)
					default:
					}
					time.Sleep(29 * time.Minute)
					synctest.Wait()
					select {
					case <-calls:
						t.Fatal("renewed window too short")
					default:
					}
					time.Sleep(time.Minute)
					synctest.Wait()
					select {
					case <-calls:
					default:
						t.Fatal("повторная проверка не вызвана")
					}
					if confirmations != 0 {
						t.Fatalf("активность из сверки преждевременно подтвердила восстановление: %d", confirmations)
					}
					c.mu.Lock()
					turn.note("turn/completed", json.RawMessage(`{"threadId":"main","turn":{"id":"ours","status":"completed"}}`), time.Now())
					c.mu.Unlock()
					if err := <-done; err != nil || confirmations != 1 {
						t.Fatalf("завершение хода: %v, подтверждений: %d", err, confirmations)
					}
					return
				}
				err := <-done
				if tc.want == "completed" {
					if err != nil || confirmations != 1 {
						t.Fatalf("завершение по сверке: %v, подтверждений: %d", err, confirmations)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), tc.want) || confirmations != 0 {
					t.Fatalf("ожидали %s, получили %v, подтверждений: %d", tc.want, err, confirmations)
				}
			})
		})
	}
}

func TestAppTaskSilenceProbeEventRace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		turn := &appTaskTurn{thread: "main", last: time.Now(), early: map[string]appTaskActivity{}, wake: make(chan struct{}, 1), ends: make(chan json.RawMessage, 128)}
		c := &appChat{thread: "main", busy: true, proc: &appServer{dead: make(chan struct{})}}
		entered := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		probe := func() (json.RawMessage, error) { close(entered); <-release; return nil, errors.New("offline") }
		go func() { done <- c.waitTaskTurnProbe(turn, "ours", nil, 30*time.Minute, nil, probe) }()
		time.Sleep(30 * time.Minute)
		<-entered
		c.mu.Lock()
		turn.note("turn/completed", json.RawMessage(`{"threadId":"main","turn":{"id":"ours","status":"completed"}}`), time.Now())
		c.mu.Unlock()
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("completion lost to probe failure: %v", err)
		}
	})
}

func TestAppTaskSilenceProbeStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		turn := &appTaskTurn{thread: "main", last: time.Now(), early: map[string]appTaskActivity{}, wake: make(chan struct{}, 1), ends: make(chan json.RawMessage, 128)}
		c := &appChat{thread: "main", proc: &appServer{dead: make(chan struct{})}}
		entered := make(chan struct{})
		release := make(chan struct{})
		stop := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- c.waitTaskTurnProbe(turn, "ours", stop, 30*time.Minute, nil, func() (json.RawMessage, error) { close(entered); <-release; return nil, errors.New("offline") })
		}()
		time.Sleep(30 * time.Minute)
		<-entered
		close(stop)
		if err := <-done; err == nil || !strings.Contains(err.Error(), "остановлен") {
			t.Fatalf("stop: %v", err)
		}
		close(release)
	})
}

func TestAppTaskSilenceProbeProgressRace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		turn := &appTaskTurn{thread: "main", last: time.Now(), early: map[string]appTaskActivity{}, wake: make(chan struct{}, 1), ends: make(chan json.RawMessage, 128)}
		c := &appChat{thread: "main", proc: &appServer{dead: make(chan struct{})}}
		entered := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		confirmations := 0
		confirm := func() { confirmations++ }
		go func() {
			done <- c.waitTaskTurnProbe(turn, "ours", nil, 30*time.Minute, confirm, func() (json.RawMessage, error) { close(entered); <-release; return nil, errors.New("offline") })
		}()
		time.Sleep(30 * time.Minute)
		<-entered
		c.mu.Lock()
		turn.note("item/agentMessage/delta", json.RawMessage(`{"threadId":"main","turnId":"ours"}`), time.Now())
		c.mu.Unlock()
		close(release)
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("progress lost: %v", err)
		default:
		}
		if confirmations != 1 {
			t.Fatalf("рабочее событие должно подтвердить восстановление один раз: %d", confirmations)
		}
		time.Sleep(29 * time.Minute)
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("renewed window lost: %v", err)
		default:
		}
		c.mu.Lock()
		turn.note("turn/completed", json.RawMessage(`{"threadId":"main","turn":{"id":"ours","status":"completed"}}`), time.Now())
		c.mu.Unlock()
		if err := <-done; err != nil || confirmations != 1 {
			t.Fatalf("завершение после рабочего события: %v, подтверждений: %d", err, confirmations)
		}
	})
}

func TestAppTaskSilenceProbeStopAndCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		turn := &appTaskTurn{thread: "main", last: time.Now(), early: map[string]appTaskActivity{}, wake: make(chan struct{}, 1), ends: make(chan json.RawMessage, 128)}
		turn.ends <- json.RawMessage(`{"threadId":"main","turn":{"id":"ours","status":"completed"}}`)
		c := &appChat{thread: "main", proc: &appServer{dead: make(chan struct{})}}
		stop := make(chan struct{})
		close(stop)
		confirm := func() { t.Error("остановленный ход подтвердил восстановление") }
		err := c.waitTaskTurnProbe(turn, "ours", stop, 30*time.Minute, confirm, func() (json.RawMessage, error) { t.Fatal("unexpected probe"); return nil, nil })
		if err == nil || !strings.Contains(err.Error(), "остановлен") {
			t.Fatalf("stop lost: %v", err)
		}
	})
}
