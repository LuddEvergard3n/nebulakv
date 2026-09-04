package command

import (
	"nebulakv/internal/resp"
	"sort"
	"strings"
)

type spec struct {
	min, max, first, last, step int
	write                       bool
}

var specs = map[string]spec{
	"AUTH": {2, 3, 0, 0, 0, false}, "REWRITEAOF": {1, 1, 0, 0, 0, false},
	"PING": {1, 2, 0, 0, 0, false}, "ECHO": {2, 2, 0, 0, 0, false},
	"SET": {3, 0, 1, 1, 1, true}, "GET": {2, 2, 1, 1, 1, false},
	"MSET": {3, 0, 1, -1, 2, true}, "MGET": {2, 0, 1, -1, 1, false},
	"DEL": {2, 0, 1, -1, 1, true}, "EXISTS": {2, 0, 1, -1, 1, false},
	"INCR": {2, 2, 1, 1, 1, true}, "DECR": {2, 2, 1, 1, 1, true},
	"EXPIRE": {3, 3, 1, 1, 1, true}, "PEXPIRE": {3, 3, 1, 1, 1, true},
	"TTL": {2, 2, 1, 1, 1, false}, "PTTL": {2, 2, 1, 1, 1, false},
	"PERSIST": {2, 2, 1, 1, 1, true}, "TYPE": {2, 2, 1, 1, 1, false},
	"DBSIZE": {1, 1, 0, 0, 0, false}, "KEYS": {2, 2, 0, 0, 0, false},
	"FLUSHDB": {1, 1, 0, 0, 0, true}, "INFO": {1, 1, 0, 0, 0, false},
	"COMMAND": {1, 0, 0, 0, 0, false}, "QUIT": {1, 1, 0, 0, 0, false},
	"SELECT": {2, 2, 0, 0, 0, false}, "CLIENT": {2, 0, 0, 0, 0, false},
}

func describe(name string) resp.Value {
	s, ok := specs[strings.ToUpper(name)]
	if !ok {
		return resp.Nil()
	}
	arity := s.min
	if s.max != s.min {
		arity = -arity
	}
	flag := "readonly"
	if s.write {
		flag = "write"
	}
	return resp.List(resp.String(strings.ToLower(name)), resp.Int(int64(arity)), resp.List(resp.String(flag)), resp.Int(int64(s.first)), resp.Int(int64(s.last)), resp.Int(int64(s.step)))
}

func commandInfo(args []string) resp.Value {
	if len(args) > 1 {
		switch strings.ToUpper(args[1]) {
		case "COUNT":
			if len(args) == 2 {
				return resp.Int(int64(len(specs)))
			}
		case "INFO":
			items := make([]resp.Value, 0, len(args)-2)
			for _, s := range args[2:] {
				items = append(items, describe(s))
			}
			return resp.List(items...)
		default:
			return resp.Err("ERR unsupported COMMAND subcommand")
		}
		return resp.Err("ERR wrong number of arguments")
	}
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]resp.Value, 0, len(names))
	for _, name := range names {
		items = append(items, describe(name))
	}
	return resp.List(items...)
}
