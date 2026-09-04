package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"nebulakv/internal/config"
	"nebulakv/internal/server"
	"nebulakv/internal/storage"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func run() error {
	c, err := config.Parse(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: c.Level}))
	s := storage.New(nil)
	if c.AppendOnly {
		return errors.New("persistence milestone is not installed yet")
	}
	listener, err := net.Listen("tcp", c.Address())
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("NebulaKV listening", "address", listener.Addr().String(), "appendonly", c.AppendOnly)
	return server.New(c, s, logger).Serve(ctx, listener)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "nebulakv:", err)
		os.Exit(1)
	}
}
