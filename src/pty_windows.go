//go:build windows

package main

import (
	"context"
	"io"
	"os"
	"os/exec"

	"github.com/UserExistsError/conpty"
)

// windowsPTY wraps conpty for Windows systems
type windowsPTY struct {
	cpty *conpty.ConPty
}

// startPTY starts a command with a pseudo-terminal on Windows using ConPTY
func startPTY(cmd *exec.Cmd) (PTY, error) {
	// Get initial console size
	cols, rows := getConsoleSize()

	// Build command line
	cmdLine := cmd.Path
	if len(cmd.Args) > 1 {
		for _, arg := range cmd.Args[1:] {
			cmdLine += " " + arg
		}
	}

	// Create ConPTY with initial size
	cpty, err := conpty.Start(cmdLine, conpty.ConPtyDimensions(cols, rows))
	if err != nil {
		return nil, err
	}

	return &windowsPTY{cpty: cpty}, nil
}

func (p *windowsPTY) Read(buf []byte) (int, error) {
	return p.cpty.Read(buf)
}

func (p *windowsPTY) Write(buf []byte) (int, error) {
	return p.cpty.Write(buf)
}

func (p *windowsPTY) Resize(rows, cols uint16) error {
	return p.cpty.Resize(int(cols), int(rows))
}

func (p *windowsPTY) Close() error {
	return p.cpty.Close()
}

// Fd returns 0 on Windows as ConPTY doesn't expose a file descriptor
func (p *windowsPTY) Fd() uintptr {
	return 0
}

// InheritSize copies the terminal size from stdin to the PTY
func (p *windowsPTY) InheritSize(_ *os.File) error {
	cols, rows := getConsoleSize()
	return p.Resize(uint16(rows), uint16(cols))
}

// AsWriter returns the PTY as an io.Writer
func (p *windowsPTY) AsWriter() io.Writer {
	return p.cpty
}

// Wait waits for the ConPTY process to exit and returns the exit code
func (p *windowsPTY) Wait() (int, error) {
	exitCode, err := p.cpty.Wait(context.Background())
	return int(exitCode), err
}
