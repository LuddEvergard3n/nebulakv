//go:build !windows && !linux && !darwin && !freebsd

package persistence

import (
	"errors"
	"os"
)

func lock(*os.File) error { return errors.New("journal locking unsupported on this operating system") }
