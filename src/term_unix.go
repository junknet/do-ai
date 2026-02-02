//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// termState holds the terminal state for Unix
type termState struct {
	fd    int
	state *term.State
}

// isTerminal returns true if the file descriptor is a terminal
func isTerminal(fd int) bool {
	return term.IsTerminal(fd)
}

// makeRaw puts the terminal into raw mode and returns a state to restore later
func makeRaw(fd int) (*termState, error) {
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return &termState{fd: fd, state: state}, nil
}

// restore restores the terminal to its previous state
func (t *termState) restore() error {
	return term.Restore(t.fd, t.state)
}

// setupWinchHandler sets up SIGWINCH handling for window size changes
// Returns a channel that can be used to manually trigger a resize
func setupWinchHandler(pty PTY) chan<- struct{} {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)

	trigger := make(chan struct{}, 1)

	go func() {
		for {
			select {
			case <-winch:
				_ = pty.InheritSize(os.Stdin)
			case <-trigger:
				_ = pty.InheritSize(os.Stdin)
			}
		}
	}()

	// Initial size sync
	trigger <- struct{}{}

	return trigger
}
