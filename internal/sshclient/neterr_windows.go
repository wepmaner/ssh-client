package sshclient

import "golang.org/x/sys/windows"

// Winsock codes of a failed connect (connectex); syscall.ECONNREFUSED and friends are
// different numbers on Windows.
var (
	errRefused         error = windows.WSAECONNREFUSED
	errTimedOut        error = windows.WSAETIMEDOUT
	errHostUnreachable error = windows.WSAEHOSTUNREACH
	errNetUnreachable  error = windows.WSAENETUNREACH
	errAddrInUse       error = windows.WSAEADDRINUSE
	errAccess          error = windows.WSAEACCES
	errAddrNotAvail    error = windows.WSAEADDRNOTAVAIL
)
