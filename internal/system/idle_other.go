//go:build !darwin

package system

import (
	"context"
	"errors"
	"time"
)

func defaultChecker() IdleChecker {
	return OtherIdleChecker{}
}

// OtherIdleChecker is a fallback for non-macOS platforms.
type OtherIdleChecker struct{}

func (OtherIdleChecker) IdleFor(ctx context.Context) (time.Duration, error) {
	return 0, errors.New("idle detection not supported on this platform")
}
