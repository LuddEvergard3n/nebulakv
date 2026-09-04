package storage

import "errors"

type Rewriter interface{ Rewrite(map[string]Entry) error }

type Snapshot struct {
	Entries  map[string]Entry
	Revision uint64
	Bytes    int64
}

// SnapshotSince copies only map metadata; immutable strings can be shared safely.
func (s *Store) SnapshotSince(revision uint64, force bool) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && revision == s.revision {
		return Snapshot{Revision: s.revision}, false
	}
	s.purgeExpired()
	entries := make(map[string]Entry, len(s.entries))
	for k, e := range s.entries {
		entries[k] = e
	}
	return Snapshot{entries, s.revision, s.used}, true
}

// ReplaceSnapshot takes ownership of entries only after durable replacement.
// Callers must not access or mutate the map after a successful return.
func (s *Store) ReplaceSnapshot(entries map[string]Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	var used int64
	for k, e := range entries {
		if e.ExpiresAt != 0 && e.ExpiresAt <= now {
			delete(entries, k)
			continue
		}
		used += Cost(k, e)
		if used > s.maxMemory {
			return ErrMemory
		}
	}
	if s.journal != nil {
		r, ok := s.journal.(Rewriter)
		if !ok {
			return errors.New("journal does not support snapshot replacement")
		}
		if err := r.Rewrite(entries); err != nil {
			return err
		}
	}
	s.entries = entries
	s.used = used
	s.revision++
	return nil
}

// Rewrite holds the store lock until the journal replacement is complete.
func (s *Store) Rewrite() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.journal.(Rewriter)
	if !ok {
		return errors.New("append-only persistence is disabled")
	}
	s.purgeExpired()
	return r.Rewrite(s.entries)
}
