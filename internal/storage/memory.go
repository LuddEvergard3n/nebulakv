package storage

import "errors"

const DefaultMaxMemory int64 = 64 << 20

// EntryOverhead is a conservative accounting allowance, not a measured RSS size.
const EntryOverhead int64 = 96

var ErrMemory = errors.New("memory limit exceeded; write rejected")

func Cost(key string, e Entry) int64 { return int64(len(key)+len(e.Value)) + EntryOverhead }

func (s *Store) remove(key string) {
	if old, ok := s.entries[key]; ok {
		s.used -= Cost(key, old)
		delete(s.entries, key)
	}
}

func (s *Store) assign(key string, e Entry) {
	s.remove(key)
	s.entries[key] = e
	s.used += Cost(key, e)
}

func (s *Store) purgeExpired() {
	now := s.now().UnixMilli()
	for key := range s.entries {
		s.lookup(key, now)
	}
}

func (s *Store) SetMemoryLimit(limit int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		return errors.New("memory limit must be positive")
	}
	s.purgeExpired()
	if s.used > limit {
		return ErrMemory
	}
	s.maxMemory = limit
	return nil
}

func (s *Store) canPut(key string, e Entry) bool {
	next := s.used + Cost(key, e)
	if old, ok := s.entries[key]; ok {
		next -= Cost(key, old)
	}
	return next <= s.maxMemory
}

func (s *Store) admitPut(key string, e Entry) error {
	if s.canPut(key, e) {
		return nil
	}
	s.purgeExpired()
	if !s.canPut(key, e) {
		return ErrMemory
	}
	return nil
}

func (s *Store) mutationCost(m Mutation) int64 {
	if m.Clear {
		return 0
	}
	final := make(map[string]Change, len(m.Changes))
	for _, c := range m.Changes {
		final[string(c.Key)] = c
	}
	next := s.used
	for key, c := range final {
		if old, ok := s.entries[key]; ok {
			next -= Cost(key, old)
		}
		if !c.Delete {
			next += Cost(key, Entry{Value: string(c.Value)})
		}
	}
	return next
}

func (s *Store) admit(m Mutation) error {
	if s.mutationCost(m) <= s.maxMemory {
		return nil
	}
	s.purgeExpired()
	if s.mutationCost(m) > s.maxMemory {
		return ErrMemory
	}
	return nil
}
