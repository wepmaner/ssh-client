//go:build !windows

package sshclient

import "syscall"

var (
	errRefused         error = syscall.ECONNREFUSED
	errTimedOut        error = syscall.ETIMEDOUT
	errHostUnreachable error = syscall.EHOSTUNREACH
	errNetUnreachable  error = syscall.ENETUNREACH
)
