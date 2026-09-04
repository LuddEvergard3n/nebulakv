package config

import (
	"errors"
	"io"
	"os"
	"strings"
)

// ReadSecret accepts one nonempty line, allowing a final CRLF from text editors.
func ReadSecret(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", err
	}
	s := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	if len(b) > 4096 || s == "" || strings.ContainsAny(s, "\r\n\x00") {
		return "", errors.New("password file must contain one nonempty line of at most 4096 bytes")
	}
	return s, nil
}
