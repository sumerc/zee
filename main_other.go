//go:build !linux

package main

import (
	"runtime"

	"golang.design/x/hotkey/mainthread"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	mainthread.Init(run)
}
