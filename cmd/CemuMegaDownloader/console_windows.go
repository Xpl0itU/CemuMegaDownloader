//go:build windows

package main

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

var (
	kernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	procFlushConsoleInputBuffer = kernel32.NewProc("FlushConsoleInputBuffer")

	msvcrt    = windows.NewLazySystemDLL("msvcrt.dll")
	procGetch = msvcrt.NewProc("_getch")
	procKbhit = msvcrt.NewProc("_kbhit")
)

func enableVirtualTerminal() func() {
	stdout := windows.Handle(os.Stdout.Fd())

	origOutCP, _ := windows.GetConsoleOutputCP()
	_ = windows.SetConsoleOutputCP(65001)

	var mode uint32
	err := windows.GetConsoleMode(stdout, &mode)
	if err == nil {
		const vtFlags = windows.ENABLE_PROCESSED_OUTPUT |
			windows.ENABLE_WRAP_AT_EOL_OUTPUT |
			windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
		_ = windows.SetConsoleMode(stdout, mode|vtFlags)
	}

	return func() {
		if err == nil {
			_ = windows.SetConsoleMode(stdout, mode)
		}
		if origOutCP != 0 {
			_ = windows.SetConsoleOutputCP(origOutCP)
		}
	}
}

func flushStdin() {
	stdin := windows.Handle(os.Stdin.Fd())
	_, _, _ = procFlushConsoleInputBuffer.Call(uintptr(stdin))
	for {
		hit, _, _ := procKbhit.Call()
		if hit == 0 {
			break
		}
		_, _, _ = procGetch.Call()
	}
}

func enterRawTerminal() func() {
	return func() {}
}

func readKeyWithTimeout(timeout time.Duration) KeyCode {
	deadline := time.Now().Add(timeout)
	for {
		hit, _, _ := procKbhit.Call()
		if hit != 0 {
			r, _, _ := procGetch.Call()
			ch := int(r)
			if ch == 0 || ch == 224 {
				r2, _, _ := procGetch.Call()
				switch int(r2) {
				case 72:
					return KeyUp
				case 80:
					return KeyDown
				}
				return KeyNone
			}
			if ch == 27 {
				h, _, _ := procKbhit.Call()
				if h != 0 {
					r2, _, _ := procGetch.Call()
					if int(r2) == '[' || int(r2) == 'O' {
						r3, _, _ := procGetch.Call()
						switch int(r3) {
						case 'A':
							return KeyUp
						case 'B':
							return KeyDown
						}
					}
					return KeyNone
				}
				return KeyQuit
			}
			switch ch {
			case 32:
				return KeySpace
			case 13, 10:
				return KeyEnter
			case 'a', 'A':
				return KeyToggleAll
			case 'q', 'Q', 3:
				return KeyQuit
			}
			return KeyNone
		}
		if time.Now().After(deadline) {
			return KeyNone
		}
		time.Sleep(15 * time.Millisecond)
	}
}
