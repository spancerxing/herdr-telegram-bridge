package system

import (
	"context"
	"testing"
	"time"
)

func TestDefaultIdleChecker(t *testing.T) {
	c := DefaultIdleChecker()
	if c == nil {
		t.Fatal("DefaultIdleChecker is nil")
	}
	d, err := c.IdleFor(context.Background())
	if err != nil {
		t.Logf("IdleFor returned error (expected if not on desktop GUI or unsupported): %v", err)
		return
	}
	if d < 0 {
		t.Fatalf("idle duration negative: %v", d)
	}
	t.Logf("current idle duration: %v", d.Round(time.Millisecond))
}
