//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const exeSuffix = ".exe"

// detachProcess keeps the spawned daemon alive after the app exits and stops it
// from flashing a console window.
func detachProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
}
