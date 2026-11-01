package check

import (
	"errors"
	"syscall"
)

func isConnectionRefused(err error) bool {
	// Winsock's WSAECONNREFUSED is 10061; syscall does not export its name.
	return errors.Is(err, syscall.Errno(10061)) || errors.Is(err, syscall.ECONNREFUSED)
}
