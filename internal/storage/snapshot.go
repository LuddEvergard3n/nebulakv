package storage

import "errors"

type Rewriter interface{ Rewrite(map[string]Entry) error }

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
