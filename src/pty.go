package main

import (
	"io"
	"os"
)

// PTY is the platform-independent interface for pseudo-terminal operations
type PTY interface {
	// Read reads data from the PTY
	Read(p []byte) (n int, err error)

	// Write writes data to the PTY
	Write(p []byte) (n int, err error)

	// Resize changes the PTY window size
	Resize(rows, cols uint16) error

	// Close closes the PTY
	Close() error

	// Fd returns the file descriptor (Unix) or 0 (Windows)
	Fd() uintptr

	// InheritSize copies the terminal size from stdin to the PTY
	InheritSize(stdin *os.File) error

	// AsWriter returns the PTY as an io.Writer
	AsWriter() io.Writer

	// Wait waits for the child process to exit and returns the exit code
	Wait() (int, error)
}
