package system

import (
	"context"
	"time"
)

// IdleChecker reports how long the system has been idle (no keyboard/mouse input).
type IdleChecker interface {
	IdleFor(ctx context.Context) (time.Duration, error)
}

// DefaultIdleChecker returns the platform-specific idle checker.
func DefaultIdleChecker() IdleChecker {
	return defaultChecker()
}
