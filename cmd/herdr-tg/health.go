package main

import (
	"context"
	"fmt"
	"time"
)

const herdrDisconnectGrace = 5 * time.Second

// A brief socket interruption during a server handoff is recoverable. A
// sustained outage means the host stopped, and the bridge must stop too.
func watchHerdr(ctx context.Context, ticks <-chan time.Time, ping func(context.Context) error) error {
	var downSince time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticks:
			probe, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := ping(probe)
			cancel()
			if err == nil {
				downSince = time.Time{}
				continue
			}
			if downSince.IsZero() {
				downSince = now
			}
			if now.Sub(downSince) >= herdrDisconnectGrace {
				return fmt.Errorf("Herdr unreachable for %s: %w", herdrDisconnectGrace, err)
			}
		}
	}
}
