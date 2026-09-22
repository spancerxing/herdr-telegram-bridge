package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWatchHerdrRecoversBriefOutageAndStopsAfterHostExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	results := make(chan error, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- watchHerdr(ctx, ticks, func(context.Context) error { return <-results })
	}()
	offline := errors.New("socket unavailable")
	for _, step := range []struct {
		second int
		err    error
	}{
		{0, offline}, {4, offline}, {5, nil}, // recovery resets the grace period
		{6, offline}, {10, offline}, {11, offline},
	} {
		results <- step.err
		select {
		case ticks <- time.Unix(100+int64(step.second), 0):
		case err := <-finished:
			t.Fatalf("stopped too early at %ds: %v", step.second, err)
		case <-time.After(time.Second):
			t.Fatal("health watcher stalled")
		}
	}
	select {
	case err := <-finished:
		if !errors.Is(err, offline) {
			t.Fatalf("exit cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge kept running after Herdr stayed offline")
	}
}

func TestWatchHerdrStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := watchHerdr(ctx, nil, func(context.Context) error {
		t.Fatal("ping called after cancellation")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
