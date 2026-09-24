//go:build windows

package main

import (
	"errors"
	"syscall"
)

func isExpectedUDPNoise(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	// Windows UDP sockets may report ICMP from a stale peer as WSAECONNRESET or
	// WSAECONNABORTED. UrsusFlasher explicitly suppresses the same condition.
	return errno == syscall.Errno(10054) || errno == syscall.Errno(10053)
}
