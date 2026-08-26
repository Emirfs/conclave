//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

const exeSuffix = ""

// detachProcess puts the daemon in its own session so it survives the app.
func detachProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
