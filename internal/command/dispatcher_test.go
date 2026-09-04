package command

import (
	"nebulakv/internal/resp"
	"nebulakv/internal/storage"
	"reflect"
	"testing"
	"time"
)

func TestCommands(t *testing.T) {
	now := time.UnixMilli(100000)
	d := Dispatcher{Store: storage.New(func() time.Time { return now })}
	cases := []struct {
		args []string
		want resp.Value
	}{
		{[]string{"ping"}, resp.Status("PONG")}, {[]string{"PING", "hi"}, resp.String("hi")},
		{[]string{"ECHO", "\x00\xff"}, resp.String("\x00\xff")}, {[]string{"GET", "x"}, resp.Nil()},
		{[]string{"SET", "x", "v", "PX", "1500"}, resp.Status("OK")},
		{[]string{"PTTL", "x"}, resp.Int(1500)}, {[]string{"TTL", "x"}, resp.Int(2)},
		{[]string{"SET", "x", "new", "NX", "GET"}, resp.String("v")}, {[]string{"GET", "x"}, resp.String("v")},
		{[]string{"SET", "x", "new", "XX", "GET"}, resp.String("v")}, {[]string{"TTL", "x"}, resp.Int(-1)},
		{[]string{"SET", "absent", "v", "XX"}, resp.Nil()},
		{[]string{"EXISTS", "x", "x", "absent"}, resp.Int(2)},
		{[]string{"MSET", "a", "1", "b", "2"}, resp.Status("OK")},
		{[]string{"MGET", "a", "absent", "b"}, resp.List(resp.String("1"), resp.Nil(), resp.String("2"))},
		{[]string{"INCR", "a"}, resp.Int(2)}, {[]string{"DECR", "b"}, resp.Int(1)},
		{[]string{"TYPE", "a"}, resp.Status("string")}, {[]string{"TYPE", "absent"}, resp.Status("none")},
		{[]string{"DBSIZE"}, resp.Int(3)}, {[]string{"KEYS", "[ab]"}, resp.List(resp.String("a"), resp.String("b"))},
		{[]string{"EXPIRE", "a", "1"}, resp.Int(1)}, {[]string{"PERSIST", "a"}, resp.Int(1)},
		{[]string{"DEL", "a", "a", "absent"}, resp.Int(1)},
		{[]string{"PEXPIRE", "b", "-1"}, resp.Int(1)}, {[]string{"GET", "b"}, resp.Nil()},
		{[]string{"FLUSHDB"}, resp.Status("OK")}, {[]string{"DBSIZE"}, resp.Int(0)},
	}
	for _, tc := range cases {
		if got := d.Execute(tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %#v want %#v", tc.args, got, tc.want)
		}
	}
}

func TestInvalidCommandsDoNotWrite(t *testing.T) {
	d := Dispatcher{Store: storage.New(nil)}
	for _, args := range [][]string{
		{}, {"NOPE"}, {"SET", "key"}, {"GET"}, {"MSET", "a", "1", "b"}, {"SET", "a", "1", "NX", "XX"},
		{"SET", "a", "1", "PX"}, {"SET", "a", "1", "EX", "0"}, {"SET", "a", "1", "PX", "2", "EX", "3"},
		{"SET", "a", "1", "EX", "9223372036854775807"}, {"EXPIRE", "a", "wat"},
		{"SET", "a", "1", "GET", "WAT"}, {"SELECT", "1"},
	} {
		if got := d.Execute(args); got.Kind != resp.Error {
			t.Errorf("%q accepted: %#v", args, got)
		}
	}
	if d.Store.Stats().Keys != 0 {
		t.Fatal("invalid command mutated storage")
	}
}

func TestCatalogAndInfo(t *testing.T) {
	d := Dispatcher{Store: storage.New(nil), Info: func() string { return "uptime_in_seconds:0\r\n" }}
	if d.Execute([]string{"INFO"}).Text != "uptime_in_seconds:0\r\n" {
		t.Fatal("info")
	}
	all := d.Execute([]string{"COMMAND"})
	if len(all.Items) != len(specs) {
		t.Fatal(all)
	}
	info := d.Execute([]string{"COMMAND", "INFO", "SET", "unknown"})
	if len(info.Items) != 2 || !info.Items[1].Null {
		t.Fatal(info)
	}
	for name, s := range specs {
		if s.min > 1 {
			if d.Execute([]string{name}).Kind != resp.Error {
				t.Fatal("arity", name)
			}
		}
	}
}
