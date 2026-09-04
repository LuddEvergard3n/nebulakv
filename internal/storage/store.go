// Package storage provides atomic string operations and millisecond expiration.
package storage

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"
)

var (
	ErrInteger  = errors.New("value is not an integer or out of range")
	ErrOverflow = errors.New("increment or decrement would overflow")
	ErrExpiry   = errors.New("invalid expire time")
)

type Entry struct {
	Value     string
	ExpiresAt int64
}

// Byte slices ensure that JSON journals preserve arbitrary binary keys/values.
type Change struct {
	Key       []byte `json:"key"`
	Value     []byte `json:"value,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Delete    bool   `json:"delete,omitempty"`
}

type Mutation struct {
	Clear   bool     `json:"clear,omitempty"`
	Changes []Change `json:"changes,omitempty"`
}

type Journal interface{ Append(Mutation) error }

type Store struct {
	revision        uint64
	mu              sync.Mutex
	entries         map[string]Entry
	now             func() time.Time
	journal         Journal
	expired         uint64
	used, maxMemory int64
}

func New(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{entries: make(map[string]Entry), now: now, maxMemory: DefaultMaxMemory}
}

// SetJournal is called after replay, before accepting clients.
func (s *Store) SetJournal(j Journal) { s.mu.Lock(); defer s.mu.Unlock(); s.journal = j }

func (s *Store) lookup(key string, now int64) (Entry, bool) {
	e, ok := s.entries[key]
	if ok && e.ExpiresAt != 0 && e.ExpiresAt <= now {
		s.remove(key)
		s.expired++
		return Entry{}, false
	}
	return e, ok
}

func (s *Store) apply(m Mutation) {
	s.revision++
	if m.Clear {
		s.entries = make(map[string]Entry)
		s.used = 0
	}
	for _, c := range m.Changes {
		if c.Delete {
			s.remove(string(c.Key))
		} else {
			s.assign(string(c.Key), Entry{string(c.Value), c.ExpiresAt})
		}
	}
}

// Replay applies trusted, validated journal records without appending them again.
func (s *Store) Replay(m Mutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.admit(m); err != nil {
		return err
	}
	s.apply(m)
	return nil
}

func (s *Store) commit(m Mutation) error {
	if err := s.admit(m); err != nil {
		return err
	}
	if s.journal != nil {
		if err := s.journal.Append(m); err != nil {
			return err
		}
	}
	s.apply(m)
	return nil
}

func change(key string, e Entry) Change {
	return Change{Key: []byte(key), Value: []byte(e.Value), ExpiresAt: e.ExpiresAt}
}

// In-memory writes do not need to allocate or encode a journal record.
func (s *Store) put(key string, e Entry) error {
	if err := s.admitPut(key, e); err != nil {
		return err
	}
	if s.journal == nil {
		s.assign(key, e)
		s.revision++
		return nil
	}
	return s.commit(Mutation{Changes: []Change{change(key, e)}})
}

func (s *Store) Get(key string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookup(key, s.now().UnixMilli())
}

func (s *Store) GetMany(keys []string) ([]Entry, []bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	entries, found := make([]Entry, len(keys)), make([]bool, len(keys))
	for i, k := range keys {
		entries[i], found[i] = s.lookup(k, now)
	}
	return entries, found
}

type SetOptions struct {
	NX, XX bool
	TTL    int64
}

func (s *Store) Set(key, value string, opts SetOptions) (old Entry, existed, applied bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	old, existed = s.lookup(key, now)
	if opts.NX && existed || opts.XX && !existed {
		return old, existed, false, nil
	}
	e := Entry{Value: value}
	if opts.TTL != 0 {
		if opts.TTL < 0 || now > math.MaxInt64-opts.TTL {
			return old, existed, false, ErrExpiry
		}
		e.ExpiresAt = now + opts.TTL
	}
	err = s.put(key, e)
	return old, existed, err == nil, err
}

func (s *Store) SetMany(pairs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(pairs)%2 != 0 {
		return errors.New("expected key/value pairs")
	}
	m := Mutation{Changes: make([]Change, 0, len(pairs)/2)}
	for i := 0; i < len(pairs); i += 2 {
		m.Changes = append(m.Changes, change(pairs[i], Entry{Value: pairs[i+1]}))
	}
	return s.commit(m)
}

func (s *Store) Delete(keys []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	m := Mutation{}
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if _, ok := s.lookup(k, now); ok && !seen[k] {
			m.Changes = append(m.Changes, Change{Key: []byte(k), Delete: true})
			seen[k] = true
		}
	}
	if len(m.Changes) == 0 {
		return 0, nil
	}
	if err := s.commit(m); err != nil {
		return 0, err
	}
	return int64(len(m.Changes)), nil
}

func (s *Store) Increment(key string, delta int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, exists := s.lookup(key, s.now().UnixMilli())
	var n int64
	if exists {
		var err error
		n, err = strconv.ParseInt(e.Value, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != e.Value {
			return 0, ErrInteger
		}
	}
	if delta > 0 && n > math.MaxInt64-delta || delta < 0 && n < math.MinInt64-delta {
		return 0, ErrOverflow
	}
	n += delta
	e.Value = strconv.FormatInt(n, 10)
	if err := s.put(key, e); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) Expire(key string, ttl int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	if ttl > 0 && now > math.MaxInt64-ttl {
		return 0, ErrExpiry
	}
	e, ok := s.lookup(key, now)
	if !ok {
		return 0, nil
	}
	c := Change{Key: []byte(key), Delete: true}
	if ttl > 0 {
		e.ExpiresAt = now + ttl
		c = change(key, e)
	}
	if err := s.commit(Mutation{Changes: []Change{c}}); err != nil {
		return 0, err
	}
	return 1, nil
}

func (s *Store) TTL(key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	e, ok := s.lookup(key, now)
	if !ok {
		return -2
	}
	if e.ExpiresAt == 0 {
		return -1
	}
	return e.ExpiresAt - now
}

func (s *Store) Persist(key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.lookup(key, s.now().UnixMilli())
	if !ok || e.ExpiresAt == 0 {
		return 0, nil
	}
	e.ExpiresAt = 0
	if err := s.put(key, e); err != nil {
		return 0, err
	}
	return 1, nil
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commit(Mutation{Clear: true})
}

// Sweep bounds each active-expiration cycle; lazy expiration remains authoritative.
func (s *Store) Sweep(limit int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	for key := range s.entries {
		if limit <= 0 {
			break
		}
		s.lookup(key, now)
		limit--
	}
}

func (s *Store) Keys(pattern string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	keys := make([]string, 0)
	for key := range s.entries {
		if _, ok := s.lookup(key, now); ok && Match(pattern, key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

type Stats struct {
	Keys                      int
	Expired                   uint64
	Bytes                     int64
	AccountedBytes, MaxMemory int64
}

// Stats performs a full scan; use for diagnostics, not a hot-path metric.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	stats := Stats{}
	for key := range s.entries {
		if e, ok := s.lookup(key, now); ok {
			stats.Keys++
			stats.Bytes += int64(len(key) + len(e.Value))
		}
	}
	stats.Expired = s.expired
	stats.AccountedBytes, stats.MaxMemory = s.used, s.maxMemory
	return stats
}
