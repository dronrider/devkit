package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const appWSLimit = 32 << 20

type appWS struct {
	conn net.Conn
	mu   sync.Mutex
}

func (w *appWS) Close() error { return w.conn.Close() }

// Write receives one JSONL message from the RPC layer and sends one text frame.
func (w *appWS) Write(p []byte) (int, error) {
	payload := p
	if len(payload) > 0 && payload[len(payload)-1] == '\n' {
		payload = payload[:len(payload)-1]
	}
	if err := w.frame(1, payload); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *appWS) frame(op byte, payload []byte) error {
	if len(payload) > appWSLimit {
		return errors.New("WebSocket message too large")
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		header = append(header, 0x80|byte(n))
	case n <= 65535:
		header = append(header, 0x80|126, byte(n>>8), byte(n))
	default:
		header = append(header, 0x80|127)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(n))
		header = append(header, size[:]...)
	}
	header = append(header, mask[:]...)
	frame := make([]byte, len(header)+len(payload))
	copy(frame, header)
	for i, b := range payload {
		frame[len(header)+i] = b ^ mask[i%4]
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for len(frame) != 0 {
		n, err := w.conn.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		frame = frame[n:]
	}
	return nil
}

func appWSRead(w *appWS, r *bufio.Reader, out *io.PipeWriter) {
	defer w.Close()
	defer out.Close()
	var message []byte
	fragmented := false
	for {
		var h [2]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		if h[0]&0x70 != 0 || h[1]&0x80 != 0 {
			return
		}
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
			if n < 126 {
				return
			}
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return
			}
			n = binary.BigEndian.Uint64(b[:])
			if n < 65536 {
				return
			}
		}
		if n > appWSLimit || uint64(len(message))+n > appWSLimit {
			return
		}
		if op >= 8 && (!fin || n > 125) {
			return
		}
		data := make([]byte, int(n))
		if _, err := io.ReadFull(r, data); err != nil {
			return
		}
		switch op {
		case 8:
			_ = w.frame(8, data)
			return
		case 9:
			if w.frame(10, data) != nil {
				return
			}
			continue
		case 10:
			continue
		case 1:
			if fragmented {
				return
			}
			message = append(message, data...)
			fragmented = !fin
		case 0:
			if !fragmented {
				return
			}
			message = append(message, data...)
			fragmented = !fin
		default:
			return
		}
		if fin {
			message = append(message, '\n')
			if _, err := out.Write(message); err != nil {
				return
			}
			message = nil
		}
	}
}

func (s *server) appServerConnect(socket string, note func(string, json.RawMessage)) (*appServer, error) {
	conn, err := net.DialTimeout("unix", socket, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("сокет Codex: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	req, err := http.NewRequest("GET", "http://localhost/", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, fmt.Errorf("WebSocket handshake: %w", err)
	}
	hash := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") || !headerHasToken(resp.Header.Values("Connection"), "upgrade") || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(hash[:]) {
		return nil, fmt.Errorf("неверный ответ WebSocket handshake: %s", resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	w := &appWS{conn: conn}
	pr, pw := io.Pipe()
	p := &appServer{in: w, note: note, pending: map[int64]chan *appMsg{}, dead: make(chan struct{})}
	s.appWorkers.Go(func() { appWSRead(w, reader, pw) })
	s.appWorkers.Go(func() { defer pr.Close(); p.readOut(pr) })
	ok = true
	return p, nil
}

func headerHasToken(values []string, token string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
