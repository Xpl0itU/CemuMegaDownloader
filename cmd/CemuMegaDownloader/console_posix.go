//go:build !windows

package main

import (
	"os"
	"time"

	"golang.org/x/term"
)

func enableVirtualTerminal() func() {
	return func() {}
}

func flushStdin() {}

func enterRawTerminal() func() {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return func() {}
	}
	return func() {
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}
}

func readKeyWithTimeout(timeout time.Duration) KeyCode {
	_ = os.Stdin.SetReadDeadline(time.Now().Add(timeout))
	defer func() {
		_ = os.Stdin.SetReadDeadline(time.Time{})
	}()

	var buf [3]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil || n == 0 {
		return KeyNone
	}
	if n == 3 && buf[0] == 27 && buf[1] == '[' {
		switch buf[2] {
		case 'A':
			return KeyUp
		case 'B':
			return KeyDown
		}
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
