package storage

import (
	"errors"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"
)

func put(t *testing.T, s *Store, k, v string, ttl int64) {
	t.Helper()
	if _, _, _, err := s.Set(k, v, SetOptions{TTL: ttl}); err != nil {
		t.Fatal(err)
	}
}

func TestExpiration(t *testing.T) {
	now := time.UnixMilli(100000)
	s := New(func() time.Time { return now })
	if s.TTL("a") != -2 {
		t.Fatal("missing TTL")
	}
	put(t, s, "a", "v", 0)
	if s.TTL("a") != -1 {
		t.Fatal("persistent TTL")
	}
	if n, err := s.Expire("a", 1500); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	now = now.Add(500 * time.Millisecond)
	if s.TTL("a") != 1000 {
		t.Fatal(s.TTL("a"))
	}
	now = now.Add(time.Second)
	if s.TTL("a") != -2 {
		t.Fatal("expired key survived")
	}
	put(t, s, "b", "v", 1)
	now = now.Add(time.Millisecond)
	s.Sweep(100)
	if len(s.entries) != 0 {
		t.Fatal("active expiration did not remove entries")
	}
	if got := s.Stats(); got.Keys != 0 || got.Expired != 2 {
		t.Fatal(got)
	}
	put(t, s, "p", "v", 10)
	if n, err := s.Persist("p"); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	now = now.Add(time.Second)
	if s.TTL("p") != -1 {
		t.Fatal("PERSIST did not remove TTL")
	}
	if n, err := s.Expire("p", 0); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if s.TTL("p") != -2 {
		t.Fatal("nonpositive expiry must delete")
	}
}

func TestConcurrentIncrement(t *testing.T) {
	s := New(nil)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				if _, err := s.Increment("n", 1); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	e, ok := s.GetMany([]string{"n"})
	if !ok[0] || e[0].Value != "8000" {
		t.Fatal(e, ok)
	}
}

func TestIntegerAndConditionalEdges(t *testing.T) {
	s := New(nil)
	for _, v := range []string{"", "01", "+1", " 1", "-0", "1.0", "9223372036854775808"} {
		put(t, s, "n", v, 0)
		if _, err := s.Increment("n", 1); err == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	put(t, s, "n", strconv.FormatInt(math.MaxInt64, 10), 0)
	if _, err := s.Increment("n", 1); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
	put(t, s, "n", strconv.FormatInt(math.MinInt64, 10), 0)
	if _, err := s.Increment("n", -1); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
	put(t, s, "key", "old", 10000)
	old, existed, applied, err := s.Set("key", "new", SetOptions{NX: true})
	if old.Value != "old" || !existed || applied || err != nil {
		t.Fatal(old, existed, applied, err)
	}
	if _, _, applied, err = s.Set("missing", "new", SetOptions{XX: true}); applied || err != nil {
		t.Fatal(applied, err)
	}
	put(t, s, "key", "replacement", 0)
	if s.TTL("key") != -1 {
		t.Fatal("SET must clear TTL")
	}
	if n, err := s.Delete([]string{"key", "key", "missing"}); n != 1 || err != nil {
		t.Fatal(n, err)
	}
}

type brokenJournal struct{}

func (brokenJournal) Append(Mutation) error { return errors.New("disk full") }

func TestJournalFailureDoesNotMutate(t *testing.T) {
	s := New(nil)
	put(t, s, "a", "old", 0)
	s.SetJournal(brokenJournal{})
	if err := s.SetMany([]string{"a", "new", "b", "new"}); err == nil {
		t.Fatal("missing write failure")
	}
	e, found := s.GetMany([]string{"a", "b"})
	if e[0].Value != "old" || found[1] {
		t.Fatal("partial write", e, found)
	}
	if err := s.Clear(); err == nil {
		t.Fatal("missing clear failure")
	}
	if s.Stats().Keys != 1 {
		t.Fatal("clear applied despite failure")
	}
}

func TestMultiKeyAtomicity(t *testing.T) {
	s := New(nil)
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				v := strconv.Itoa(worker*200 + i)
				if err := s.SetMany([]string{"a", v, "b", v}); err != nil {
					t.Error(err)
				}
				e, found := s.GetMany([]string{"a", "b"})
				if !found[0] || !found[1] || e[0].Value != e[1].Value {
					t.Error("torn MSET", e)
				}
			}
		}()
	}
	wg.Wait()
}

func TestGlob(t *testing.T) {
	for _, tc := range []struct {
		p, s string
		want bool
	}{
		{"*", "a/b", true}, {"a?c", "abc", true}, {"a?c", "ac", false}, {"[a-c]*", "cat", true}, {"[^0-9]", "x", true}, {"[^0-9]", "2", false}, {"a\\*", "a*", true}, {"", "", true}, {"[abc", "a", false}, {"*a*b", "xxaaab", true},
	} {
		if got := Match(tc.p, tc.s); got != tc.want {
			t.Errorf("%q %q: %v", tc.p, tc.s, got)
		}
	}
}

func BenchmarkStore(b *testing.B) {
	for _, kind := range []string{"GET", "SET", "Mixed"} {
		b.Run(kind, func(b *testing.B) {
			s := New(nil)
			if _, _, _, err := s.Set("key", "value", SetOptions{}); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if kind == "GET" || kind == "Mixed" && i%2 == 0 {
					s.Get("key")
				} else {
					if _, _, _, err := s.Set("key", "value", SetOptions{}); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
	for _, parallel := range []int{1, 2, 4, 8} {
		b.Run("Concurrent/"+strconv.Itoa(parallel), func(b *testing.B) {
			s := New(nil)
			b.SetParallelism(parallel)
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := s.Increment("counter", 1); err != nil {
						b.Error(err)
					}
				}
			})
		})
	}
}
