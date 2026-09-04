package config

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"strconv"
	"time"
)

type Config struct {
	Primary, PrimaryPassword                   string
	ReplicaInterval, ReplicaTimeout            time.Duration
	RewriteSize                                int64
	Host                                       string
	Port                                       int
	AppendOnly                                 bool
	Data                                       string
	Level                                      slog.Level
	MaxClients                                 int
	MaxMemory                                  int64
	Password                                   string
	ReadTimeout, WriteTimeout, ShutdownTimeout time.Duration
}

func Parse(args []string, out io.Writer) (Config, error) {
	c := Config{}
	f := flag.NewFlagSet("nebulakv", flag.ContinueOnError)
	f.SetOutput(out)
	f.StringVar(&c.Host, "host", "127.0.0.1", "TCP bind address")
	f.StringVar(&c.Primary, "replicaof", "", "primary host:port; enable read-only asynchronous snapshot replication")
	primaryPasswordFile := f.String("primary-password-file", "", "password file used to authenticate to the primary")
	f.DurationVar(&c.ReplicaInterval, "replica-interval", time.Second, "interval between primary revision checks")
	f.DurationVar(&c.ReplicaTimeout, "replica-timeout", 30*time.Second, "deadline for a complete snapshot transfer")
	f.IntVar(&c.Port, "port", 6380, "TCP port (1-65535)")
	f.BoolVar(&c.AppendOnly, "appendonly", false, "enable synchronous append-only persistence")
	f.Int64Var(&c.RewriteSize, "aof-rewrite-size", 64<<20, "automatic rewrite threshold in bytes; 0 disables")
	f.StringVar(&c.Data, "data", "./data", "directory containing appendonly.aof")
	level := f.String("log-level", "info", "debug, info, warn, or error")
	f.IntVar(&c.MaxClients, "max-clients", 64, "maximum simultaneous connections")
	f.Int64Var(&c.MaxMemory, "maxmemory", 64<<20, "maximum accounted dataset bytes (no eviction)")
	passwordFile := f.String("password-file", "", "file containing the AUTH password; empty disables AUTH")
	f.DurationVar(&c.ReadTimeout, "read-timeout", 30*time.Second, "deadline for a complete command, including idle time")
	f.DurationVar(&c.WriteTimeout, "write-timeout", 5*time.Second, "response write deadline")
	f.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", 5*time.Second, "time to drain connections before closing them")
	if err := f.Parse(args); err != nil {
		return c, err
	}
	if f.NArg() != 0 {
		return c, errors.New("unexpected positional arguments")
	}
	if c.RewriteSize < 0 || c.RewriteSize > 1<<40 {
		return c, errors.New("invalid AOF rewrite threshold")
	}
	if c.MaxMemory <= 0 || c.MaxMemory > 1<<40 {
		return c, errors.New("maxmemory must be between 1 and 1099511627776 bytes")
	}
	var err error
	c.PrimaryPassword, err = ReadSecret(*primaryPasswordFile)
	if err != nil {
		return c, err
	}
	if c.ReplicaInterval <= 0 || c.ReplicaTimeout <= 0 {
		return c, errors.New("replica intervals must be positive")
	}
	if c.Primary != "" {
		host, port, err := net.SplitHostPort(c.Primary)
		if err != nil || host == "" {
			return c, errors.New("replicaof requires host:port")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return c, errors.New("invalid primary port")
		}
	} else if *primaryPasswordFile != "" {
		return c, errors.New("primary password requires replicaof")
	}
	c.Password, err = ReadSecret(*passwordFile)
	if err != nil {
		return c, err
	}
	if err := c.Level.UnmarshalText([]byte(*level)); err != nil {
		return c, err
	}
	if c.Port < 1 || c.Port > 65535 || c.MaxClients < 1 || c.ReadTimeout <= 0 || c.WriteTimeout <= 0 || c.ShutdownTimeout <= 0 || c.Host == "" || c.Data == "" {
		return c, errors.New("invalid address, port, client limit, data path, or timeout")
	}
	return c, nil
}

func (c Config) Address() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }
