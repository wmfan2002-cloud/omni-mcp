//go:build !windows

package process

import (
	"fmt"
	"os"
	"os/exec"
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func Terminate(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGTERM)
}

func Kill(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGKILL)
}

func SignalGroup(cmd *exec.Cmd, signal Signal) error {
	switch signal {
	case SignalTERM:
		return signalGroup(cmd, syscall.SIGTERM)
	case SignalKILL:
		return signalGroup(cmd, syscall.SIGKILL)
	case SignalINT:
		return signalGroup(cmd, syscall.SIGINT)
	case SignalHUP:
		return signalGroup(cmd, syscall.SIGHUP)
	case SignalQUIT:
		return signalGroup(cmd, syscall.SIGQUIT)
	case SignalUSR1:
		return signalGroup(cmd, syscall.SIGUSR1)
	case SignalUSR2:
		return signalGroup(cmd, syscall.SIGUSR2)
	default:
		return fmt.Errorf("unsupported process signal %d", signal)
	}
}

func signalGroup(cmd *exec.Cmd, signal syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, signal); err != nil {
		if err := cmd.Process.Signal(signal); err != nil {
			return fmt.Errorf("signal process group: %w", err)
		}
	}
	return nil
}

func Interrupts() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
