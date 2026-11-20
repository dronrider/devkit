package main

import (
	"encoding/json"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

func TestAppTaskSilenceProbeWire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer := net.Pipe()
		defer client.Close()
		defer peer.Close()
		deadline := time.Now().Add(31 * time.Minute)
		if err := client.SetDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		if err := peer.SetDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		proc := &appServer{in: client, pending: make(map[int64]chan *appMsg), dead: make(chan struct{})}
		go proc.readOut(client)
		turn := &appTaskTurn{thread: "thread-1", last: time.Now(), early: map[string]appTaskActivity{}, wake: make(chan struct{}, 1), ends: make(chan json.RawMessage, 128)}
		c := &appChat{thread: "thread-1", busy: true, proc: proc}
		type request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				Thread       string `json:"threadId"`
				IncludeTurns bool   `json:"includeTurns"`
			} `json:"params"`
		}
		requests := make(chan request, 1)
		peerErr := make(chan error, 1)
		respond := make(chan struct{}, 1)
		defer close(respond)
		go func() {
			var req request
			if err := json.NewDecoder(peer).Decode(&req); err != nil {
				peerErr <- err
				return
			}
			requests <- req
			<-respond
			peerErr <- json.NewEncoder(peer).Encode(map[string]any{"id": req.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-1", "turns": []any{map[string]any{"id": "turn-1", "status": "completed"}}}}})
		}()
		done := make(chan error, 1)
		go func() { done <- c.waitTaskTurn(turn, "turn-1", nil, 30*time.Minute, nil) }()
		synctest.Wait()
		time.Sleep(30*time.Minute - time.Nanosecond)
		synctest.Wait()
		select {
		case req := <-requests:
			t.Fatalf("запрос до истечения тишины: %+v", req)
		default:
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		var req request
		select {
		case req = <-requests:
		case err := <-peerErr:
			t.Fatalf("чтение запроса: %v", err)
		case err := <-done:
			t.Fatalf("ход завершился до проверки: %v", err)
		default:
			t.Fatal("запрос после 30 минут не отправлен")
		}
		if req.ID == 0 || req.Method != "thread/read" || req.Params.Thread != "thread-1" || !req.Params.IncludeTurns {
			t.Fatalf("неверный запрос: %+v", req)
		}
		respond <- struct{}{}
		if err := <-peerErr; err != nil {
			t.Fatalf("ответ не записан: %v", err)
		}
		if err := <-done; err != nil {
			t.Fatalf("завершение не принято: %v", err)
		}
	})
}
