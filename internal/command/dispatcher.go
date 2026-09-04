// Package command translates Redis-compatible commands into storage operations.
package command

import (
	"fmt"
	"math"
	"nebulakv/internal/resp"
	"nebulakv/internal/storage"
	"strconv"
	"strings"
)

type Dispatcher struct {
	Store *storage.Store
	Info  func() string
}

func failure(err error) resp.Value { return resp.Err("ERR " + err.Error()) }
func count(n int64, err error) resp.Value {
	if err != nil {
		return failure(err)
	}
	return resp.Int(n)
}

func (d *Dispatcher) Execute(args []string) resp.Value {
	if len(args) == 0 {
		return resp.Err("ERR empty command")
	}
	name := strings.ToUpper(args[0])
	spec, ok := specs[name]
	if !ok {
		return resp.Err("ERR unknown command")
	}
	if len(args) < spec.min || spec.max > 0 && len(args) > spec.max {
		return resp.Err("ERR wrong number of arguments for '" + strings.ToLower(name) + "' command")
	}
	s := d.Store
	switch name {
	case "PING":
		if len(args) == 2 {
			return resp.String(args[1])
		}
		return resp.Status("PONG")
	case "ECHO":
		return resp.String(args[1])
	case "SET":
		return d.set(args)
	case "GET":
		e, found := s.Get(args[1])
		if !found {
			return resp.Nil()
		}
		return resp.String(e.Value)
	case "MGET":
		e, found := s.GetMany(args[1:])
		items := make([]resp.Value, len(e))
		for i := range e {
			items[i] = resp.Nil()
			if found[i] {
				items[i] = resp.String(e[i].Value)
			}
		}
		return resp.List(items...)
	case "MSET":
		if len(args)%2 != 1 {
			return resp.Err("ERR wrong number of arguments for 'mset' command")
		}
		if err := s.SetMany(args[1:]); err != nil {
			return failure(err)
		}
		return resp.Status("OK")
	case "DEL":
		return count(s.Delete(args[1:]))
	case "EXISTS":
		_, found := s.GetMany(args[1:])
		var n int64
		for _, ok := range found {
			if ok {
				n++
			}
		}
		return resp.Int(n)
	case "INCR":
		return count(s.Increment(args[1], 1))
	case "DECR":
		return count(s.Increment(args[1], -1))
	case "EXPIRE", "PEXPIRE":
		n, err := parseInteger(args[2])
		if err != nil {
			return failure(err)
		}
		if name == "EXPIRE" {
			if n > math.MaxInt64/1000 || n < math.MinInt64/1000 {
				return failure(storage.ErrExpiry)
			}
			n *= 1000
		}
		return count(s.Expire(args[1], n))
	case "TTL", "PTTL":
		n := s.TTL(args[1])
		if name == "TTL" && n > 0 {
			n = n/1000 + boolInt(n%1000 >= 500)
		}
		return resp.Int(n)
	case "PERSIST":
		return count(s.Persist(args[1]))
	case "TYPE":
		_, found := s.Get(args[1])
		if found {
			return resp.Status("string")
		}
		return resp.Status("none")
	case "DBSIZE":
		return resp.Int(int64(s.Stats().Keys))
	case "KEYS":
		if len(args[1]) > 256 {
			return resp.Err("ERR pattern exceeds 256 bytes")
		}
		keys := s.Keys(args[1])
		items := make([]resp.Value, len(keys))
		for i, k := range keys {
			items[i] = resp.String(k)
		}
		return resp.List(items...)
	case "FLUSHDB":
		if err := s.Clear(); err != nil {
			return failure(err)
		}
		return resp.Status("OK")
	case "INFO":
		if d.Info != nil {
			return resp.String(d.Info())
		}
		stats := s.Stats()
		return resp.String(fmt.Sprintf("keys:%d\r\nexpired_keys:%d\r\n", stats.Keys, stats.Expired))
	case "COMMAND":
		return commandInfo(args)
	case "QUIT":
		return resp.Status("OK")
	case "SELECT":
		if args[1] == "0" {
			return resp.Status("OK")
		}
		return resp.Err("ERR only database 0 is supported")
	case "CLIENT":
		if len(args) == 4 && strings.EqualFold(args[1], "SETINFO") {
			return resp.Status("OK")
		}
		return resp.Err("ERR unsupported CLIENT subcommand")
	}
	return resp.Err("ERR command not implemented")
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
func parseInteger(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != s {
		return 0, storage.ErrInteger
	}
	return n, nil
}

func (d *Dispatcher) set(args []string) resp.Value {
	opts := storage.SetOptions{}
	get := false
	expiry := false
	for i := 3; i < len(args); i++ {
		switch strings.ToUpper(args[i]) {
		case "NX":
			if opts.XX {
				return resp.Err("ERR syntax error")
			}
			opts.NX = true
		case "XX":
			if opts.NX {
				return resp.Err("ERR syntax error")
			}
			opts.XX = true
		case "GET":
			get = true
		case "EX", "PX":
			if expiry || i+1 == len(args) {
				return resp.Err("ERR syntax error")
			}
			expiry = true
			n, err := parseInteger(args[i+1])
			if err != nil {
				return failure(err)
			}
			if n <= 0 {
				return failure(storage.ErrExpiry)
			}
			if strings.EqualFold(args[i], "EX") {
				if n > math.MaxInt64/1000 {
					return failure(storage.ErrExpiry)
				}
				n *= 1000
			}
			opts.TTL = n
			i++
		default:
			return resp.Err("ERR syntax error")
		}
	}
	old, existed, applied, err := d.Store.Set(args[1], args[2], opts)
	if err != nil {
		return failure(err)
	}
	if get {
		if existed {
			return resp.String(old.Value)
		}
		return resp.Nil()
	}
	if !applied {
		return resp.Nil()
	}
	return resp.Status("OK")
}
