package server

import (
	"context"
	"io"
	"log/slog"
	"nebulakv/internal/config"
	"nebulakv/internal/persistence"
	"nebulakv/internal/replication"
	"nebulakv/internal/resp"
	"nebulakv/internal/storage"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReplicationSnapshotRestartAndReadOnly(t *testing.T) {
	var clockMS atomic.Int64
	clockMS.Store(100000)
	clock := func() time.Time { return time.UnixMilli(clockMS.Load()) }
	primary := storage.New(clock)
	cfg := config.Config{Password: "test-secret", MaxClients: 8, ReadTimeout: time.Second, WriteTimeout: time.Second, ShutdownTimeout: time.Second}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	address, cancel, done := startServer(t, New(cfg, primary, logger))
	if err := primary.SetMany([]string{"a", "1", "\xff", "\x00\xfe"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := primary.Set("ttl", "v", storage.SetOptions{TTL: 1000}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	replica := storage.New(clock)
	journal, err := persistence.Open(dir, replica.Replay)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { journal.Close() }()
	replica.SetJournal(journal)
	client := &replication.Client{Address: address, Password: "test-secret", Timeout: time.Second, MaxMemory: 1 << 20, Store: replica}
	replicaCfg := cfg
	replicaCfg.Primary = address
	replicaServer := New(replicaCfg, replica, logger)
	replicaServer.ReplicationReady = client.Ready
	replicaAddress, _, _ := startServer(t, replicaServer)
	conn := dial(t, replicaAddress)
	d := resp.NewDecoder(conn)
	for _, args := range [][]string{{"AUTH", "test-secret"}, {"GET", "a"}} {
		if err := send(conn, args...); err != nil {
			t.Fatal(err)
		}
		v, err := d.Read()
		if err != nil {
			t.Fatal(err)
		}
		if args[0] == "GET" && !strings.HasPrefix(v.Text, "LOADING") {
			t.Fatal(v)
		}
	}
	if err := client.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !client.Ready() {
		t.Fatal("replica not ready")
	}
	if e, ok := replica.Get("\xff"); !ok || e.Value != "\x00\xfe" {
		t.Fatal(e, ok)
	}
	if err := send(conn, "SET", "a", "bad"); err != nil {
		t.Fatal(err)
	}
	v, err := d.Read()
	if err != nil || !strings.HasPrefix(v.Text, "READONLY") {
		t.Fatal(v, err)
	}
	firstRewrites := journal.Stats().Rewrites
	if err := client.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if journal.Stats().Rewrites != firstRewrites {
		t.Fatal("unchanged snapshot rewrote disk")
	}
	clockMS.Add(250)
	if _, err := primary.Delete([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := client.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := replica.Get("a"); ok || replica.TTL("ttl") != 750 {
		t.Fatal("deletion or TTL did not replicate", replica.TTL("ttl"))
	}
	cancel()
	<-done
	if err := client.SyncOnce(context.Background()); err == nil {
		t.Fatal("offline primary accepted")
	}
	if e, ok := replica.Get("\xff"); !ok || e.Value != "\x00\xfe" {
		t.Fatal("offline replica lost data")
	}
	// A fresh primary process uses a fresh epoch even when its revision is lower.
	replacement := storage.New(clock)
	if err := replacement.SetMany([]string{"new", "value"}); err != nil {
		t.Fatal(err)
	}
	newAddress, _, _ := startServer(t, New(cfg, replacement, logger))
	client.Address = newAddress
	if err := client.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if replica.Stats().Keys != 1 {
		t.Fatal("old-primary data survived full synchronization")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded := storage.New(clock)
	journal, err = persistence.Open(dir, reloaded.Replay)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := reloaded.Get("new"); !ok || e.Value != "value" {
		t.Fatal("replica persistence failed", e, ok)
	}
}

func TestReplicationAuthBudgetAndCancellation(t *testing.T) {
	primary := storage.New(nil)
	if err := primary.SetMany([]string{"a", strings.Repeat("x", 200)}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Password: "secret", MaxClients: 8, ReadTimeout: time.Second, WriteTimeout: time.Second, ShutdownTimeout: time.Second}
	address, _, _ := startServer(t, New(cfg, primary, slog.New(slog.NewTextHandler(io.Discard, nil))))
	replica := storage.New(nil)
	if err := replica.SetMany([]string{"keep", "old"}); err != nil {
		t.Fatal(err)
	}
	client := &replication.Client{Address: address, Password: "wrong", Timeout: time.Second, MaxMemory: 100, Store: replica, Interval: time.Millisecond}
	if err := client.SyncOnce(context.Background()); err == nil {
		t.Fatal("wrong password accepted")
	}
	client.Password = "secret"
	if err := client.SyncOnce(context.Background()); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
	if e, ok := replica.Get("keep"); !ok || e.Value != "old" {
		t.Fatal("failed sync changed replica")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); client.Run(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("replication did not cancel")
	}
}

func TestReplicationStreamsMoreThanRESPArrayLimit(t *testing.T) {
	primary := storage.New(nil)
	pairs := make([]string, 0, 2200)
	for i := range 1100 {
		pairs = append(pairs, "key-"+strconv.Itoa(i), "value")
	}
	if err := primary.SetMany(pairs); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MaxClients: 4, ReadTimeout: time.Second, WriteTimeout: time.Second, ShutdownTimeout: time.Second}
	address, _, _ := startServer(t, New(cfg, primary, slog.New(slog.NewTextHandler(io.Discard, nil))))
	replica := storage.New(nil)
	client := &replication.Client{Address: address, Timeout: 3 * time.Second, MaxMemory: 1 << 20, Store: replica}
	if err := client.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if replica.Stats().Keys != 1100 {
		t.Fatal(replica.Stats())
	}
}

func TestAutomaticRewriteWorker(t *testing.T) {
	s := storage.New(nil)
	journal, err := persistence.Open(t.TempDir(), s.Replay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close() })
	s.SetJournal(journal)
	for range 10 {
		if _, _, _, err := s.Set("key", strings.Repeat("x", 1000), storage.SetOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	before := journal.Stats().Bytes
	cfg := config.Config{MaxClients: 4, ReadTimeout: time.Second, WriteTimeout: time.Second, ShutdownTimeout: time.Second}
	srv := New(cfg, s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rewritten := make(chan error, 1)
	srv.AutoRewrite = func() error {
		if journal.NeedsRewrite(1000) {
			err := s.Rewrite()
			rewritten <- err
			return err
		}
		return nil
	}
	startServer(t, srv)
	select {
	case err := <-rewritten:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("automatic rewrite did not run")
	}
	if journal.Stats().Bytes >= before {
		t.Fatal("automatic rewrite did not compact")
	}
}
