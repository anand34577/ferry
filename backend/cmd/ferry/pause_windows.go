package main

import (
	"bufio"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// pauseIfDoubleClicked keeps the window open after an error when Ferry was started by double-clicking
// (the console then belongs to Ferry alone), so the message can be read.
func pauseIfDoubleClicked() {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	var ids [2]uint32
	n, _, _ := proc.Call(uintptr(unsafe.Pointer(&ids[0])), 2)
	if n == 1 {
		fmt.Fprint(os.Stderr, "\nPress Enter to close this window.")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
}
