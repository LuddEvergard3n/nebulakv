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
	Host                                       string
	Port                                       int
	AppendOnly                                 bool
	Data                                       string
	Level                                      slog.Level
	MaxClients                                 int
	ReadTimeout, WriteTimeout, ShutdownTimeout time.Duration
}

func Parse(args []string, out io.Writer) (Config, error) {
	c := Config{}
	f := flag.NewFlagSet("nebulakv", flag.ContinueOnError)
	f.SetOutput(out)
	f.StringVar(&c.Host, "host", "127.0.0.1", "TCP bind address")
	f.IntVar(&c.Port, "port", 6380, "TCP port (1-65535)")
	f.BoolVar(&c.AppendOnly, "appendonly", false, "enable synchronous append-only persistence")
	f.StringVar(&c.Data, "data", "./data", "directory containing appendonly.aof")
	level := f.String("log-level", "info", "debug, info, warn, or error")
	f.IntVar(&c.MaxClients, "max-clients", 64, "maximum simultaneous connections")
	f.DurationVar(&c.ReadTimeout, "read-timeout", 30*time.Second, "deadline for a complete command, including idle time")
	f.DurationVar(&c.WriteTimeout, "write-timeout", 5*time.Second, "response write deadline")
	f.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", 5*time.Second, "time to drain connections before closing them")
	if err := f.Parse(args); err != nil {
		return c, err
	}
	if f.NArg() != 0 {
		return c, errors.New("unexpected positional arguments")
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
