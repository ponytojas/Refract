//go:build unix

package compile

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup starts cmd in its own process group and makes cancelling
// kill the whole group, so helpers tectonic spawned don't outlive a timeout.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// cpuLimited reports whether the process died of the CPU limit (SIGXCPU, or
// SIGKILL once past the hard limit; a timeout's own kill is told apart by the caller), straight or as bwrap relays it (128+n).
func cpuLimited(e *exec.ExitError) bool {
	if ws, ok := e.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			return ws.Signal() == syscall.SIGXCPU || ws.Signal() == syscall.SIGKILL
		}
		return ws.ExitStatus() == 128+int(syscall.SIGXCPU) || ws.ExitStatus() == 128+int(syscall.SIGKILL)
	}
	return false
}
