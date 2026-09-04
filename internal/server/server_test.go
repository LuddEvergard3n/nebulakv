package server

import (
	"context"
	"io"
	"log/slog"
	"nebulakv/internal/config"
	"nebulakv/internal/resp"
	"nebulakv/internal/storage"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func start(t *testing.T, max int) (string, context.CancelFunc, <-chan error) {
	c := config.Config{MaxClients: max, ReadTimeout: time.Second, WriteTimeout: time.Second, ShutdownTimeout: time.Second}
	return startServer(t, New(c, storage.New(nil), slog.New(slog.NewTextHandler(io.Discard, nil))))
}

func startServer(t *testing.T, s *Server) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { defer close(done); done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server did not stop")
		}
	})
	return ln.Addr().String(), cancel, done
}

func dial(t *testing.T, address string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return c
}

func send(c net.Conn, args ...string) error {
	items := make([]resp.Value, len(args))
	for i, s := range args {
		items[i] = resp.String(s)
	}
	return resp.Write(c, resp.List(items...))
}

func TestTCPPipelineAndPartialReads(t *testing.T) {
	address, cancel, done := start(t, 64)
	conn := dial(t, address)
	d := resp.NewDecoder(conn)
	for _, b := range []byte("*3\r\n$3\r\nSET\r\n$1\r\nx\r\n$3\r\na\x00b\r\n") {
		if _, err := conn.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := send(conn, "GET", "x"); err != nil {
		t.Fatal(err)
	}
	if err := send(conn, "PING"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"OK", "a\x00b", "PONG"} {
		v, err := d.Read()
		if err != nil || v.Text != want {
			t.Fatal(v, err, want)
		}
	}
	if err := send(conn, "INFO"); err != nil {
		t.Fatal(err)
	}
	v, err := d.Read()
	if err != nil || !strings.Contains(v.Text, "total_commands_processed:4") {
		t.Fatal(v, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown stuck")
	}
}

func TestMalformedAndConnectionLimit(t *testing.T) {
	address, _, _ := start(t, 1)
	first := dial(t, address)
	if err := send(first, "PING"); err != nil {
		t.Fatal(err)
	}
	if _, err := resp.NewDecoder(first).Read(); err != nil {
		t.Fatal(err)
	}
	second := dial(t, address)
	if _, err := second.Read(make([]byte, 1)); err == nil {
		t.Fatal("excess connection accepted")
	}
	if _, err := io.WriteString(first, "$999999999\r\n"); err != nil {
		t.Fatal(err)
	}
	v, err := resp.NewDecoder(first).Read()
	if err != nil || v.Kind != resp.Error {
		t.Fatal(v, err)
	}
}

func TestConcurrentTCPClients(t *testing.T) {
	address, _, _ := start(t, 64)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", address, time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			d := resp.NewDecoder(conn)
			for range 100 {
				if err := send(conn, "INCR", "counter"); err != nil {
					t.Error(err)
					return
				}
				v, err := d.Read()
				if err != nil || v.Kind != resp.Integer {
					t.Error(v, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	conn := dial(t, address)
	if err := send(conn, "GET", "counter"); err != nil {
		t.Fatal(err)
	}
	v, err := resp.NewDecoder(conn).Read()
	if err != nil || v.Text != "1200" {
		t.Fatal(v, err)
	}
}

func TestShutdownPartialClient(t *testing.T) {
	address, cancel, done := start(t, 64)
	conn := dial(t, address)
	if _, err := io.WriteString(conn, "*2\r\n$100\r\na"); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("partial client blocked shutdown")
	}
}
