package webrtc

import (
	"io"
	"os/exec"
)

// BuildScreenrecordArgs returns the adb command args for H264 streaming.
func BuildScreenrecordArgs(adbSerial string) []string {
	return []string{
		"adb", "-s", adbSerial,
		"exec-out",
		"screenrecord",
		"--output-format=h264",
		"--time-limit=0",
		"-",
	}
}

// ScreenCapture manages an adb screenrecord subprocess.
type ScreenCapture struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

// StartCapture launches the adb screenrecord process and returns the H264 byte stream.
func StartCapture(adbSerial string) (*ScreenCapture, io.ReadCloser, error) {
	args := BuildScreenrecordArgs(adbSerial)
	cmd := exec.Command(args[0], args[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return &ScreenCapture{cmd: cmd, stdout: stdout}, stdout, nil
}

// Stop kills the screenrecord subprocess.
func (c *ScreenCapture) Stop() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}
