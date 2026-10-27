//go:build !windows

package sshclient

import "syscall"

var (
	errRefused         error = syscall.ECONNREFUSED
	errTimedOut        error = syscall.ETIMEDOUT
	errHostUnreachable error = syscall.EHOSTUNREACH
	errNetUnreachable  error = syscall.ENETUNREACH
	errAddrInUse       error = syscall.EADDRINUSE
	errAccess          error = syscall.EACCES
	errAddrNotAvail    error = syscall.EADDRNOTAVAIL
)
