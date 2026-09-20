package sandbox

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
)

// PTYSession holds the virtual terminal file and the running process.
type PTYSession struct {
	PTY       *os.File
	Cmd       *exec.Cmd
	cancelCtx context.CancelFunc
}

// StartSession spawns a command inside a PTY with a strict timeout.
func StartSession(timeout time.Duration, command string, args ...string) (*PTYSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	cmd := exec.CommandContext(ctx, command, args...)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return nil, err
	}

	return &PTYSession{
		PTY:       ptmx,
		Cmd:       cmd,
		cancelCtx: cancel,
	}, nil
}
