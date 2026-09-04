package replication

import (
	"bytes"
	"context"
	"nebulakv/internal/resp"
	"nebulakv/internal/storage"
	"net"
	"testing"
	"time"
)

func TestReceiveRejectsIncompleteDuplicateAndOversized(t *testing.T) {
	entry := resp.List(resp.String("a"), resp.String("v"), resp.Int(0))
	for _, tc := range []struct {
		name         string
		count, total int64
		values       []resp.Value
	}{
		{"missing-end", 1, 98, []resp.Value{entry}},
		{"duplicate-key", 2, 196, []resp.Value{entry, entry, resp.Status("END")}},
		{"wrong-total", 1, 99, []resp.Value{entry, resp.Status("END")}},
		{"oversized-count", 100, 100, []resp.Value{}},
		{"negative-expiry", 1, 98, []resp.Value{resp.List(resp.String("a"), resp.String("v"), resp.Int(-1)), resp.Status("END")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			for _, v := range tc.values {
				if err := resp.Write(&b, v); err != nil {
					t.Fatal(err)
				}
			}
			header := resp.List(resp.String("NKV1"), resp.String("epoch:1"), resp.Int(tc.count), resp.Int(tc.total))
			if _, _, err := receive(resp.NewDecoder(&b), header, 1000); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
}

func TestInterruptedTransferPreservesVisibleState(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := resp.NewDecoder(conn).Read(); err != nil {
			return
		}
		resp.Write(conn, resp.List(resp.String("NKV1"), resp.String("epoch:1"), resp.Int(2), resp.Int(200)))
		resp.Write(conn, resp.List(resp.String("new"), resp.String("v"), resp.Int(0)))
	}()
	s := storage.New(nil)
	if err := s.SetMany([]string{"keep", "old"}); err != nil {
		t.Fatal(err)
	}
	c := &Client{Address: ln.Addr().String(), Timeout: time.Second, MaxMemory: 1000, Store: s}
	if err := c.SyncOnce(context.Background()); err == nil || c.Ready() {
		t.Fatal("partial transfer applied", err)
	}
	<-done
	if e, ok := s.Get("keep"); !ok || e.Value != "old" || s.Stats().Keys != 1 {
		t.Fatal(e, ok)
	}
}
