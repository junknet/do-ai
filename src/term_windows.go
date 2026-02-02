//go:build windows

package main

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// termState holds the terminal state for Windows
type termState struct {
	inHandle  windows.Handle
	outHandle windows.Handle
	inMode    uint32
	outMode   uint32
}

// isTerminal returns true if the file descriptor is a terminal
func isTerminal(fd int) bool {
	h := windows.Handle(fd)
	var mode uint32
	err := windows.GetConsoleMode(h, &mode)
	return err == nil
}

// makeRaw puts the terminal into raw mode and returns a state to restore later
func makeRaw(fd int) (*termState, error) {
	inHandle := windows.Handle(os.Stdin.Fd())
	outHandle := windows.Handle(os.Stdout.Fd())

	var inMode, outMode uint32

	// Save current input mode
	if err := windows.GetConsoleMode(inHandle, &inMode); err != nil {
		return nil, err
	}

	// Save current output mode
	if err := windows.GetConsoleMode(outHandle, &outMode); err != nil {
		return nil, err
	}

	// Set raw input mode
	// Disable: ENABLE_ECHO_INPUT, ENABLE_LINE_INPUT, ENABLE_PROCESSED_INPUT
	// Enable: ENABLE_VIRTUAL_TERMINAL_INPUT
	rawInMode := inMode
	rawInMode &^= windows.ENABLE_ECHO_INPUT
	rawInMode &^= windows.ENABLE_LINE_INPUT
	rawInMode &^= windows.ENABLE_PROCESSED_INPUT
	rawInMode |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT

	if err := windows.SetConsoleMode(inHandle, rawInMode); err != nil {
		return nil, err
	}

	// Enable virtual terminal processing for output
	rawOutMode := outMode
	rawOutMode |= windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	rawOutMode |= windows.DISABLE_NEWLINE_AUTO_RETURN

	if err := windows.SetConsoleMode(outHandle, rawOutMode); err != nil {
		// Restore input mode on failure
		_ = windows.SetConsoleMode(inHandle, inMode)
		return nil, err
	}

	return &termState{
		inHandle:  inHandle,
		outHandle: outHandle,
		inMode:    inMode,
		outMode:   outMode,
	}, nil
}

// restore restores the terminal to its previous state
func (t *termState) restore() error {
	var err error
	if e := windows.SetConsoleMode(t.inHandle, t.inMode); e != nil {
		err = e
	}
	if e := windows.SetConsoleMode(t.outHandle, t.outMode); e != nil && err == nil {
		err = e
	}
	return err
}

// getConsoleSize returns the current console size (cols, rows)
func getConsoleSize() (int, int) {
	h := windows.Handle(os.Stdout.Fd())
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(h, &info); err != nil {
		return 80, 24 // Default size
	}
	cols := int(info.Window.Right - info.Window.Left + 1)
	rows := int(info.Window.Bottom - info.Window.Top + 1)
	return cols, rows
}

// setupWinchHandler sets up window size change handling for Windows
// Windows doesn't have SIGWINCH, so we use polling instead
func setupWinchHandler(pty PTY) chan<- struct{} {
	trigger := make(chan struct{}, 1)

	go func() {
		lastCols, lastRows := getConsoleSize()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				cols, rows := getConsoleSize()
				if cols != lastCols || rows != lastRows {
					_ = pty.Resize(uint16(rows), uint16(cols))
					lastCols, lastRows = cols, rows
				}
			case <-trigger:
				cols, rows := getConsoleSize()
				_ = pty.Resize(uint16(rows), uint16(cols))
				lastCols, lastRows = cols, rows
			}
		}
	}()

	// Initial size sync
	trigger <- struct{}{}

	return trigger
}
