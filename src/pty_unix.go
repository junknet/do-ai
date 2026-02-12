//go:build !windows

package main

import (
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// unixPTY wraps creack/pty for Unix systems
type unixPTY struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

// startPTY starts a command with a pseudo-terminal on Unix
func startPTY(cmd *exec.Cmd) (PTY, error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &unixPTY{ptmx: ptmx, cmd: cmd}, nil
}

func (p *unixPTY) Read(buf []byte) (int, error) {
	return p.ptmx.Read(buf)
}

func (p *unixPTY) Write(buf []byte) (int, error) {
	return p.ptmx.Write(buf)
}

func (p *unixPTY) Resize(rows, cols uint16) error {
	return pty.Setsize(p.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

func (p *unixPTY) Close() error {
	err := p.ptmx.Close()
	// 关闭 ptmx 会向子进程发送 SIGHUP，但如果子进程忽略了 HUP 则需要强制终止。
	// 注意：不在此处调用 cmd.Wait()，避免与外部 Wait() 调用产生竞态。
	if p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
		time.AfterFunc(500*time.Millisecond, func() {
			// 延迟 SIGKILL 兜底：若进程已退出，Kill 返回 ESRCH（静默忽略）
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		})
	}
	return err
}

// Fd returns the file descriptor for the PTY master
func (p *unixPTY) Fd() uintptr {
	return p.ptmx.Fd()
}

// InheritSize copies the terminal size from stdin to the PTY
func (p *unixPTY) InheritSize(stdin *os.File) error {
	return pty.InheritSize(stdin, p.ptmx)
}

// AsWriter returns the PTY as an io.Writer
func (p *unixPTY) AsWriter() io.Writer {
	return p.ptmx
}

// Wait waits for the child process to exit and returns the exit code
func (p *unixPTY) Wait() (int, error) {
	err := p.cmd.Wait()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return 1, err
}
