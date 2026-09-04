package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretAndMemoryFlags(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret")
	for _, bad := range []string{"", "a\nb", strings.Repeat("x", 4097)} {
		if err := os.WriteFile(p, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSecret(p); err == nil {
			t.Fatal("accepted invalid secret")
		}
	}
	if err := os.WriteFile(p, []byte("test-secret\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Parse([]string{"--password-file", p, "--maxmemory", "4096"}, io.Discard)
	if err != nil || c.Password != "test-secret" || c.MaxMemory != 4096 {
		t.Fatal("configuration failed", err)
	}
	if _, err := Parse([]string{"--maxmemory", "0"}, io.Discard); err == nil {
		t.Fatal("unlimited budget accepted")
	}
}
