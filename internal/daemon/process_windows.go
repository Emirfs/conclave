//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

func configureProcessTree(command *exec.Cmd) {
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		killer := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid))
		if output, err := killer.CombinedOutput(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill process tree: %w: %s", err, output)
		}
		return nil
	}
}
