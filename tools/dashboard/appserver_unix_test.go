package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func wsTestRead(r *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	if h[1]&128 == 0 {
		return 0, nil, fmt.Errorf("unmasked client frame")
	}
	n := uint64(h[1] & 127)
	if n == 126 {
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > appWSLimit {
		return 0, nil, fmt.Errorf("oversized frame")
	}
	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return 0, nil, err
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	for i := range p {
		p[i] ^= mask[i%4]
	}
	return h[0] & 15, p, nil
}

func TestAppServerUnixRPC(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "appws-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		req, err := http.ReadRequest(r)
		if err != nil {
			serverDone <- err
			return
		}
		sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, err = fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
		if err != nil {
			serverDone <- err
			return
		}
		op, data, err := wsTestRead(r)
		if err != nil || op != 1 {
			serverDone <- fmt.Errorf("request: %d %v", op, err)
			return
		}
		var m struct {
			ID int64 `json:"id"`
		}
		if err = json.Unmarshal(data, &m); err != nil {
			serverDone <- err
			return
		}
		_, err = conn.Write([]byte{0x89, 1, 'p'})
		if err != nil {
			serverDone <- err
			return
		}
		op, data, err = wsTestRead(r)
		if err != nil || op != 10 || string(data) != "p" {
			serverDone <- fmt.Errorf("pong: %d %q %v", op, data, err)
			return
		}
		response := fmt.Sprintf(`{"id":%d,"result":{"ok":true}}`, m.ID)
		mid := len(response) / 2
		_, err = conn.Write(append([]byte{1, byte(mid)}, []byte(response[:mid])...))
		if err != nil {
			serverDone <- err
			return
		}
		_, err = conn.Write(append([]byte{0x80, byte(len(response) - mid)}, []byte(response[mid:])...))
		if err != nil {
			serverDone <- err
			return
		}
		_, _, err = wsTestRead(r)
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- nil
	}()
	s := &server{}
	p, err := s.appServerConnect(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.call("test", map[string]string{"hello": "world"})
	if err != nil || !strings.Contains(string(got), `"ok":true`) {
		t.Fatalf("response %s: %v", got, err)
	}
	if _, err = p.call("disconnect", nil); err == nil {
		t.Fatal("disconnect accepted")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server stuck")
	}
	p.kill()
	s.appWorkers.Wait()
}

func TestAppServerUnixRejectsHandshake(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "appws-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = http.ReadRequest(bufio.NewReader(c))
		fmt.Fprint(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: invalid\r\n\r\n")
	}()
	if p, err := (&server{}).appServerConnect(path, nil); err == nil {
		p.kill()
		t.Fatal("invalid handshake accepted")
	}
}
