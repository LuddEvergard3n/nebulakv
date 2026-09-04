package config

import (
	"io"
	"testing"
)

func TestFlags(t *testing.T) {
	c, err := Parse(nil, io.Discard)
	if err != nil || c.Address() != "127.0.0.1:6380" {
		t.Fatal(c, err)
	}
	for _, args := range [][]string{{"--port", "0"}, {"--max-clients", "0"}, {"--read-timeout", "0"}, {"--log-level", "wat"}, {"extra"}, {"--host", ""}} {
		if _, err := Parse(args, io.Discard); err == nil {
			t.Fatal(args)
		}
	}
}
