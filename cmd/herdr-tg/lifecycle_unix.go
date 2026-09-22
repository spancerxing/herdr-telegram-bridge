//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/spancerxing/herdr-telegram-bridge/internal/app"
)

var errDaemonRunning = errors.New("daemon already running")

// Lock the canonical config location, so the standalone config and Herdr's
// symlink to it cannot start competing Telegram pollers. Never unlink this
// file: every process must lock the same inode. The kernel releases the lock
// on exit, including crashes; stale file contents do not imply a live daemon.
func acquireDaemonLock(configPath string) (*os.File, error) {
	canonical, err := filepath.EvalSymlinks(configPath)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(canonical), "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errDaemonRunning
		}
		return nil, err
	}
	return f, nil
}

func daemonLock(configPath string, inherited bool) (*os.File, error) {
	if inherited {
		f := os.NewFile(3, "daemon.lock")
		if _, err := f.Stat(); err != nil {
			f.Close()
			return nil, fmt.Errorf("inherited daemon lock: %w", err)
		}
		return f, nil
	}
	return acquireDaemonLock(configPath)
}

// Herdr startup hooks are one-shot commands, not supervised services. Like
// Telegram Agents, launch a detached child with durable logs; the child's
// health watcher ends it when Herdr goes away.
func runStartup(ctx context.Context, args []string, log *slog.Logger) error {
	configPath := app.ConfigPath(flagFirst(args, "config"))
	if _, err := app.LoadConfig(configPath, log); err != nil {
		return err
	}
	lock, err := acquireDaemonLock(configPath)
	if errors.Is(err, errDaemonRunning) {
		fmt.Println("daemon already running")
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()

	canonical, err := filepath.EvalSymlinks(configPath)
	if err != nil {
		return err
	}
	stateDir := os.Getenv("HERDR_PLUGIN_STATE_DIR")
	if stateDir == "" {
		stateDir = filepath.Dir(canonical)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(filepath.Join(stateDir, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer output.Close()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	childArgs := append([]string{"daemon", "--inherit-lock"}, args...)
	cmd := exec.Command(exe, childArgs...)
	cmd.Stdout, cmd.Stderr = output, output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Transfer the already-held lock atomically across exec. Closing the
	// parent's descriptor does not release the child's copy of the lock.
	cmd.ExtraFiles = []*os.File{lock}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Printf("daemon started (pid %d), log: %s\n", cmd.Process.Pid, output.Name())
	go func() { _ = cmd.Wait() }()
	return nil
}
