//go:build !windows

package token

import (
	"errors"
	"os"
	"syscall"
)

func tryLockFile(f *os.File) (func() error, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	return func() error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }, err
}

func lockContended(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
