//go:build windows

package main

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

func enableVirtualTerminal() func() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")
	stdout := syscall.Handle(os.Stdout.Fd())

	var mode uint32
	getConsoleMode.Call(uintptr(stdout), uintptr(unsafe.Pointer(&mode)))
	setConsoleMode.Call(uintptr(stdout), uintptr(mode|0x0004|0x0001|0x0002))

	return func() {
		setConsoleMode.Call(uintptr(stdout), uintptr(mode))
	}
}

func flushStdin() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procFlush := kernel32.NewProc("FlushConsoleInputBuffer")
	stdin := syscall.Handle(os.Stdin.Fd())
	procFlush.Call(uintptr(stdin))
}

func enterRawTerminal() func() {
	return func() {}
}

func readKeyWithTimeout(timeout time.Duration) KeyCode {
	msvcrt := syscall.NewLazyDLL("msvcrt.dll")
	procGetch := msvcrt.NewProc("_getch")
	procKbhit := msvcrt.NewProc("_kbhit")

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
					if int(r2) == '[' {
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
		time.Sleep(25 * time.Millisecond)
	}
}
