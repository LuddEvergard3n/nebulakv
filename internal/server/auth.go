package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"nebulakv/internal/resp"
)

func (s *Server) authenticate(args []string) (bool, resp.Value) {
	if len(args) != 2 && len(args) != 3 {
		return false, resp.Err("ERR wrong number of arguments for AUTH")
	}
	if s.config.Password == "" {
		return false, resp.Err("ERR AUTH called without a configured password")
	}
	want := sha256.Sum256([]byte(s.config.Password))
	got := sha256.Sum256([]byte(args[len(args)-1]))
	match := subtle.ConstantTimeCompare(want[:], got[:]) == 1
	if len(args) == 3 && args[1] != "default" {
		match = false
	}
	if !match {
		return false, resp.Err("WRONGPASS invalid username-password pair")
	}
	return true, resp.Status("OK")
}
