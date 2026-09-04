package persistence

import (
	"errors"
	"nebulakv/internal/storage"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRewriteKeepsLiveStateAndContinues(t *testing.T) {
	dir := t.TempDir()
	now := time.UnixMilli(100000)
	clock := func() time.Time { return now }
	s, l := openStore(t, dir, clock)
	for range 50 {
		set(t, s, "history", strings.Repeat("x", 1024), 0)
	}
	set(t, s, "history", "final", 0)
	set(t, s, "\xff", "\x00\xfe", 0)
	set(t, s, "gone", "old", 1)
	set(t, s, "ttl", "value", 1000)
	now = now.Add(10 * time.Millisecond)
	before := l.Stats().Bytes
	if !l.NeedsRewrite(1024) {
		t.Fatal("automatic threshold not triggered")
	}
	if err := s.Rewrite(); err != nil {
		t.Fatal(err)
	}
	if after := l.Stats(); after.Bytes >= before || after.Rewrites != 1 || l.NeedsRewrite(1) {
		t.Fatal("rewrite did not compact or would immediately repeat", before, after)
	}
	set(t, s, "after", "append", 0)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	s, l = openStore(t, dir, clock)
	defer l.Close()
	if v, ok := s.Get("history"); !ok || v.Value != "final" {
		t.Fatal(v, ok)
	}
	if v, ok := s.Get("\xff"); !ok || v.Value != "\x00\xfe" {
		t.Fatal(v, ok)
	}
	if s.Stats().Keys != 4 || s.TTL("ttl") != 990 {
		t.Fatal(s.Stats(), s.TTL("ttl"))
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".nebulakv-rewrite-*"))
	if err != nil || len(temps) != 0 {
		t.Fatal(temps, err)
	}
}

func TestRewriteFailurePreservesJournalAndLease(t *testing.T) {
	dir := t.TempDir()
	s, l := openStore(t, dir, nil)
	set(t, s, "a", "old", 0)
	l.replace = func(string, string) error {
		if other, err := Open(dir, func(storage.Mutation) error { return nil }); err == nil {
			other.Close()
			t.Fatal("lease lost during replacement")
		}
		return errors.New("simulated rename failure")
	}
	if err := s.Rewrite(); err == nil {
		t.Fatal("expected replacement failure")
	}
	l.replace = os.Rename
	set(t, s, "b", "new", 0)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	s, l = openStore(t, dir, nil)
	defer l.Close()
	if s.Stats().Keys != 2 {
		t.Fatal(s.Stats())
	}
}

func TestConcurrentRewriteDoesNotLoseWrites(t *testing.T) {
	s, l := openStore(t, t.TempDir(), nil)
	defer l.Close()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := s.Increment("counter", 1); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	for range 5 {
		if err := s.Rewrite(); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if e, _ := s.Get("counter"); e.Value != "80" {
		t.Fatal(e)
	}
	if err := s.Rewrite(); err != nil {
		t.Fatal(err)
	}
	check := storage.New(nil)
	f, err := os.Open(l.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Use the same journal decoder after skipping its already-verified header.
	if _, err := f.Seek(int64(len(magic)), 0); err != nil {
		t.Fatal(err)
	}
	if err := replay(f, check.Replay); err != nil {
		t.Fatal(err)
	}
	if e, _ := check.Get("counter"); e.Value != "80" {
		t.Fatal("lost persisted writes", e)
	}
}
