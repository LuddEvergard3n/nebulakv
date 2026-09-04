package resp

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

func TestRoundTripFragmented(t *testing.T) {
	values := []Value{Status("OK"), Err("ERR failed"), Int(-42), String(""), String("a\x00\xff\r\nb"), Nil(), List(String("PING")), {Kind: Array, Null: true}}
	var wire bytes.Buffer
	for _, v := range values {
		if err := Write(&wire, v); err != nil {
			t.Fatal(err)
		}
	}
	d := NewDecoder(iotest.OneByteReader(&wire))
	for _, want := range values {
		got, err := d.Read()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, %v; want %#v", got, err, want)
		}
	}
	if _, err := d.Read(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestMalformed(t *testing.T) {
	cases := []string{"", "+OK\n", "$-2\r\n", "$1048577\r\n", "*1025\r\n", ":9223372036854775808\r\n", ":+2\r\n", "$x\r\n", "$3\r\nab", "$1\r\naXX", "?hello\r\n", strings.Repeat("*1\r\n", 10) + "+x\r\n", strings.Repeat("a", 4097) + "\r\n"}
	for _, wire := range cases {
		if _, err := NewDecoder(strings.NewReader(wire)).Read(); err == nil {
			t.Fatalf("accepted %.80q", wire)
		}
	}
}

func TestCommandValidation(t *testing.T) {
	for _, v := range []Value{Nil(), List(), List(Int(1)), List(Nil())} {
		if _, err := Command(v); err == nil {
			t.Fatalf("accepted %#v", v)
		}
	}
	args, err := Command(List(String("SET"), String("x"), String("\xff")))
	if err != nil || len(args) != 3 || args[2] != "\xff" {
		t.Fatal(args, err)
	}
}

func TestFrameBudget(t *testing.T) {
	v := List()
	for range 9 {
		v.Items = append(v.Items, String(strings.Repeat("x", MaxBulk)))
	}
	var b bytes.Buffer
	if err := Write(&b, v); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDecoder(&b).Read(); err == nil {
		t.Fatal("accepted oversized frame")
	}
}

func FuzzDecoder(f *testing.F) {
	for _, seed := range []string{"*1\r\n$4\r\nPING\r\n", "$-1\r\n", ":42\r\n", "garbage"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		v, err := NewDecoder(strings.NewReader(input)).Read()
		if err != nil {
			return
		}
		var b bytes.Buffer
		if err := Write(&b, v); err != nil {
			t.Fatal(err)
		}
		if _, err := NewDecoder(&b).Read(); err != nil {
			t.Fatal(err)
		}
	})
}
