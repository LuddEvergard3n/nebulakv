package storage

import (
	"errors"
	"testing"
)

func TestSnapshotIsolationAndFailedReplacement(t *testing.T) {
	s := New(nil)
	put(t, s, "a", "old", 0)
	snapshot, _ := s.SnapshotSince(0, true)
	put(t, s, "a", "new", 0)
	if snapshot.Entries["a"].Value != "old" {
		t.Fatal("snapshot changed with primary")
	}
	if _, changed := s.SnapshotSince(snapshot.Revision, false); !changed {
		t.Fatal("revision did not change")
	}
	s.SetJournal(brokenJournal{})
	if err := s.ReplaceSnapshot(map[string]Entry{"b": {Value: "v"}}); err == nil {
		t.Fatal("unsupported journal accepted")
	}
	s.SetJournal(nil)
	if err := s.SetMemoryLimit(101); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSnapshot(map[string]Entry{"b": {Value: "too-large"}}); !errors.Is(err, ErrMemory) {
		t.Fatal(err)
	}
	if e, _ := s.Get("a"); e.Value != "new" {
		t.Fatal("failed snapshot was partially applied")
	}
}
