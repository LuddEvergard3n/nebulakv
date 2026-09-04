package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"nebulakv/internal/config"
	"nebulakv/internal/persistence"
	"nebulakv/internal/replication"
	"nebulakv/internal/server"
	"nebulakv/internal/storage"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func run() (runErr error) {
	c, err := config.Parse(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: c.Level}))
	s := storage.New(nil)
	if err := s.SetMemoryLimit(c.MaxMemory); err != nil {
		return err
	}
	var journal *persistence.Log
	if c.AppendOnly {
		journal, err = persistence.Open(c.Data, s.Replay)
		if err != nil {
			return err
		}
		s.SetJournal(journal)
		defer func() { runErr = errors.Join(runErr, journal.Close()) }()
	}
	listener, err := net.Listen("tcp", c.Address())
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("NebulaKV listening", "address", listener.Addr().String(), "appendonly", c.AppendOnly)
	srv := server.New(c, s, logger)
	if c.Primary != "" {
		client := &replication.Client{Address: c.Primary, Password: c.PrimaryPassword, Interval: c.ReplicaInterval, Timeout: c.ReplicaTimeout, MaxMemory: c.MaxMemory, Store: s}
		srv.ReplicationInfo = client.Info
		srv.ReplicationReady = client.Ready
		replicaCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); client.Run(replicaCtx) }()
		defer func() { cancel(); <-done }()
	}
	if journal != nil {
		srv.PersistenceStatus = journal.Status
		srv.PersistenceInfo = func() string {
			stats := journal.Stats()
			return fmt.Sprintf("aof_bytes:%d\r\naof_rewrites:%d\r\n", stats.Bytes, stats.Rewrites)
		}
		srv.AutoRewrite = func() error {
			if journal.NeedsRewrite(c.RewriteSize) {
				return s.Rewrite()
			}
			return nil
		}
	}
	return srv.Serve(ctx, listener)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "nebulakv:", err)
		os.Exit(1)
	}
}
