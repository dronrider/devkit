package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestAppTaskSilenceTimer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		thread string
		turn   string
		wantOK bool
	}{
		{"current turn", "main", "ours", true},
		{"another thread", "other", "ours", false},
		{"another turn", "main", "other", false},
		{"no events", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				turn := &appTaskTurn{thread: "main", last: time.Now(), early: map[string]appTaskActivity{},
					wake: make(chan struct{}, 1), ends: make(chan json.RawMessage, 128)}
				c := &appChat{thread: "main", taskTurn: turn, busy: true, proc: &appServer{dead: make(chan struct{})}}
				s := &server{now: time.Now, appChats: map[string]*appChat{"chat": c}}
				done := make(chan error, 1)
				go func() {
					done <- c.waitTaskTurnProbe(turn, "ours", nil, appBusyCap, func() (json.RawMessage, error) { return nil, errors.New("offline") })
				}()
				time.Sleep(25 * time.Minute)
				if tc.thread != "" {
					params, _ := json.Marshal(map[string]string{"threadId": tc.thread, "turnId": tc.turn})
					s.appNote("chat", "item/agentMessage/delta", params)
				}
				time.Sleep(5 * time.Minute)
				synctest.Wait()
				if !tc.wantOK {
					select {
					case err := <-done:
						if err == nil || !strings.Contains(err.Error(), "проверить ход") {
							t.Fatalf("ожидался тайм-аут тишины, получено %v", err)
						}
					default:
						t.Fatal("таймер тишины не сработал")
					}
					return
				}
				select {
				case err := <-done:
					t.Fatalf("текущий ход завершился раньше уведомления: %v", err)
				default:
				}
				s.appNote("chat", "turn/completed", json.RawMessage(`{"threadId":"main","turn":{"id":"ours","status":"completed"}}`))
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
