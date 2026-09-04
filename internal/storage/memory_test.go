package storage

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingJournal struct{ records []Mutation }

func (j *recordingJournal) Append(m Mutation) error { j.records = append(j.records, m); return nil }

func TestMemoryAdmissionIsAtomic(t *testing.T) {
	s := New(nil)
	if err := s.SetMemoryLimit(200); err != nil {
		t.Fatal(err)
	}
	j := &recordingJournal{}
	s.SetJournal(j)
	put(t, s, "a", "1", 0)
	if err := s.SetMany([]string{"a", "changed", "b", strings.Repeat("x", 100)}); !errors.Is(err, ErrMemory) {
		t.Fatal(err)
	}
	if len(j.records) != 1 {
		t.Fatal("rejected command reached journal")
	}
	e, found := s.Get("a")
	if !found || e.Value != "1" || s.Stats().Keys != 1 {
		t.Fatal("partial mutation", e, s.Stats())
	}
	if err := s.SetMany([]string{"a", strings.Repeat("x", 1000), "a", "2", "b", "3"}); err != nil {
		t.Fatal("final duplicate-key value must determine cost", err)
	}
	if s.Stats().AccountedBytes != 196 {
		t.Fatal(s.Stats())
	}
	if _, err := s.Delete([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if s.Stats().AccountedBytes != 98 {
		t.Fatal("delete accounting")
	}
	if err := s.Clear(); err != nil || s.Stats().AccountedBytes != 0 {
		t.Fatal(err, s.Stats())
	}
}

func TestMemoryExpiryAndCounterGrowth(t *testing.T) {
	now := time.UnixMilli(1000)
	s := New(func() time.Time { return now })
	if err := s.SetMemoryLimit(98); err != nil {
		t.Fatal(err)
	}
	put(t, s, "a", "9", 1)
	if _, err := s.Increment("a", 1); !errors.Is(err, ErrMemory) {
		t.Fatal(err)
	}
	now = now.Add(time.Millisecond)
	put(t, s, "b", "v", 0)
	if s.Stats().Keys != 1 || s.Stats().AccountedBytes != 98 {
		t.Fatal(s.Stats())
	}
	if err := s.Replay(Mutation{Changes: []Change{{Key: []byte("c"), Value: []byte("v")}}}); !errors.Is(err, ErrMemory) {
		t.Fatal("replay must enforce budget", err)
	}
}

func TestMemoryConcurrentAdmission(t *testing.T) {
	s := New(nil)
	if err := s.SetMemoryLimit(98); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := s.Set(string(rune('a'+i)), "v", SetOptions{})
			if err != nil && !errors.Is(err, ErrMemory) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if stats := s.Stats(); stats.Keys != 1 || stats.AccountedBytes > stats.MaxMemory {
		t.Fatal(stats)
	}
}
