package sandbox

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// PTYSession holds the virtual terminal file and the running process.
type PTYSession struct {
	PTY       *os.File
	Cmd       *exec.Cmd
	cancelCtx context.CancelFunc
}

// StartSession spawns a command inside a PTY with a strict timeout.
func StartSession(timeout time.Duration, command string, args ...string) (*PTYSession, error) {
	ctx, cancel := contect.WithTime
}
