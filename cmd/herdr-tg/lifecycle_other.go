//go:build !darwin && !linux

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

func daemonLock(string, bool) (*os.File, error) {
	return nil, fmt.Errorf("daemon lifecycle is supported on macOS and Linux")
}

func runStartup(context.Context, []string, *slog.Logger) error {
	return fmt.Errorf("daemon lifecycle is supported on macOS and Linux")
}
