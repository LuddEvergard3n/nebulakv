package resp

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
)

const (
	MaxBulk   = 1 << 20
	MaxFrame  = 8 << 20
	MaxArray  = 1024
	MaxDepth  = 8
	MaxValues = 4096
)

type Decoder struct {
	reader            *bufio.Reader
	remaining, values int
}

func NewDecoder(r io.Reader) *Decoder { return &Decoder{reader: bufio.NewReaderSize(r, 4096)} }
func protocol(s string) error         { return fmt.Errorf("protocol error: %s", s) }

func (d *Decoder) Read() (Value, error) {
	d.remaining, d.values = MaxFrame, MaxValues
	return d.read(0)
}

func (d *Decoder) consume(n int) error {
	if n > d.remaining {
		return protocol("frame too large")
	}
	d.remaining -= n
	return nil
}

func (d *Decoder) line() (string, error) {
	b, err := d.reader.ReadSlice('\n')
	if err != nil {
		return "", err
	}
	if err = d.consume(len(b)); err != nil {
		return "", err
	}
	if len(b) < 2 || b[len(b)-2] != '\r' {
		return "", protocol("expected CRLF")
	}
	for _, c := range b[:len(b)-2] {
		if c == '\r' {
			return "", protocol("unexpected CR")
		}
	}
	return string(b[:len(b)-2]), nil
}

func number(s string) (int64, error) {
	if s == "" {
		return 0, protocol("empty integer")
	}
	start := 0
	if s[0] == '-' {
		start = 1
	}
	if start == len(s) {
		return 0, protocol("invalid integer")
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, protocol("invalid integer")
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, protocol("integer out of range")
	}
	return n, nil
}

func (d *Decoder) read(depth int) (Value, error) {
	if depth > MaxDepth || d.values == 0 {
		return Value{}, protocol("too many nested values")
	}
	d.values--
	line, err := d.line()
	if err != nil {
		return Value{}, err
	}
	if len(line) == 0 {
		return Value{}, protocol("missing type")
	}
	v := Value{Kind: Kind(line[0])}
	switch v.Kind {
	case Simple, Error:
		v.Text = line[1:]
	case Integer:
		v.Number, err = number(line[1:])
	case Bulk, Array:
		var n int64
		n, err = number(line[1:])
		if err != nil {
			break
		}
		if n == -1 {
			v.Null = true
			break
		}
		if n < 0 {
			return Value{}, protocol("invalid length")
		}
		if v.Kind == Bulk {
			if n > MaxBulk {
				return Value{}, protocol("bulk string too large")
			}
			if err = d.consume(int(n) + 2); err != nil {
				break
			}
			b := make([]byte, int(n)+2)
			if _, err = io.ReadFull(d.reader, b); err != nil {
				break
			}
			if b[n] != '\r' || b[n+1] != '\n' {
				return Value{}, protocol("invalid bulk terminator")
			}
			v.Text = string(b[:n])
		} else {
			if n > MaxArray || n > int64(d.values) {
				return Value{}, protocol("array too large")
			}
			v.Items = make([]Value, int(n))
			for i := range v.Items {
				v.Items[i], err = d.read(depth + 1)
				if err != nil {
					break
				}
			}
		}
	default:
		err = protocol("unknown type")
	}
	return v, err
}

func Command(v Value) ([]string, error) {
	if v.Kind != Array || v.Null || len(v.Items) == 0 {
		return nil, protocol("expected nonempty command array")
	}
	args := make([]string, len(v.Items))
	for i, item := range v.Items {
		if item.Kind != Bulk || item.Null {
			return nil, protocol("command arguments must be bulk strings")
		}
		args[i] = item.Text
	}
	return args, nil
}
