package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

type Signal int

const (
	SignalTERM Signal = iota
	SignalKILL
	SignalINT
	SignalHUP
	SignalQUIT
	SignalUSR1
	SignalUSR2
)

func Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000200 | 0x08000000,
	}
}

func Terminate(cmd *exec.Cmd) error { return taskkill(cmd) }
func Kill(cmd *exec.Cmd) error      { return taskkill(cmd) }

func SignalGroup(cmd *exec.Cmd, signal Signal) error {
	return taskkill(cmd)
}

func taskkill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	killerPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
	killer := exec.Command(killerPath, "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	killer.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH")}
	Configure(killer)
	if err := killer.Run(); err != nil {
		if processErr := cmd.Process.Kill(); processErr != nil {
			return fmt.Errorf("stop Windows process tree: %w", processErr)
		}
	}
	return nil
}

func Interrupts() []os.Signal { return []os.Signal{os.Interrupt} }
