// Package replication implements revision-aware, atomic full-snapshot replication.
// It is a NebulaKV protocol, not Redis PSYNC or a consensus/failover system.
package replication

import (
	"context"
	"errors"
	"fmt"
	"nebulakv/internal/resp"
	"nebulakv/internal/storage"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	Address, Password       string
	Interval, Timeout       time.Duration
	MaxMemory               int64
	Store                   *storage.Store
	mu, syncMu              sync.Mutex
	token, state, lastError string
	syncs                   uint64
	lastSuccess             time.Time
}

func (c *Client) Run(ctx context.Context) {
	for ctx.Err() == nil {
		c.SyncOnce(ctx) // Status retains errors; a failed transfer is retried next interval.
		timer := time.NewTimer(c.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (c *Client) SyncOnce(ctx context.Context) (syncErr error) {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if syncErr != nil {
			c.state = "disconnected"
			c.lastError = syncErr.Error()
		} else {
			c.state = "up"
			c.lastError = ""
			c.lastSuccess = time.Now()
		}
	}()
	conn, err := (&net.Dialer{Timeout: c.Timeout}).DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(c.Timeout)); err != nil {
		return err
	}
	d := resp.NewDecoder(conn)
	if c.Password != "" {
		if err := request(conn, "AUTH", c.Password); err != nil {
			return err
		}
		v, err := d.Read()
		if err != nil {
			return err
		}
		if v.Kind != resp.Simple || v.Text != "OK" {
			return errors.New("primary authentication failed")
		}
	}
	c.mu.Lock()
	token := c.token
	c.mu.Unlock()
	if err := request(conn, "NKV.SYNC", token); err != nil {
		return err
	}
	first, err := d.Read()
	if err != nil {
		return err
	}
	if first.Kind == resp.Simple && first.Text == "UNCHANGED" {
		if token == "" {
			return errors.New("unchanged without initial snapshot")
		}
		return nil
	}
	next, entries, err := receive(d, first, c.MaxMemory)
	if err != nil {
		return err
	}
	if err := c.Store.ReplaceSnapshot(entries); err != nil {
		return err
	}
	c.mu.Lock()
	c.token = next
	c.syncs++
	c.mu.Unlock()
	return nil
}

func request(conn net.Conn, args ...string) error {
	items := make([]resp.Value, len(args))
	for i, s := range args {
		items[i] = resp.String(s)
	}
	return resp.Write(conn, resp.List(items...))
}

func receive(d *resp.Decoder, header resp.Value, limit int64) (string, map[string]storage.Entry, error) {
	bad := errors.New("invalid or oversized replication snapshot")
	if header.Kind != resp.Array || len(header.Items) != 4 {
		return "", nil, bad
	}
	h := header.Items
	if h[0].Kind != resp.Bulk || h[0].Text != "NKV1" || h[1].Kind != resp.Bulk || h[1].Null || len(h[1].Text) == 0 || len(h[1].Text) > 128 || h[2].Kind != resp.Integer || h[3].Kind != resp.Integer {
		return "", nil, bad
	}
	count, total := h[2].Number, h[3].Number
	if limit <= 0 || count < 0 || count > limit/storage.EntryOverhead || total < 0 || total > limit {
		return "", nil, bad
	}
	entries := make(map[string]storage.Entry)
	var used int64
	for i := int64(0); i < count; i++ {
		v, err := d.Read()
		if err != nil {
			return "", nil, err
		}
		if v.Kind != resp.Array || len(v.Items) != 3 {
			return "", nil, bad
		}
		p := v.Items
		if p[0].Kind != resp.Bulk || p[0].Null || p[1].Kind != resp.Bulk || p[1].Null || p[2].Kind != resp.Integer || p[2].Number < 0 {
			return "", nil, bad
		}
		key := p[0].Text
		e := storage.Entry{Value: p[1].Text, ExpiresAt: p[2].Number}
		if _, exists := entries[key]; exists {
			return "", nil, bad
		}
		used += storage.Cost(key, e)
		if used > limit {
			return "", nil, bad
		}
		entries[key] = e
	}
	end, err := d.Read()
	if err != nil {
		return "", nil, err
	}
	if end.Kind != resp.Simple || end.Text != "END" || used != total {
		return "", nil, bad
	}
	return h[1].Text, entries, nil
}

func (c *Client) Info() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.state
	if state == "" {
		state = "connecting"
	}
	age := "-1"
	if !c.lastSuccess.IsZero() {
		age = strconv.FormatInt(int64(time.Since(c.lastSuccess).Seconds()), 10)
	}
	err := strings.NewReplacer("\r", " ", "\n", " ").Replace(c.lastError)
	return fmt.Sprintf("role:replica\r\nprimary_link_status:%s\r\nreplica_syncs:%d\r\nreplica_last_success_seconds:%s\r\nreplica_last_error:%s\r\n", state, c.syncs, age, err)
}

func (c *Client) Ready() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.token != "" }
