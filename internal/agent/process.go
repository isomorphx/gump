package agent

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	// KillGrace is how long we wait after SIGTERM before sending SIGKILL so the CLI can flush and exit cleanly.
	KillGrace = 5 * time.Second
)

// Start builds stdout/stderr pipes, tees stdout to the artefact file, starts the command in dir, and returns a Process.
// Caller must call Wait(process) and may attach a timeout via WithTimeout(process, timeout).
func Start(ctx context.Context, cmd *exec.Cmd, dir, stdoutPath, stderrPath string) (*Process, error) {
	cmd.Dir = dir

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, err
	}
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		stdoutFile.Close()
		return nil, err
	}

	stdoutReader := io.TeeReader(stdoutPipe, stdoutFile)
	stderrReader := io.TeeReader(stderrPipe, stderrFile)

	if err := cmd.Start(); err != nil {
		stdoutFile.Close()
		stderrFile.Close()
		return nil, err
	}

	proc := &Process{
		Cmd:        cmd,
		Stdout:     &readCloserWithFile{Reader: stdoutReader, file: stdoutFile},
		Stderr:     &readCloserWithFile{Reader: stderrReader, file: stderrFile},
		StdoutFile: stdoutPath,
		StderrFile: stderrPath,
	}
	go func() {
		_, _ = io.Copy(io.Discard, proc.Stderr)
		_ = proc.Stderr.Close()
	}()

	return proc, nil
}

type readCloserWithFile struct {
	io.Reader
	file *os.File
}

func (r *readCloserWithFile) Close() error {
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

// WithTimeout starts a goroutine that sends SIGTERM when ctx expires, then SIGKILL after KillGrace.
// Sets Process.TimedOut so Wait can return the spec-compliant timeout RunResult.
func WithTimeout(proc *Process, timeout time.Duration) context.CancelFunc {
	if timeout <= 0 {
		return func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	go func() {
		<-ctx.Done()
		if proc.Cmd == nil || proc.Cmd.Process == nil {
			return
		}
		proc.TimedOut = true
		_ = proc.Cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(KillGrace)
		if proc.Cmd.Process != nil {
			_ = proc.Cmd.Process.Kill()
		}
	}()
	return cancel
}
