package server

import (
	"io"
	"log/slog"
	"nebulakv/internal/config"
	"nebulakv/internal/resp"
	"nebulakv/internal/storage"
	"testing"
	"time"
)

func TestAuthenticationIsConnectionLocal(t *testing.T) {
	c := config.Config{Password: "test-only-secret", MaxClients: 8, ReadTimeout: time.Second, WriteTimeout: time.Second, ShutdownTimeout: time.Second}
	address, _, _ := startServer(t, New(c, storage.New(nil), slog.New(slog.NewTextHandler(io.Discard, nil))))
	a := dial(t, address)
	b := dial(t, address)
	da, db := resp.NewDecoder(a), resp.NewDecoder(b)
	check := func(args []string, want string) {
		t.Helper()
		if err := send(a, args...); err != nil {
			t.Fatal(err)
		}
		v, err := da.Read()
		if err != nil || v.Text != want {
			t.Fatal(args, v, err)
		}
	}
	check([]string{"SET", "a", "1"}, "NOAUTH Authentication required")
	check([]string{"AUTH", "wrong"}, "WRONGPASS invalid username-password pair")
	check([]string{"AUTH", "default", "test-only-secret"}, "OK")
	check([]string{"SET", "a", "1"}, "OK")
	if err := send(b, "GET", "a"); err != nil {
		t.Fatal(err)
	}
	v, err := db.Read()
	if err != nil || v.Text != "NOAUTH Authentication required" {
		t.Fatal("auth leaked", v, err)
	}
	check([]string{"AUTH", "wrong"}, "WRONGPASS invalid username-password pair")
	check([]string{"GET", "a"}, "NOAUTH Authentication required")
	for range 4 {
		check([]string{"AUTH", "wrong"}, "WRONGPASS invalid username-password pair")
	}
	if _, err := da.Read(); err == nil {
		t.Fatal("five failures should close connection")
	}
}
