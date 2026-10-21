//go:build !windows

package main

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func enableVirtualTerminal() func() {
	return func() {}
}

func flushStdin() {
	fd := int(os.Stdin.Fd())
	pfds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	var discard [64]byte
	for {
		n, err := unix.Poll(pfds, 0)
		if err != nil || n <= 0 || (pfds[0].Revents&unix.POLLIN) == 0 {
			break
		}
		if _, err := os.Stdin.Read(discard[:]); err != nil {
			break
		}
	}
}

func enterRawTerminal() func() {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return func() {}
	}
	return func() {
		_ = term.Restore(fd, oldState)
	}
}

func readKeyWithTimeout(timeout time.Duration) KeyCode {
	fd := int(os.Stdin.Fd())
	pfds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}

	timeoutMs := int(timeout.Milliseconds())
	n, err := unix.Poll(pfds, timeoutMs)
	if err != nil || n <= 0 || (pfds[0].Revents&unix.POLLIN) == 0 {
		return KeyNone
	}

	var buf [16]byte
	nRead, err := os.Stdin.Read(buf[:])
	if err != nil || nRead == 0 {
		return KeyNone
	}

	if nRead >= 3 && buf[0] == 27 && (buf[1] == '[' || buf[1] == 'O') {
		switch buf[2] {
		case 'A':
			return KeyUp
		case 'B':
			return KeyDown
		}
		return KeyNone
	}

	if buf[0] == 27 && nRead > 1 {
		return KeyNone
	}

	switch buf[0] {
	case 32:
		return KeySpace
	case 13, 10:
		return KeyEnter
	case 'a', 'A':
		return KeyToggleAll
	case 'q', 'Q', 27, 3:
		return KeyQuit
	}

	return KeyNone
}
