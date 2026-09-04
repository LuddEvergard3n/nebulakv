package server

import (
	"bufio"
	"context"
	"nebulakv/internal/resp"
	"net"
	"strconv"
	"strings"
	"time"
)

func (s *Server) synchronize(ctx context.Context, conn net.Conn, w *bufio.Writer, args []string) error {
	if len(args) != 2 {
		return s.respond(conn, w, resp.Err("ERR NKV.SYNC requires a revision token"))
	}
	if s.config.Primary != "" {
		return s.respond(conn, w, resp.Err("ERR replicas cannot serve replication"))
	}
	select {
	case s.syncSlot <- struct{}{}:
		defer func() { <-s.syncSlot }()
	default:
		return s.respond(conn, w, resp.Err("BUSY another snapshot transfer is active"))
	}
	prefix := s.epoch + ":"
	known, err := strconv.ParseUint(strings.TrimPrefix(args[1], prefix), 10, 64)
	force := err != nil || !strings.HasPrefix(args[1], prefix)
	snapshot, changed := s.store.SnapshotSince(known, force)
	if !changed {
		return s.respond(conn, w, resp.Status("UNCHANGED"))
	}
	token := prefix + strconv.FormatUint(snapshot.Revision, 10)
	// Bound the whole transfer, including receivers that repeatedly read slowly.
	timeout := s.config.ReplicaTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if err := resp.Write(w, resp.List(resp.String("NKV1"), resp.String(token), resp.Int(int64(len(snapshot.Entries))), resp.Int(snapshot.Bytes))); err != nil {
		return err
	}
	for key, e := range snapshot.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := resp.Write(w, resp.List(resp.String(key), resp.String(e.Value), resp.Int(e.ExpiresAt))); err != nil {
			return err
		}
	}
	if err := resp.Write(w, resp.Status("END")); err != nil {
		return err
	}
	return w.Flush()
}

func startupCommand(name string) bool {
	switch strings.ToUpper(name) {
	case "PING", "INFO", "COMMAND", "QUIT", "SELECT", "CLIENT":
		return true
	default:
		return false
	}
}
