// Package resp implements a bounded RESP2 codec without protocol dependencies.
package resp

import (
	"io"
	"strconv"
	"strings"
)

type Kind byte

const (
	Simple  Kind = '+'
	Error   Kind = '-'
	Integer Kind = ':'
	Bulk    Kind = '$'
	Array   Kind = '*'
)

type Value struct {
	Kind   Kind
	Text   string
	Number int64
	Items  []Value
	Null   bool
}

func String(s string) Value     { return Value{Kind: Bulk, Text: s} }
func Status(s string) Value     { return Value{Kind: Simple, Text: s} }
func Err(s string) Value        { return Value{Kind: Error, Text: s} }
func Int(n int64) Value         { return Value{Kind: Integer, Number: n} }
func Nil() Value                { return Value{Kind: Bulk, Null: true} }
func List(items ...Value) Value { return Value{Kind: Array, Items: items} }

// Write encodes a value. The caller may wrap w in bufio.Writer for pipelining.
func Write(w io.Writer, v Value) error {
	write := func(s string) error { _, err := io.WriteString(w, s); return err }
	switch v.Kind {
	case Simple, Error:
		// Error messages must never inject another response into the stream.
		s := strings.NewReplacer("\r", " ", "\n", " ").Replace(v.Text)
		return write(string(byte(v.Kind)) + s + "\r\n")
	case Integer:
		return write(":" + strconv.FormatInt(v.Number, 10) + "\r\n")
	case Bulk:
		if v.Null {
			return write("$-1\r\n")
		}
		if err := write("$" + strconv.Itoa(len(v.Text)) + "\r\n"); err != nil {
			return err
		}
		if err := write(v.Text); err != nil {
			return err
		}
		return write("\r\n")
	case Array:
		if v.Null {
			return write("*-1\r\n")
		}
		if err := write("*" + strconv.Itoa(len(v.Items)) + "\r\n"); err != nil {
			return err
		}
		for _, item := range v.Items {
			if err := Write(w, item); err != nil {
				return err
			}
		}
		return nil
	default:
		return protocol("unknown response type")
	}
}
