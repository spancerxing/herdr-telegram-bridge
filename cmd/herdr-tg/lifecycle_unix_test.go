//go:build darwin || linux

package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDaemonLockSharedByConfigSymlinkAndReleasedOnClose(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "linked.json")
	if err := os.Symlink(config, alias); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireDaemonLock(config)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if duplicate, err := acquireDaemonLock(alias); !errors.Is(err, errDaemonRunning) {
		if duplicate != nil {
			duplicate.Close()
		}
		t.Fatalf("symlink started a second daemon: %v", err)
	}
	lock.Close()
	lock, err = acquireDaemonLock(alias)
	if err != nil {
		t.Fatalf("stale lock file prevented restart: %v", err)
	}
	lock.Close()
}

func TestInheritedDaemonLock(t *testing.T) {
	if os.Getenv("HERDR_TEST_LOCK_CHILD") == "1" {
		lock, err := daemonLock("", true)
		if err != nil {
			os.Exit(2)
		}
		defer lock.Close()
		_, _ = os.Stdout.WriteString("ready\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireDaemonLock(config)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestInheritedDaemonLock$")
	child.Env = append(os.Environ(), "HERDR_TEST_LOCK_CHILD=1")
	child.ExtraFiles = []*os.File{lock}
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); _ = child.Wait() })
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child did not inherit lock: %q, %v", line, err)
	}
	lock.Close() // the startup hook exits, but the daemon keeps ownership
	if duplicate, err := acquireDaemonLock(config); !errors.Is(err, errDaemonRunning) {
		if duplicate != nil {
			duplicate.Close()
		}
		t.Fatalf("parent exit released child's lock: %v", err)
	}
	input.Close()
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	lock, err = acquireDaemonLock(config)
	if err != nil {
		t.Fatalf("child exit did not release lock: %v", err)
	}
	lock.Close()
}
