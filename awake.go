package main

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// keepAwake holds a caffeinate assertion for as long as this process lives;
// -w makes caffeinate exit on its own if hatch dies without cleaning up.
func keepAwake() func() {
	noop := func() {}
	if runtime.GOOS != "darwin" {
		return noop
	}
	path, err := exec.LookPath("caffeinate")
	if err != nil {
		return noop
	}
	cmd := exec.Command(path, "-is", "-w", strconv.Itoa(os.Getpid()))
	if cmd.Start() != nil {
		return noop
	}
	return func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
}
