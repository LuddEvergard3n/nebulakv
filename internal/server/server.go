// Package server owns TCP connections, deadlines and graceful shutdown.
package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nebulakv/internal/command"
	"nebulakv/internal/config"
	"nebulakv/internal/resp"
	"nebulakv/internal/storage"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	// PersistenceStatus is configured before Serve starts.
	PersistenceStatus     func() string
	config                config.Config
	store                 *storage.Store
	dispatch              command.Dispatcher
	log                   *slog.Logger
	started               time.Time
	mu                    sync.Mutex
	clients               map[net.Conn]struct{}
	wg                    sync.WaitGroup
	connections, commands atomic.Uint64
}

func New(c config.Config, store *storage.Store, log *slog.Logger) *Server {
	s := &Server{config: c, store: store, log: log, started: time.Now(), clients: make(map[net.Conn]struct{})}
	s.dispatch = command.Dispatcher{Store: store, Info: s.info}
	return s
}

// Serve takes ownership of listener and returns only after all handlers exit.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	maintenance := make(chan struct{})
	go func() {
		defer close(maintenance)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				listener.Close()
				return
			case <-ticker.C:
				s.store.Sweep(128)
			}
		}
	}()
	var serveErr error
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				serveErr = err
			}
			break
		}
		s.connections.Add(1)
		s.mu.Lock()
		if len(s.clients) >= s.config.MaxClients {
			s.mu.Unlock()
			// Reject immediately: a slow reader must never stall the accept loop.
			conn.Close()
			continue
		}
		s.clients[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handle(ctx, conn)
	}
	cancel()
	<-maintenance
	s.mu.Lock()
	for conn := range s.clients {
		conn.SetReadDeadline(time.Now())
	}
	s.mu.Unlock()
	drained := make(chan struct{})
	go func() { s.wg.Wait(); close(drained) }()
	timer := time.NewTimer(s.config.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-drained:
	case <-timer.C:
		s.mu.Lock()
		for conn := range s.clients {
			conn.Close()
		}
		s.mu.Unlock()
		<-drained
	}
	return serveErr
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer func() { conn.Close(); s.mu.Lock(); delete(s.clients, conn); s.mu.Unlock(); s.wg.Done() }()
	decoder := resp.NewDecoder(conn)
	writer := bufio.NewWriter(conn)
	authenticated := s.config.Password == ""
	failedAuth := 0
	for ctx.Err() == nil {
		if err := conn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout)); err != nil {
			return
		}
		value, err := decoder.Read()
		if err != nil {
			var networkErr net.Error
			if !errors.Is(err, io.EOF) && !(errors.As(err, &networkErr) && networkErr.Timeout()) {
				s.log.Debug("invalid client frame", "error", err)
				s.respond(conn, writer, resp.Err("ERR malformed or oversized RESP frame"))
			}
			return
		}
		args, err := resp.Command(value)
		if err != nil {
			s.respond(conn, writer, resp.Err("ERR expected array of bulk strings"))
			return
		}
		s.commands.Add(1)
		var response resp.Value
		if strings.EqualFold(args[0], "AUTH") {
			var ok bool
			ok, response = s.authenticate(args)
			authenticated = ok || s.config.Password == ""
			if !ok {
				failedAuth++
			} else {
				failedAuth = 0
			}
		} else if !authenticated && !strings.EqualFold(args[0], "QUIT") {
			response = resp.Err("NOAUTH Authentication required")
		} else {
			response = s.dispatch.Execute(args)
		}
		if err := s.respond(conn, writer, response); err != nil {
			s.log.Debug("response failed", "error", err)
			return
		}
		if strings.EqualFold(args[0], "QUIT") || failedAuth >= 5 {
			return
		}
	}
}

func (s *Server) respond(conn net.Conn, w *bufio.Writer, value resp.Value) error {
	if err := conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout)); err != nil {
		return err
	}
	if err := resp.Write(w, value); err != nil {
		return err
	}
	return w.Flush()
}

func (s *Server) info() string {
	s.mu.Lock()
	clients := len(s.clients)
	s.mu.Unlock()
	stats := s.store.Stats()
	persistence := "disabled"
	if s.config.AppendOnly {
		persistence = "enabled;fsync=always"
		if s.PersistenceStatus != nil {
			persistence += ";status=" + s.PersistenceStatus()
		}
	}
	return fmt.Sprintf("# Server\r\nnebulakv_version:0.1.0\r\nuptime_in_seconds:%d\r\n# Clients\r\nconnected_clients:%d\r\ntotal_connections_received:%d\r\n# Stats\r\ntotal_commands_processed:%d\r\nkeys:%d\r\nexpired_keys:%d\r\nkey_value_bytes:%d\r\npersistence:%s\r\n", int64(time.Since(s.started).Seconds()), clients, s.connections.Load(), s.commands.Load(), stats.Keys, stats.Expired, stats.Bytes, persistence)
}
