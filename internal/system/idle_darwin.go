//go:build darwin

package system

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

var hidIdleTime = regexp.MustCompile(`"HIDIdleTime"\s*=\s*(\d+)`)

func defaultChecker() IdleChecker {
	return DarwinIdleChecker{}
}

// DarwinIdleChecker queries IOKit via ioreg on macOS.
type DarwinIdleChecker struct{}

func (DarwinIdleChecker) IdleFor(ctx context.Context) (time.Duration, error) {
	out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem", "-d", "4").Output()
	if err != nil {
		return 0, err
	}
	m := hidIdleTime.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("HIDIdleTime not found in ioreg output")
	}
	ns, err := strconv.ParseUint(string(m[1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(ns), nil
}
