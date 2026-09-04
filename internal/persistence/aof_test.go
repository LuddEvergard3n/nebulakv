package persistence

import (
	"nebulakv/internal/storage"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openStore(t *testing.T, dir string, now func() time.Time) (*storage.Store, *Log) {
	t.Helper()
	s := storage.New(now)
	l, err := Open(dir, s.Replay)
	if err != nil {
		t.Fatal(err)
	}
	s.SetJournal(l)
	return s, l
}
func set(t *testing.T, s *storage.Store, k, v string, ttl int64) {
	t.Helper()
	if _, _, _, err := s.Set(k, v, storage.SetOptions{TTL: ttl}); err != nil {
		t.Fatal(err)
	}
}

func TestRestartBinaryAndAbsoluteExpiry(t *testing.T) {
	dir := t.TempDir()
	now := time.UnixMilli(100000)
	clock := func() time.Time { return now }
	s, l := openStore(t, dir, clock)
	set(t, s, "\xff\x00", "\xfe\r\n\x00", 0)
	set(t, s, "expired", "x", 100)
	set(t, s, "alive", "x", 2000)
	if err := s.SetMany([]string{"a", "1", "b", "2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	s, l = openStore(t, dir, clock)
	defer l.Close()
	e, found := s.GetMany([]string{"\xff\x00", "expired", "a", "b"})
	if e[0].Value != "\xfe\r\n\x00" || !found[0] || found[1] || found[2] || e[3].Value != "2" {
		t.Fatal(e, found)
	}
	if s.TTL("alive") != 1000 {
		t.Fatal("TTL reset during replay", s.TTL("alive"))
	}
}

func TestIncompleteTailAndContinuedAppend(t *testing.T) {
	for _, tail := range [][]byte{{0, 0}, {0, 0, 0, 10, 0, 0, 0, 0, 'x'}} {
		t.Run(string(rune(len(tail)+'0')), func(t *testing.T) {
			dir := t.TempDir()
			s, l := openStore(t, dir, nil)
			set(t, s, "safe", "value", 0)
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "appendonly.aof")
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write(tail); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			s, l = openStore(t, dir, nil)
			set(t, s, "after", "recovery", 0)
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			s, l = openStore(t, dir, nil)
			defer l.Close()
			if s.Stats().Keys != 2 {
				t.Fatal(s.Stats())
			}
		})
	}
}

func TestCorruptionIsNotSilentlyTruncated(t *testing.T) {
	dir := t.TempDir()
	s, l := openStore(t, dir, nil)
	set(t, s, "safe", "value", 0)
	l.Close()
	path := filepath.Join(dir, "appendonly.aof")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 1
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if l, err := Open(dir, func(storage.Mutation) error { return nil }); err == nil {
		l.Close()
		t.Fatal("accepted corrupt journal")
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Size() != int64(len(b)) {
		t.Fatal("corrupt journal changed", stat, err)
	}
}

func TestExclusiveLockAndClear(t *testing.T) {
	dir := t.TempDir()
	s, l := openStore(t, dir, nil)
	if other, err := Open(dir, func(storage.Mutation) error { return nil }); err == nil {
		other.Close()
		t.Fatal("journal opened twice")
	}
	set(t, s, "key", "value", 0)
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	s, l = openStore(t, dir, nil)
	defer l.Close()
	if s.Stats().Keys != 0 {
		t.Fatal(s.Stats())
	}
}

func TestStickyWriteFailure(t *testing.T) {
	dir := t.TempDir()
	s, l := openStore(t, dir, nil)
	defer l.lease.Close()
	set(t, s, "key", "old", 0)
	if err := l.file.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, _, err := s.Set("key", "new", storage.SetOptions{}); err == nil {
			t.Fatal("write failure not reported")
		}
	}
	e, _ := s.GetMany([]string{"key"})
	if e[0].Value != "old" || l.Status() != "error" {
		t.Fatal(e, l.Status())
	}
}
