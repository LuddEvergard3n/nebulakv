package persistence

import (
	"errors"
	"fmt"
	"io"
	"nebulakv/internal/storage"
	"os"
	"path/filepath"
)

// Rewrite replaces history with the current live map. The caller must keep that
// map stable until return. A stable sidecar lock protects the close/rename gap.
func (l *Log) Rewrite(entries map[string]storage.Entry) (err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failure != nil {
		return fmt.Errorf("persistence unavailable: %w", l.failure)
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".nebulakv-rewrite-*")
	if err != nil {
		return err
	}
	tempPath := tmp.Name()
	defer func() {
		tmp.Close()
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if _, err = io.WriteString(tmp, magic); err != nil {
		return err
	}
	for key, e := range entries {
		m := storage.Mutation{Changes: []storage.Change{{Key: []byte(key), Value: []byte(e.Value), ExpiresAt: e.ExpiresAt}}}
		if err = writeRecord(tmp, m); err != nil {
			return err
		}
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = l.file.Close(); err != nil {
		l.failure = err
		return err
	}
	replaceErr := l.replace(tempPath, l.path)
	// Reopen either the new journal or the unchanged old journal after a failed rename.
	f, openErr := os.OpenFile(l.path, os.O_RDWR, 0600)
	if openErr == nil {
		openErr = lock(f)
	}
	var size int64
	if openErr == nil {
		size, openErr = f.Seek(0, io.SeekEnd)
	}
	if openErr != nil {
		if f != nil {
			f.Close()
		}
		l.failure = openErr
		return errors.Join(replaceErr, openErr)
	}
	l.file = f
	if replaceErr != nil {
		return replaceErr
	}
	l.bytes = size
	l.baseSize = size
	l.rewrites++
	if err = syncDirectory(filepath.Dir(l.path)); err != nil {
		l.failure = err
		return err
	}
	return nil
}

func (l *Log) NeedsRewrite(threshold int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failure == nil && threshold > 0 && l.bytes >= threshold && l.bytes >= 2*l.baseSize
}

type Stats struct {
	Bytes    int64
	Rewrites uint64
}

func (l *Log) Stats() Stats { l.mu.Lock(); defer l.mu.Unlock(); return Stats{l.bytes, l.rewrites} }
