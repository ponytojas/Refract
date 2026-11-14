//go:build !unix

package compile

import "os/exec"

// ownProcessGroup: no process groups here; cancelling kills tectonic itself.
// The server runs on Linux (the container); other builds are for `refract login`.
func ownProcessGroup(*exec.Cmd) {}

func cpuLimited(*exec.ExitError) bool { return false }
