// Package persistence implements a versioned, checksummed append-only journal.
package persistence

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"nebulakv/internal/resp"
	"nebulakv/internal/storage"
	"os"
	"path/filepath"
	"sync"
)

const magic = "NKV1\n"
const maxRecord = 24 << 20

type Log struct {
	mu      sync.Mutex
	file    *os.File
	failure error
}

// Open locks the journal for its lifetime, replays valid records and truncates
// only an incomplete final record. Complete corrupt records fail closed.
func Open(dir string, apply func(storage.Mutation)) (*Log, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "appendonly.aof"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	if err := lock(f); err != nil {
		return nil, fmt.Errorf("journal already in use or cannot be locked: %w", err)
	}
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Size() == 0 {
		if _, err := io.WriteString(f, magic); err != nil {
			return nil, err
		}
		if err := f.Sync(); err != nil {
			return nil, err
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	header := make([]byte, len(magic))
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, err
	}
	if string(header) != magic {
		return nil, errors.New("invalid journal header")
	}
	if err := replay(f, apply); err != nil {
		return nil, err
	}
	ok = true
	return &Log{file: f}, nil
}

func replay(f *os.File, apply func(storage.Mutation)) error {
	offset := int64(len(magic))
	for {
		var header [8]byte
		n, err := io.ReadFull(f, header[:])
		if errors.Is(err, io.EOF) && n == 0 {
			break
		}
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return truncateTail(f, offset)
			}
			return err
		}
		size := binary.BigEndian.Uint32(header[:4])
		if size == 0 || size > maxRecord {
			return fmt.Errorf("invalid journal record length at %d", offset)
		}
		payload := make([]byte, int(size))
		if _, err := io.ReadFull(f, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return truncateTail(f, offset)
			}
			return err
		}
		if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(header[4:]) {
			return fmt.Errorf("journal checksum mismatch at %d", offset)
		}
		var m storage.Mutation
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&m); err != nil {
			return fmt.Errorf("invalid journal record at %d: %w", offset, err)
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return errors.New("extra journal record content")
		}
		if err := validate(m); err != nil {
			return fmt.Errorf("invalid journal record at %d: %w", offset, err)
		}
		apply(m)
		offset += 8 + int64(size)
	}
	_, err := f.Seek(offset, io.SeekStart)
	return err
}

func truncateTail(f *os.File, offset int64) error {
	if err := f.Truncate(offset); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	_, err := f.Seek(offset, io.SeekStart)
	return err
}

func validate(m storage.Mutation) error {
	if m.Clear && len(m.Changes) != 0 || !m.Clear && len(m.Changes) == 0 || len(m.Changes) > resp.MaxArray {
		return errors.New("invalid mutation")
	}
	for _, c := range m.Changes {
		if len(c.Key) > resp.MaxBulk || len(c.Value) > resp.MaxBulk || c.ExpiresAt < 0 || c.Delete && (len(c.Value) > 0 || c.ExpiresAt != 0) {
			return errors.New("invalid change")
		}
	}
	return nil
}

func (l *Log) Append(m storage.Mutation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failure != nil {
		return fmt.Errorf("persistence unavailable: %w", l.failure)
	}
	if err := validate(m); err != nil {
		return err
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(payload) > maxRecord {
		return errors.New("journal record too large")
	}
	frame := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	copy(frame[8:], payload)
	n, err := l.file.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = l.file.Sync()
	}
	if err != nil {
		l.failure = err
		return fmt.Errorf("persistence write failed: %w", err)
	}
	return nil
}

func (l *Log) Status() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failure != nil {
		return "error"
	}
	return "ok"
}
func (l *Log) Close() error { l.mu.Lock(); defer l.mu.Unlock(); return l.file.Close() }
