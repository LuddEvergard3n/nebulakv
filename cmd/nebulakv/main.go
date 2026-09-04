package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"nebulakv/internal/config"
	"nebulakv/internal/persistence"
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
	if journal != nil {
		srv.PersistenceStatus = journal.Status
	}
	return srv.Serve(ctx, listener)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "nebulakv:", err)
		os.Exit(1)
	}
}
