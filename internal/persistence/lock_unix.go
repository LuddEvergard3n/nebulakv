//go:build linux || darwin || freebsd

package persistence

import (
	"os"
	"syscall"
)

func lock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
