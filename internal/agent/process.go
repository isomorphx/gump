package agent

import (
	"bufio"
	"context"
	"fmt"
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

// Start builds stdout/stderr pipes, records each stdout line with an observation timestamp to the artefact file,
// exposes a pipe of raw lines to the caller for streaming, starts the command in dir, and returns a Process.
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

	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader := io.TeeReader(stderrPipe, stderrFile)

	if err := cmd.Start(); err != nil {
		stdoutFile.Close()
		stderrFile.Close()
		stdoutReader.Close()
		_ = stdoutPipe.Close()
		return nil, err
	}

	go func() {
		defer stdoutWriter.Close()
		defer stdoutFile.Close()
		r := bufio.NewReader(stdoutPipe)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 && line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 {
				// WHY: ledger consumers need when Gump observed each line, distinct from provider timestamps.
				ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
				fmt.Fprintf(stdoutFile, "%s %s\n", ts, line)
				if _, werr := stdoutWriter.Write(append(append([]byte{}, line...), '\n')); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	proc := &Process{
		Cmd:        cmd,
		Stdout:     stdoutReader,
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

// Terminate asks a process to stop now using the same grace policy as timeouts.
func Terminate(proc *Process) {
	if proc == nil || proc.Cmd == nil || proc.Cmd.Process == nil {
		return
	}
	_ = proc.Cmd.Process.Signal(syscall.SIGTERM)
	time.Sleep(KillGrace)
	if proc.Cmd.Process != nil {
		_ = proc.Cmd.Process.Kill()
	}
}
